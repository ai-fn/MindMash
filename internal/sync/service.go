package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// defaultPageSize bounds how many rows of *each* entity type are returned per
// GetChanges call. Kept small relative to a typical collection so a single
// page comfortably fits in one gRPC/HTTP response.
const defaultPageSize = 500

// Service is the Sync domain service: read-only USN delta assembly plus a
// Redis-backed pub/sub used by the SSE notifier to nudge open tabs. Redis
// (not an in-process map) is required because Sync runs as a multi-replica
// Deployment (see docs/detailed/14-kubernetes-deployment.md): a Kafka event
// consumed by one replica must still reach SSE clients connected to any
// other replica, which only a shared broker — not process-local memory — can
// guarantee.
type Service struct {
	repo     Repository
	rdb      *redis.Client
	log      *zap.Logger
	pageSize int // per-entity-type page size; tests shrink this to exercise pagination cheaply
}

// NewService constructs a Service.
func NewService(repo Repository, rdb *redis.Client, log *zap.Logger) *Service {
	return &Service{repo: repo, rdb: rdb, log: log, pageSize: defaultPageSize}
}

// GetChanges returns one page of the delta feed for userID: every entity with
// usn > sinceUSN (on the first page) or beyond the per-type cursor encoded in
// pageToken (on subsequent pages), plus the user's latest known usn.
func (s *Service) GetChanges(ctx context.Context, userID string, sinceUSN int64, pageToken string) (ChangeSet, int64, string, error) {
	cursors, err := decodePageToken(pageToken)
	if err != nil {
		return ChangeSet{}, 0, "", platformerrors.InvalidArgument("invalid page_token", err)
	}

	cursorFor := func(entityType string) int64 {
		if c, ok := cursors[entityType]; ok {
			return c
		}
		return sinceUSN
	}

	fetchLimit := s.pageSize + 1 // fetch one extra to detect "more rows exist"

	// Tombstones share no entity-type cursor of their own (deletions aren't
	// re-fetched per-type above); track them under a dedicated key.
	const tombstoneCursorKey = "_tombstones"

	// The 9 reads below are independent — different tables, same userID — so
	// run them concurrently rather than paying their latency 9 times over on
	// a path the client calls on every page load, focus regain, post-write,
	// and heartbeat.
	var (
		noteTypes     []NoteType
		notes         []Note
		cards         []Card
		decks         []Deck
		deckConfigs   []DeckConfig
		filteredDecks []FilteredDeck
		tags          []Tag
		deleted       []Tombstone
		latestUSN     int64
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		noteTypes, err = s.repo.ListNoteTypesSince(gctx, userID, cursorFor(EntityNoteType), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		notes, err = s.repo.ListNotesSince(gctx, userID, cursorFor(EntityNote), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		cards, err = s.repo.ListCardsSince(gctx, userID, cursorFor(EntityCard), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		decks, err = s.repo.ListDecksSince(gctx, userID, cursorFor(EntityDeck), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		deckConfigs, err = s.repo.ListDeckConfigsSince(gctx, userID, cursorFor(EntityDeckConfig), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		filteredDecks, err = s.repo.ListFilteredDecksSince(gctx, userID, cursorFor(EntityFilteredDeck), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		tags, err = s.repo.ListTagsSince(gctx, userID, cursorFor(EntityTag), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		deleted, err = s.repo.ListTombstonesSince(gctx, userID, cursorFor(tombstoneCursorKey), fetchLimit)
		return err
	})
	g.Go(func() (err error) {
		latestUSN, err = s.repo.LatestUSN(gctx, userID)
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
		next[EntityNoteType] = noteTypes[len(noteTypes)-1].USN
	}

	notes, more = truncate(notes, s.pageSize)
	hasMore = hasMore || more
	if len(notes) > 0 {
		next[EntityNote] = notes[len(notes)-1].USN
	}

	cards, more = truncate(cards, s.pageSize)
	hasMore = hasMore || more
	if len(cards) > 0 {
		next[EntityCard] = cards[len(cards)-1].USN
	}

	decks, more = truncate(decks, s.pageSize)
	hasMore = hasMore || more
	if len(decks) > 0 {
		next[EntityDeck] = decks[len(decks)-1].USN
	}

	deckConfigs, more = truncate(deckConfigs, s.pageSize)
	hasMore = hasMore || more
	if len(deckConfigs) > 0 {
		next[EntityDeckConfig] = deckConfigs[len(deckConfigs)-1].USN
	}

	filteredDecks, more = truncate(filteredDecks, s.pageSize)
	hasMore = hasMore || more
	if len(filteredDecks) > 0 {
		next[EntityFilteredDeck] = filteredDecks[len(filteredDecks)-1].USN
	}

	tags, more = truncate(tags, s.pageSize)
	hasMore = hasMore || more
	if len(tags) > 0 {
		next[EntityTag] = tags[len(tags)-1].USN
	}

	deleted, more = truncate(deleted, s.pageSize)
	hasMore = hasMore || more
	if len(deleted) > 0 {
		next[tombstoneCursorKey] = deleted[len(deleted)-1].USN
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
		// Carry forward any cursor not touched this page (entity type already
		// exhausted in an earlier page) so it isn't re-scanned.
		for k, v := range cursors {
			if _, ok := next[k]; !ok {
				next[k] = v
			}
		}
		token, err := encodePageToken(next)
		if err != nil {
			return ChangeSet{}, 0, "", fmt.Errorf("sync: encode page token: %w", err)
		}
		nextToken = token
	}

	return cs, latestUSN, nextToken, nil
}

// usn is the common shape every entity projection in this package satisfies;
// used only to let truncate report the last row's usn generically.
type usn interface{ usnValue() int64 }

func (nt NoteType) usnValue() int64     { return nt.USN }
func (n Note) usnValue() int64          { return n.USN }
func (c Card) usnValue() int64          { return c.USN }
func (d Deck) usnValue() int64          { return d.USN }
func (dc DeckConfig) usnValue() int64   { return dc.USN }
func (fd FilteredDeck) usnValue() int64 { return fd.USN }
func (t Tag) usnValue() int64           { return t.USN }
func (t Tombstone) usnValue() int64     { return t.USN }

// truncate trims items to at most pageSize entries and reports whether more
// rows existed beyond that (i.e. items originally numbered pageSize+1).
func truncate[T usn](items []T, pageSize int) ([]T, bool) {
	if len(items) > pageSize {
		return items[:pageSize], true
	}
	return items, false
}

func encodePageToken(c Cursors) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodePageToken(token string) (Cursors, error) {
	if token == "" {
		return Cursors{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	var c Cursors
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return c, nil
}

// ─── Realtime notifier pub/sub ──────────────────────────────────────────────
// Fan-out from "this user's data changed somewhere" (signalled by Notify,
// driven by a Kafka consumer over Collection/Scheduler's outbox-relayed
// topics) to open SSE connections for that user, via Redis Pub/Sub rather
// than an in-process map: Sync runs as a multi-replica Deployment, and a
// Kafka event consumed by one replica must still reach an SSE client that
// happens to be connected to a different replica. The payload carries no
// usn: the client re-pulls GetChanges to learn what's new, so a coalesced or
// slightly stale nudge is harmless.

func notifyChannel(userID string) string { return "sync:notify:" + userID }

// Subscribe registers a new SSE connection for userID and returns a channel
// that receives a value each time any replica calls Notify(userID), plus an
// unsubscribe function the caller must invoke when the connection closes.
// unsubscribe is safe to call more than once (redis.PubSub.Close is
// idempotent — a second call returns an error, which is discarded here).
func (s *Service) Subscribe(ctx context.Context, userID string) (<-chan struct{}, func(), error) {
	ps := s.rdb.Subscribe(ctx, notifyChannel(userID))
	// Confirm the subscription actually reached Redis before returning, so a
	// down/unreachable Redis surfaces as an error here rather than silently
	// producing a channel that never fires.
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return nil, nil, fmt.Errorf("sync: subscribe %s: %w", userID, err)
	}

	out := make(chan struct{}, 1)
	go func() {
		for range ps.Channel() {
			select {
			case out <- struct{}{}:
			default:
			}
		}
		close(out)
	}()

	unsubscribe := func() { _ = ps.Close() }
	return out, unsubscribe, nil
}

// Notify publishes a nudge for userID to every replica's subscribers via
// Redis. A publish with no subscribers (e.g. the user has no open tabs right
// now) is a normal no-op, not an error.
func (s *Service) Notify(ctx context.Context, userID string) error {
	if err := s.rdb.Publish(ctx, notifyChannel(userID), "1").Err(); err != nil {
		return fmt.Errorf("sync: notify %s: %w", userID, err)
	}
	return nil
}
