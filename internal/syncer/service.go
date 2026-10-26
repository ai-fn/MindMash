package syncer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

// defaultPageSize is per entity type, so a page still fits one response.
const defaultPageSize = 500

// maxConcurrentReads caps pool connections one GetChanges call holds at once.
const maxConcurrentReads = 4

// Service assembles the delta feed and fans nudges out over Redis, so an event consumed by one
// replica reaches SSE clients on every replica.
type Service struct {
	repo     Repository
	coll     CollectionClient
	rdb      *redis.Client
	hub      *notifyHub
	log      *zap.Logger
	pageSize int // per-entity-type page size; tests shrink this to exercise pagination cheaply
}

// NewService constructs a Service.
func NewService(repo Repository, coll CollectionClient, rdb *redis.Client, log *zap.Logger) *Service {
	return &Service{repo: repo, coll: coll, rdb: rdb, hub: newNotifyHub(), log: log, pageSize: defaultPageSize}
}

// GetChanges returns one page of entities past sinceUSN (or pageToken's cursors) and the latest usn.
func (s *Service) GetChanges(ctx context.Context, userID string, sinceUSN int64, pageToken string) (ChangeSet, int64, string, error) {
	cursors, pinned, err := decodePageToken(pageToken)
	if err != nil {
		return ChangeSet{}, 0, "", apperr.InvalidArgument("invalid page_token", err)
	}
	// Read the ceiling before any rows: usn is allocated under a per-user row lock held to commit,
	// so everything <= latest is already visible and nothing newer can sneak under it.
	latestUSN, err := s.repo.LatestUSN(ctx, userID)
	if err != nil {
		return ChangeSet{}, 0, "", fmt.Errorf("sync: get changes: %w", err)
	}
	if pageToken == "" && latestUSN <= sinceUSN {
		return ChangeSet{}, latestUSN, "", nil
	}
	// Keep the pass's ceiling, but never above the real counter (stale token after a restore, or forged).
	if pinned > 0 {
		latestUSN = min(latestUSN, pinned)
	}

	cursorFor := func(entityType string) Cursor {
		if c, ok := cursors[entityType]; ok {
			return c
		}
		return Cursor{USN: sinceUSN}
	}

	fetchLimit := s.pageSize + 1 // fetch one extra to detect "more rows exist"

	// Independent reads, run concurrently: clients pull on every load, focus, write and heartbeat.
	var (
		noteTypes     []NoteType
		notes         []Note
		cards         []Card
		decks         []Deck
		deckConfigs   []DeckConfig
		filteredDecks []FilteredDeck
		tags          []Tag
		deleted       []Tombstone
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentReads)
	g.Go(func() (err error) {
		noteTypes, err = s.repo.ListNoteTypesSince(gctx, userID, cursorFor(EntityNoteType), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		notes, err = s.repo.ListNotesSince(gctx, userID, cursorFor(EntityNote), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		cards, err = s.repo.ListCardsSince(gctx, userID, cursorFor(EntityCard), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		decks, err = s.repo.ListDecksSince(gctx, userID, cursorFor(EntityDeck), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		deckConfigs, err = s.repo.ListDeckConfigsSince(gctx, userID, cursorFor(EntityDeckConfig), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		filteredDecks, err = s.repo.ListFilteredDecksSince(gctx, userID, cursorFor(EntityFilteredDeck), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		tags, err = s.repo.ListTagsSince(gctx, userID, cursorFor(EntityTag), latestUSN, fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		deleted, err = s.repo.ListTombstonesSince(gctx, userID, cursorFor(tombstoneCursorKey), latestUSN, fetchLimit)
		return err
	})
	if err := g.Wait(); err != nil {
		return ChangeSet{}, 0, "", fmt.Errorf("sync: get changes: %w", err)
	}

	next := Cursors{}
	hasMore := false

	noteTypes, more := truncate(noteTypes, s.pageSize)
	hasMore = hasMore || more
	if len(noteTypes) > 0 {
		next[EntityNoteType] = noteTypes[len(noteTypes)-1].cursor()
	}

	notes, more = truncate(notes, s.pageSize)
	hasMore = hasMore || more
	if len(notes) > 0 {
		next[EntityNote] = notes[len(notes)-1].cursor()
	}

	cards, more = truncate(cards, s.pageSize)
	hasMore = hasMore || more
	if len(cards) > 0 {
		next[EntityCard] = cards[len(cards)-1].cursor()
	}

	decks, more = truncate(decks, s.pageSize)
	hasMore = hasMore || more
	if len(decks) > 0 {
		next[EntityDeck] = decks[len(decks)-1].cursor()
	}

	deckConfigs, more = truncate(deckConfigs, s.pageSize)
	hasMore = hasMore || more
	if len(deckConfigs) > 0 {
		next[EntityDeckConfig] = deckConfigs[len(deckConfigs)-1].cursor()
	}

	filteredDecks, more = truncate(filteredDecks, s.pageSize)
	hasMore = hasMore || more
	if len(filteredDecks) > 0 {
		next[EntityFilteredDeck] = filteredDecks[len(filteredDecks)-1].cursor()
	}

	tags, more = truncate(tags, s.pageSize)
	hasMore = hasMore || more
	if len(tags) > 0 {
		next[EntityTag] = tags[len(tags)-1].cursor()
	}

	deleted, more = truncate(deleted, s.pageSize)
	hasMore = hasMore || more
	if len(deleted) > 0 {
		next[tombstoneCursorKey] = deleted[len(deleted)-1].cursor()
	}

	cs := ChangeSet{
		NoteTypes:     noteTypes,
		Notes:         notes,
		Cards:         cards,
		Decks:         decks,
		DeckConfigs:   deckConfigs,
		FilteredDecks: filteredDecks,
		Tags:          tags,
		Deleted:       deleted,
	}

	nextToken := ""
	if hasMore {
		// Carry forward types exhausted on an earlier page so they aren't re-scanned.
		for k, v := range cursors {
			if _, ok := next[k]; !ok {
				next[k] = v
			}
		}
		token, err := encodePageToken(next, latestUSN)
		if err != nil {
			return ChangeSet{}, 0, "", fmt.Errorf("sync: encode page token: %w", err)
		}
		nextToken = token
	}

	return cs, latestUSN, nextToken, nil
}

func (nt NoteType) cursor() Cursor     { return Cursor{USN: nt.USN, ID: nt.ID} }
func (n Note) cursor() Cursor          { return Cursor{USN: n.USN, ID: n.ID} }
func (c Card) cursor() Cursor          { return Cursor{USN: c.USN, ID: c.ID} }
func (d Deck) cursor() Cursor          { return Cursor{USN: d.USN, ID: d.ID} }
func (dc DeckConfig) cursor() Cursor   { return Cursor{USN: dc.USN, ID: dc.ID} }
func (fd FilteredDeck) cursor() Cursor { return Cursor{USN: fd.USN, ID: fd.ID} }
func (t Tag) cursor() Cursor           { return Cursor{USN: t.USN, ID: t.ID} }
func (t Tombstone) cursor() Cursor     { return Cursor{USN: t.USN, Type: t.Type, ID: t.ID} }

// truncate trims items to pageSize and reports whether there were more.
func truncate[T any](items []T, pageSize int) ([]T, bool) {
	if len(items) > pageSize {
		return items[:pageSize], true
	}
	return items, false
}

const pageTokenVersion = 2

// tombstoneCursorKey tracks deletions, which have no entity type of their own.
const tombstoneCursorKey = "_tombstones"

var cursorKeys = map[string]bool{
	EntityNoteType: true, EntityNote: true, EntityCard: true, EntityDeck: true,
	EntityDeckConfig: true, EntityFilteredDeck: true, EntityTag: true, tombstoneCursorKey: true,
}

// validCursors drops unknown keys and rejects values the keyset SQL can't take, so a forged token is a 400, not a 500.
func validCursors(c Cursors) (Cursors, error) {
	out := make(Cursors, len(c))
	for k, cur := range c {
		if !cursorKeys[k] {
			continue
		}
		if cur.USN < 0 {
			return nil, fmt.Errorf("cursor %s: negative usn", k)
		}
		if cur.ID != "" {
			if _, err := uuid.Parse(cur.ID); err != nil {
				return nil, fmt.Errorf("cursor %s: %w", k, err)
			}
		}
		out[k] = cur
	}
	return out, nil
}

type pageToken struct {
	V int     `json:"v"`
	C Cursors `json:"c"`
	L int64   `json:"l,omitempty"` // latest_usn pinned for the whole pass
}

func encodePageToken(c Cursors, latest int64) (string, error) {
	b, err := json.Marshal(pageToken{V: pageTokenVersion, C: c, L: latest})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodePageToken returns the cursors and the pinned latest_usn (0 when the token carries none).
func decodePageToken(token string) (Cursors, int64, error) {
	if token == "" {
		return Cursors{}, 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, 0, err
	}
	var pt pageToken
	if err := json.Unmarshal(b, &pt); err != nil {
		return nil, 0, err
	}
	if pt.V != pageTokenVersion {
		return nil, 0, fmt.Errorf("unsupported page_token version %d", pt.V)
	}
	if pt.L < 0 {
		return nil, 0, errors.New("negative latest_usn")
	}
	c, err := validCursors(pt.C)
	return c, pt.L, err
}

// ─── Realtime notifier pub/sub ──────────────────────────────────────────────
// Nudges carry no usn: the client re-pulls, so a coalesced or stale nudge is harmless.

// Subscribe's channel gets coalesced nudges from any replica and closes on unsubscribe (idempotent)
// or hub shutdown.
func (s *Service) Subscribe(userID string) (<-chan struct{}, func(), error) {
	return s.hub.subscribe(userID)
}

// Notify publishes a nudge for userID to every replica; no subscribers is not an error.
func (s *Service) Notify(ctx context.Context, userID string) error {
	if err := s.rdb.Publish(ctx, notifyChannel(userID), "1").Err(); err != nil {
		return fmt.Errorf("sync: notify %s: %w", userID, err)
	}
	return nil
}
