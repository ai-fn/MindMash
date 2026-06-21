package sync

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeRepository is an in-memory Repository for unit-testing Service without
// a real Postgres connection. It emulates "WHERE usn > since ORDER BY usn ASC
// LIMIT limit" over fixed, pre-sorted fixture slices.
type fakeRepository struct {
	latestUSN     int64
	noteTypes     []NoteType
	notes         []Note
	cards         []Card
	decks         []Deck
	deckConfigs   []DeckConfig
	filteredDecks []FilteredDeck
	tags          []Tag
	tombstones    []Tombstone
}

func (f *fakeRepository) LatestUSN(context.Context, string) (int64, error) { return f.latestUSN, nil }

func sinceLimit[T any](items []T, since int64, limit int, usnOf func(T) int64) []T {
	var out []T
	for _, it := range items {
		if usnOf(it) > since {
			out = append(out, it)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (f *fakeRepository) ListNoteTypesSince(_ context.Context, _ string, since int64, limit int) ([]NoteType, error) {
	return sinceLimit(f.noteTypes, since, limit, func(nt NoteType) int64 { return nt.USN }), nil
}
func (f *fakeRepository) ListNotesSince(_ context.Context, _ string, since int64, limit int) ([]Note, error) {
	return sinceLimit(f.notes, since, limit, func(n Note) int64 { return n.USN }), nil
}
func (f *fakeRepository) ListCardsSince(_ context.Context, _ string, since int64, limit int) ([]Card, error) {
	return sinceLimit(f.cards, since, limit, func(c Card) int64 { return c.USN }), nil
}
func (f *fakeRepository) ListDecksSince(_ context.Context, _ string, since int64, limit int) ([]Deck, error) {
	return sinceLimit(f.decks, since, limit, func(d Deck) int64 { return d.USN }), nil
}
func (f *fakeRepository) ListDeckConfigsSince(_ context.Context, _ string, since int64, limit int) ([]DeckConfig, error) {
	return sinceLimit(f.deckConfigs, since, limit, func(dc DeckConfig) int64 { return dc.USN }), nil
}
func (f *fakeRepository) ListFilteredDecksSince(_ context.Context, _ string, since int64, limit int) ([]FilteredDeck, error) {
	return sinceLimit(f.filteredDecks, since, limit, func(fd FilteredDeck) int64 { return fd.USN }), nil
}
func (f *fakeRepository) ListTagsSince(_ context.Context, _ string, since int64, limit int) ([]Tag, error) {
	return sinceLimit(f.tags, since, limit, func(t Tag) int64 { return t.USN }), nil
}
func (f *fakeRepository) ListTombstonesSince(_ context.Context, _ string, since int64, limit int) ([]Tombstone, error) {
	return sinceLimit(f.tombstones, since, limit, func(t Tombstone) int64 { return t.USN }), nil
}

// GetAppliedOp/RecordAppliedOp are not exercised by Service's existing
// (delta-pull) unit tests; they're stubbed here only so fakeRepository keeps
// satisfying Repository. The apply (push) path tests land in a later task.
func (f *fakeRepository) GetAppliedOp(context.Context, string, string) (*AppliedOp, error) {
	return nil, nil
}
func (f *fakeRepository) RecordAppliedOp(context.Context, string, string, ApplyOutcome, int64) error {
	return nil
}

func newTestService(repo *fakeRepository, pageSize int) *Service {
	svc := NewService(repo, nil, nil, zap.NewNop())
	svc.pageSize = pageSize
	return svc
}

// ─── USN delta selection ──────────────────────────────────────────────────────

func TestGetChanges_ReturnsOnlyRowsAboveSinceUSN(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 5,
		notes: []Note{
			{ID: "n1", USN: 1},
			{ID: "n2", USN: 3},
			{ID: "n3", USN: 5},
		},
	}
	svc := newTestService(repo, 500)

	cs, latest, next, err := svc.GetChanges(context.Background(), "u1", 3, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if len(cs.Notes) != 1 || cs.Notes[0].ID != "n3" {
		t.Fatalf("want only n3 (usn=5 > since=3), got %+v", cs.Notes)
	}
	if latest != 5 {
		t.Fatalf("want latest_usn=5, got %d", latest)
	}
	if next != "" {
		t.Fatalf("want no next_page_token (under page size), got %q", next)
	}
}

// ─── Pagination boundary: since_usn == latest → empty result ────────────────

func TestGetChanges_SinceLatestUSN_ReturnsEmpty(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 5,
		notes:     []Note{{ID: "n1", USN: 5}},
	}
	svc := newTestService(repo, 500)

	cs, latest, next, err := svc.GetChanges(context.Background(), "u1", 5, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if len(cs.Notes) != 0 {
		t.Fatalf("want no notes when since_usn == latest, got %+v", cs.Notes)
	}
	if latest != 5 {
		t.Fatalf("want latest_usn=5, got %d", latest)
	}
	if next != "" {
		t.Fatalf("want no next_page_token, got %q", next)
	}
}

// ─── Tombstone inclusion ──────────────────────────────────────────────────────

func TestGetChanges_IncludesTombstones(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 2,
		tombstones: []Tombstone{
			{Type: EntityCard, ID: "c1", USN: 2, DeletedAt: time.Now()},
		},
	}
	svc := newTestService(repo, 500)

	cs, _, _, err := svc.GetChanges(context.Background(), "u1", 0, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if len(cs.Deleted) != 1 || cs.Deleted[0].ID != "c1" {
		t.Fatalf("want tombstone c1 in deleted, got %+v", cs.Deleted)
	}
}

// ─── Pagination across multiple pages ────────────────────────────────────────

func TestGetChanges_PaginatesWhenOverPageSize(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 3,
		notes: []Note{
			{ID: "n1", USN: 1},
			{ID: "n2", USN: 2},
			{ID: "n3", USN: 3},
		},
	}
	svc := newTestService(repo, 1) // force pagination after 1 row

	// Page 1.
	cs1, _, next1, err := svc.GetChanges(context.Background(), "u1", 0, "")
	if err != nil {
		t.Fatalf("GetChanges page 1: %v", err)
	}
	if len(cs1.Notes) != 1 || cs1.Notes[0].ID != "n1" {
		t.Fatalf("want page 1 = [n1], got %+v", cs1.Notes)
	}
	if next1 == "" {
		t.Fatal("want non-empty next_page_token after page 1 (more rows remain)")
	}

	// Page 2.
	cs2, _, next2, err := svc.GetChanges(context.Background(), "u1", 0, next1)
	if err != nil {
		t.Fatalf("GetChanges page 2: %v", err)
	}
	if len(cs2.Notes) != 1 || cs2.Notes[0].ID != "n2" {
		t.Fatalf("want page 2 = [n2], got %+v", cs2.Notes)
	}
	if next2 == "" {
		t.Fatal("want non-empty next_page_token after page 2 (one row remains)")
	}

	// Page 3 — last page, no more rows after it.
	cs3, _, next3, err := svc.GetChanges(context.Background(), "u1", 0, next2)
	if err != nil {
		t.Fatalf("GetChanges page 3: %v", err)
	}
	if len(cs3.Notes) != 1 || cs3.Notes[0].ID != "n3" {
		t.Fatalf("want page 3 = [n3], got %+v", cs3.Notes)
	}
	if next3 != "" {
		t.Fatalf("want empty next_page_token after exhausting all rows, got %q", next3)
	}
}

func TestGetChanges_InvalidPageToken_ReturnsInvalidArgument(t *testing.T) {
	svc := newTestService(&fakeRepository{}, 500)
	_, _, _, err := svc.GetChanges(context.Background(), "u1", 0, "not-valid-base64!!")
	if err == nil {
		t.Fatal("want error for invalid page_token")
	}
}

// ─── Page-token round trip ────────────────────────────────────────────────────

func TestPageTokenRoundTrip(t *testing.T) {
	want := Cursors{EntityNote: 7, EntityCard: 12}
	token, err := encodePageToken(want)
	if err != nil {
		t.Fatalf("encodePageToken: %v", err)
	}
	got, err := decodePageToken(token)
	if err != nil {
		t.Fatalf("decodePageToken: %v", err)
	}
	if len(got) != len(want) || got[EntityNote] != 7 || got[EntityCard] != 12 {
		t.Fatalf("round trip mismatch: want %+v, got %+v", want, got)
	}
}

// Subscribe/Notify are Redis-backed (see internal/sync's package doc on why
// an in-process map doesn't work across replicas) and so need a real Redis;
// they're exercised in integration_test.go instead of here.
