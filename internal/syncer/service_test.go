package syncer

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
)

// fakeRepository is an in-memory Repository for unit-testing Service without
// a real Postgres connection. It emulates the keyset "WHERE (usn, id) > cursor
// ORDER BY usn, id LIMIT limit" over fixture slices pre-sorted by (usn, id).
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

	// appliedOps/recordedOps back GetAppliedOp/RecordAppliedOp for the Apply
	// (push) path tests below; nil/empty by default so the existing
	// delta-pull tests above are unaffected.
	appliedOps  map[string]*AppliedOp
	recordedOps []recordedOp

	listCalls atomic.Int32
}

func (f *fakeRepository) LatestUSN(context.Context, string) (int64, error) { return f.latestUSN, nil }

func sinceLimit[T interface{ cursor() Cursor }](items []T, after Cursor, upTo int64, limit int) []T {
	var out []T
	for _, it := range items {
		c := it.cursor()
		if c.USN > upTo {
			continue
		}
		sameUSNAfter := after.ID != "" && c.USN == after.USN &&
			(c.Type > after.Type || (c.Type == after.Type && c.ID > after.ID))
		if c.USN > after.USN || sameUSNAfter {
			out = append(out, it)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (f *fakeRepository) ListNoteTypesSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]NoteType, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.noteTypes, after, upTo, limit), nil
}
func (f *fakeRepository) ListNotesSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]Note, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.notes, after, upTo, limit), nil
}
func (f *fakeRepository) ListCardsSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]Card, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.cards, after, upTo, limit), nil
}
func (f *fakeRepository) ListDecksSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]Deck, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.decks, after, upTo, limit), nil
}
func (f *fakeRepository) ListDeckConfigsSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]DeckConfig, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.deckConfigs, after, upTo, limit), nil
}
func (f *fakeRepository) ListFilteredDecksSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]FilteredDeck, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.filteredDecks, after, upTo, limit), nil
}
func (f *fakeRepository) ListTagsSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]Tag, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.tags, after, upTo, limit), nil
}
func (f *fakeRepository) ListTombstonesSince(_ context.Context, _ string, after Cursor, upTo int64, limit int) ([]Tombstone, error) {
	f.listCalls.Add(1)
	return sinceLimit(f.tombstones, after, upTo, limit), nil
}

// GetAppliedOp/RecordAppliedOp are not exercised by Service's existing
// (delta-pull) unit tests; the zero-value behavior below (no prior op,
// recording is a no-op) is exactly what those tests need. The apply
// (push) path tests below set appliedOps/recordedOps directly to exercise
// idempotency.
//
// appliedOps lets a test pre-seed a prior outcome for a given op_id (case
// 6, idempotent replay); recordedOps captures every RecordAppliedOp call a
// test makes, so assertions can check whether (and what) got persisted.
func (f *fakeRepository) GetAppliedOp(_ context.Context, _ string, opID string) (*AppliedOp, error) {
	if f.appliedOps == nil {
		return nil, nil
	}
	if op, ok := f.appliedOps[opID]; ok {
		return op, nil
	}
	return nil, nil
}
func (f *fakeRepository) RecordAppliedOp(_ context.Context, _ string, opID string, outcome ApplyOutcome, newUSN int64) error {
	f.recordedOps = append(f.recordedOps, recordedOp{OpID: opID, Outcome: outcome, NewUSN: newUSN})
	return nil
}

// recordedOp captures one RecordAppliedOp call.
type recordedOp struct {
	OpID    string
	Outcome ApplyOutcome
	NewUSN  int64
}

// Delta-pull fixtures need real UUIDs: page-token cursors validate them.
const (
	noteID1 = "00000000-0000-0000-0000-0000000000a1"
	noteID2 = "00000000-0000-0000-0000-0000000000a2"
	noteID3 = "00000000-0000-0000-0000-0000000000a3"
)

func newTestService(repo *fakeRepository, pageSize int) *Service {
	svc := NewService(repo, nil, nil, zap.NewNop())
	svc.pageSize = pageSize
	return svc
}

// ─── Apply (push) path test fixtures ────────────────────────────────────────
//
// fakeCollectionClient extends this file's existing fake style
// (struct-of-canned-fields, as fakeRepository uses) with one
// function-field per CollectionClient method, e.g. `getNote func(...) (...,
// error)`. A plain canned-response-per-method field (mirroring
// fakeRepository's fixed slices exactly) doesn't generalize to cases 7/8,
// which need the *same* method (UpdateNote) to behave differently across
// the two mutations in one batch (mutation 1 aborts, mutation 2 succeeds) —
// a single canned value per method can't express that, but a function field
// closing over a counter/queue can, while keeping the same "set up the
// fields you need, leave the rest nil" ergonomics fakeRepository already
// has. Each fake method also appends to `calls` so tests can assert exactly
// which RPCs fired (most importantly: zero calls for a replay, and that a
// conflict-losing path never calls Update/Delete at all).
type fakeCollectionClient struct {
	calls []string // e.g. "GetNote", "UpdateNote", in call order

	getNoteType    func(ctx context.Context, req *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error)
	updateNoteType func(ctx context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error)
	deleteNoteType func(ctx context.Context, req *collectionv1.DeleteNoteTypeRequest) (*collectionv1.DeleteNoteTypeResponse, error)

	getNote    func(ctx context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error)
	updateNote func(ctx context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error)
	deleteNote func(ctx context.Context, req *collectionv1.DeleteNoteRequest) (*collectionv1.DeleteNoteResponse, error)

	getDeck    func(ctx context.Context, req *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error)
	updateDeck func(ctx context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error)
	deleteDeck func(ctx context.Context, req *collectionv1.DeleteDeckRequest) (*collectionv1.DeleteDeckResponse, error)

	getDeckConfig    func(ctx context.Context, req *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error)
	updateDeckConfig func(ctx context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error)
	deleteDeckConfig func(ctx context.Context, req *collectionv1.DeleteDeckConfigRequest) (*collectionv1.DeleteDeckConfigResponse, error)

	deleteFilteredDeck func(ctx context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error)
}

func unsetMethod(name string) error {
	return status.Error(codes.Internal, "fakeCollectionClient: "+name+" called but not configured in this test")
}

func (f *fakeCollectionClient) GetNoteType(ctx context.Context, req *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
	f.calls = append(f.calls, "GetNoteType")
	if f.getNoteType == nil {
		return nil, unsetMethod("GetNoteType")
	}
	return f.getNoteType(ctx, req)
}
func (f *fakeCollectionClient) UpdateNoteType(ctx context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error) {
	f.calls = append(f.calls, "UpdateNoteType")
	if f.updateNoteType == nil {
		return nil, unsetMethod("UpdateNoteType")
	}
	return f.updateNoteType(ctx, req)
}
func (f *fakeCollectionClient) DeleteNoteType(ctx context.Context, req *collectionv1.DeleteNoteTypeRequest) (*collectionv1.DeleteNoteTypeResponse, error) {
	f.calls = append(f.calls, "DeleteNoteType")
	if f.deleteNoteType == nil {
		return nil, unsetMethod("DeleteNoteType")
	}
	return f.deleteNoteType(ctx, req)
}

func (f *fakeCollectionClient) GetNote(ctx context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
	f.calls = append(f.calls, "GetNote")
	if f.getNote == nil {
		return nil, unsetMethod("GetNote")
	}
	return f.getNote(ctx, req)
}
func (f *fakeCollectionClient) UpdateNote(ctx context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
	f.calls = append(f.calls, "UpdateNote")
	if f.updateNote == nil {
		return nil, unsetMethod("UpdateNote")
	}
	return f.updateNote(ctx, req)
}
func (f *fakeCollectionClient) DeleteNote(ctx context.Context, req *collectionv1.DeleteNoteRequest) (*collectionv1.DeleteNoteResponse, error) {
	f.calls = append(f.calls, "DeleteNote")
	if f.deleteNote == nil {
		return nil, unsetMethod("DeleteNote")
	}
	return f.deleteNote(ctx, req)
}

func (f *fakeCollectionClient) GetDeck(ctx context.Context, req *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error) {
	f.calls = append(f.calls, "GetDeck")
	if f.getDeck == nil {
		return nil, unsetMethod("GetDeck")
	}
	return f.getDeck(ctx, req)
}
func (f *fakeCollectionClient) UpdateDeck(ctx context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error) {
	f.calls = append(f.calls, "UpdateDeck")
	if f.updateDeck == nil {
		return nil, unsetMethod("UpdateDeck")
	}
	return f.updateDeck(ctx, req)
}
func (f *fakeCollectionClient) DeleteDeck(ctx context.Context, req *collectionv1.DeleteDeckRequest) (*collectionv1.DeleteDeckResponse, error) {
	f.calls = append(f.calls, "DeleteDeck")
	if f.deleteDeck == nil {
		return nil, unsetMethod("DeleteDeck")
	}
	return f.deleteDeck(ctx, req)
}

func (f *fakeCollectionClient) GetDeckConfig(ctx context.Context, req *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error) {
	f.calls = append(f.calls, "GetDeckConfig")
	if f.getDeckConfig == nil {
		return nil, unsetMethod("GetDeckConfig")
	}
	return f.getDeckConfig(ctx, req)
}
func (f *fakeCollectionClient) UpdateDeckConfig(ctx context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error) {
	f.calls = append(f.calls, "UpdateDeckConfig")
	if f.updateDeckConfig == nil {
		return nil, unsetMethod("UpdateDeckConfig")
	}
	return f.updateDeckConfig(ctx, req)
}
func (f *fakeCollectionClient) DeleteDeckConfig(ctx context.Context, req *collectionv1.DeleteDeckConfigRequest) (*collectionv1.DeleteDeckConfigResponse, error) {
	f.calls = append(f.calls, "DeleteDeckConfig")
	if f.deleteDeckConfig == nil {
		return nil, unsetMethod("DeleteDeckConfig")
	}
	return f.deleteDeckConfig(ctx, req)
}

func (f *fakeCollectionClient) DeleteFilteredDeck(ctx context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error) {
	f.calls = append(f.calls, "DeleteFilteredDeck")
	if f.deleteFilteredDeck == nil {
		return nil, unsetMethod("DeleteFilteredDeck")
	}
	return f.deleteFilteredDeck(ctx, req)
}

// newApplyTestService builds a Service wired to repo/coll for Apply (push
// path) tests; unlike newTestService it leaves pageSize at the default since
// none of the Apply tests exercise GetChanges pagination.
func newApplyTestService(repo *fakeRepository, coll *fakeCollectionClient) *Service {
	return NewService(repo, coll, nil, zap.NewNop())
}

// ─── USN delta selection ──────────────────────────────────────────────────────

func TestGetChanges_ReturnsOnlyRowsAboveSinceUSN(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 5,
		notes: []Note{
			{ID: noteID1, USN: 1},
			{ID: noteID2, USN: 3},
			{ID: noteID3, USN: 5},
		},
	}
	svc := newTestService(repo, 500)

	cs, latest, next, err := svc.GetChanges(context.Background(), "u1", 3, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if len(cs.Notes) != 1 || cs.Notes[0].ID != noteID3 {
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
		notes:     []Note{{ID: noteID1, USN: 5}},
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
			{ID: noteID1, USN: 1},
			{ID: noteID2, USN: 2},
			{ID: noteID3, USN: 3},
		},
	}
	svc := newTestService(repo, 1) // force pagination after 1 row

	// Page 1.
	cs1, _, next1, err := svc.GetChanges(context.Background(), "u1", 0, "")
	if err != nil {
		t.Fatalf("GetChanges page 1: %v", err)
	}
	if len(cs1.Notes) != 1 || cs1.Notes[0].ID != noteID1 {
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
	if len(cs2.Notes) != 1 || cs2.Notes[0].ID != noteID2 {
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
	if len(cs3.Notes) != 1 || cs3.Notes[0].ID != noteID3 {
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
	want := Cursors{
		EntityNote:         {USN: 7, ID: "11111111-1111-1111-1111-111111111111"},
		tombstoneCursorKey: {USN: 12, Type: EntityCard, ID: "22222222-2222-2222-2222-222222222222"},
	}
	token, err := encodePageToken(want, 42)
	if err != nil {
		t.Fatalf("encodePageToken: %v", err)
	}
	got, latest, err := decodePageToken(token)
	if err != nil {
		t.Fatalf("decodePageToken: %v", err)
	}
	if latest != 42 {
		t.Fatalf("want pinned latest 42, got %d", latest)
	}
	if len(got) != len(want) || got[EntityNote] != want[EntityNote] || got[tombstoneCursorKey] != want[tombstoneCursorKey] {
		t.Fatalf("round trip mismatch: want %+v, got %+v", want, got)
	}
}

func TestDecodePageToken_AcceptsV1(t *testing.T) {
	v1 := base64.RawURLEncoding.EncodeToString([]byte(`{"note":7,"card":12}`))
	got, _, err := decodePageToken(v1)
	if err != nil {
		t.Fatalf("decodePageToken(v1): %v", err)
	}
	if got[EntityNote] != (Cursor{USN: 7}) || got[EntityCard] != (Cursor{USN: 12}) {
		t.Fatalf("want v1 usns resumed after their whole usn, got %+v", got)
	}
}

func TestDecodePageToken_RejectsForgedCursors(t *testing.T) {
	for name, raw := range map[string]string{
		"non-uuid id":  `{"v":2,"c":{"note":{"u":1,"i":"zzz"}}}`,
		"negative usn": `{"v":2,"c":{"card":{"u":-1}}}`,
		"unknown v":    `{"v":3,"c":{}}`,
		"negative l":   `{"v":2,"c":{},"l":-1}`,
	} {
		if _, _, err := decodePageToken(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Errorf("%s: want error", name)
		}
	}

	got, _, err := decodePageToken(base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"c":{"bogus":{"u":1},"note":{"u":2}}}`)))
	if err != nil {
		t.Fatalf("decodePageToken: %v", err)
	}
	if _, ok := got["bogus"]; ok || got[EntityNote] != (Cursor{USN: 2}) {
		t.Fatalf("want unknown keys dropped and known kept, got %+v", got)
	}
}

// ─── Snapshot ceiling: usn <= latest ─────────────────────────────────────────

func TestGetChanges_ExcludesRowsAboveLatestUSN(t *testing.T) {
	// usn 4 committed after LatestUSN was read: it belongs to the next pass, not this one.
	repo := &fakeRepository{
		latestUSN: 3,
		notes:     []Note{{ID: noteID1, USN: 3}, {ID: noteID2, USN: 4}},
	}
	svc := newTestService(repo, 500)

	cs, latest, _, err := svc.GetChanges(context.Background(), "u1", 0, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if latest != 3 || len(cs.Notes) != 1 || cs.Notes[0].ID != noteID1 {
		t.Fatalf("want only rows with usn <= latest (3), got latest=%d notes=%+v", latest, cs.Notes)
	}
}

func TestGetChanges_PinsLatestAcrossPages(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 2,
		notes:     []Note{{ID: noteID1, USN: 1}, {ID: noteID2, USN: 2}},
	}
	svc := newTestService(repo, 1)

	_, latest1, next, err := svc.GetChanges(context.Background(), "u1", 0, "")
	if err != nil || next == "" {
		t.Fatalf("page 1: next=%q err=%v", next, err)
	}
	// A write lands between pages.
	repo.latestUSN = 3
	repo.notes = append(repo.notes, Note{ID: noteID3, USN: 3})

	cs2, latest2, next2, err := svc.GetChanges(context.Background(), "u1", 0, next)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if latest1 != 2 || latest2 != 2 {
		t.Fatalf("want latest pinned at 2 for the whole pass, got %d then %d", latest1, latest2)
	}
	if len(cs2.Notes) != 1 || cs2.Notes[0].ID != noteID2 || next2 != "" {
		t.Fatalf("want page 2 = [n2] and done, got %+v next=%q", cs2.Notes, next2)
	}

	cs3, latest3, _, err := svc.GetChanges(context.Background(), "u1", latest2, "")
	if err != nil || latest3 != 3 || len(cs3.Notes) != 1 || cs3.Notes[0].ID != noteID3 {
		t.Fatalf("want the next pass to pick up n3, got latest=%d notes=%+v err=%v", latest3, cs3.Notes, err)
	}
}

func TestGetChanges_CapsPinnedLatestAtRealCounter(t *testing.T) {
	repo := &fakeRepository{latestUSN: 2, notes: []Note{{ID: noteID1, USN: 1}, {ID: noteID2, USN: 2}}}
	svc := newTestService(repo, 500)
	forged, err := encodePageToken(Cursors{}, 99)
	if err != nil {
		t.Fatalf("encodePageToken: %v", err)
	}

	_, latest, _, err := svc.GetChanges(context.Background(), "u1", 0, forged)
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if latest != 2 {
		t.Fatalf("want latest capped at the real counter 2, got %d", latest)
	}
}

func TestGetChanges_CaughtUpSkipsEntityReads(t *testing.T) {
	repo := &fakeRepository{latestUSN: 5}
	svc := newTestService(repo, 500)

	if _, _, _, err := svc.GetChanges(context.Background(), "u1", 5, ""); err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if repo.listCalls.Load() != 0 {
		t.Fatalf("want no entity reads when since_usn == latest, got %d", repo.listCalls.Load())
	}
}

// ─── Pagination inside one usn ───────────────────────────────────────────────

func TestGetChanges_PaginatesRowsSharingOneUSN(t *testing.T) {
	repo := &fakeRepository{
		latestUSN: 5,
		notes:     []Note{{ID: noteID1, USN: 5}, {ID: noteID2, USN: 5}, {ID: noteID3, USN: 5}},
	}
	svc := newTestService(repo, 1)

	var got []string
	token := ""
	for page := 0; page < 5; page++ {
		cs, _, next, err := svc.GetChanges(context.Background(), "u1", 0, token)
		if err != nil {
			t.Fatalf("GetChanges page %d: %v", page, err)
		}
		for _, n := range cs.Notes {
			got = append(got, n.ID)
		}
		if next == "" {
			break
		}
		token = next
	}
	if strings.Join(got, ",") != strings.Join([]string{noteID1, noteID2, noteID3}, ",") {
		t.Fatalf("want every note sharing usn 5 exactly once, got %v", got)
	}
}

// ─── Apply (push) path ───────────────────────────────────────────────────────
//
// These tests assert Global Constraints 5 (independent per-mutation apply,
// no batch abort), 6 (Update/Delete always pins expected_usn to the freshly
// re-read server usn, never the client's stale base_usn), 7 (codes.Aborted
// -> OutcomeFailed, never recorded in sync_applied_ops), 8 (deletes never
// LWW-merge; stale-base_usn delete against an updated entity is always
// OutcomeServerWon; deleting an already-deleted entity is idempotent
// OutcomeApplied) and 9 (a replay never serves a cached payload; it
// re-fetches live from Collection) from plan.md's Global Constraints.

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// Case 1: fast-forward (server.usn == base_usn) -> OutcomeApplied,
// UpdateNote called with ExpectedUsn = serverUSN.
func TestApply_NoteUpdate_FastForward_AppliesWithFreshExpectedUsn(t *testing.T) {
	const serverUSN = int64(10)
	server := &collectionv1.Note{
		Id: "n1", Fields: map[string]string{"Front": "old"}, Usn: serverUSN,
		Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z")),
	}

	var updateReq *collectionv1.UpdateNoteRequest
	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return &collectionv1.GetNoteResponse{Note: server}, nil
		},
		updateNote: func(_ context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
			updateReq = req
			updated := &collectionv1.Note{Id: "n1", Fields: req.GetFields(), Usn: serverUSN + 1}
			return &collectionv1.UpdateNoteResponse{Note: updated}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: serverUSN,
		ClientMtime: mustTime(t, "2024-01-02T00:00:00Z"),
		Note:        &NotePayload{Fields: map[string]string{"Front": "new"}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
	if updateReq == nil {
		t.Fatal("want UpdateNote to be called")
	}
	if updateReq.GetExpectedUsn() != serverUSN {
		t.Fatalf("want ExpectedUsn=%d (freshly re-read server usn), got %d", serverUSN, updateReq.GetExpectedUsn())
	}
}

// Case 2: conflict, client newer wins -> OutcomeMerged, client's payload
// reaches the UpdateNote call.
func TestApply_NoteUpdate_ConflictClientNewer_MergesWithClientPayload(t *testing.T) {
	const baseUSN, serverUSN = int64(5), int64(10)
	server := &collectionv1.Note{
		Id: "n1", Fields: map[string]string{"Front": "server-side"}, Usn: serverUSN,
		Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z")), // older than client mtime below
	}

	var updateReq *collectionv1.UpdateNoteRequest
	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, _ *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return &collectionv1.GetNoteResponse{Note: server}, nil
		},
		updateNote: func(_ context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
			updateReq = req
			updated := &collectionv1.Note{Id: "n1", Fields: req.GetFields(), Usn: serverUSN + 1}
			return &collectionv1.UpdateNoteResponse{Note: updated}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	clientFields := map[string]string{"Front": "client-newer"}
	m := Mutation{
		OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: baseUSN,
		ClientMtime: mustTime(t, "2024-06-01T00:00:00Z"), // After server's mtime -> client wins
		Note:        &NotePayload{Fields: clientFields},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeMerged {
		t.Fatalf("want OutcomeMerged, got %+v", results)
	}
	if updateReq == nil {
		t.Fatal("want UpdateNote to be called")
	}
	if updateReq.GetFields()["Front"] != "client-newer" {
		t.Fatalf("want client's payload to reach UpdateNote, got %+v", updateReq.GetFields())
	}
	if updateReq.GetExpectedUsn() != serverUSN {
		t.Fatalf("want ExpectedUsn pinned to fresh server usn %d even on client-wins path, got %d", serverUSN, updateReq.GetExpectedUsn())
	}
}

// Case 3: conflict, server newer wins -> OutcomeServerWon, no UpdateNote
// call made, returned entity is the server's untouched state.
func TestApply_NoteUpdate_ConflictServerNewer_ServerWonNoUpdateCall(t *testing.T) {
	const baseUSN, serverUSN = int64(5), int64(10)
	server := &collectionv1.Note{
		Id: "n1", Fields: map[string]string{"Front": "server-side"}, Usn: serverUSN,
		Mtime: timestamppb.New(mustTime(t, "2024-06-01T00:00:00Z")), // newer than client mtime below
	}

	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, _ *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return &collectionv1.GetNoteResponse{Note: server}, nil
		},
		// updateNote intentionally left nil: any call would fail via unsetMethod.
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: baseUSN,
		ClientMtime: mustTime(t, "2024-01-01T00:00:00Z"), // older than server's mtime -> server wins
		Note:        &NotePayload{Fields: map[string]string{"Front": "client-side"}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeServerWon {
		t.Fatalf("want OutcomeServerWon, got %+v", results)
	}
	if results[0].Note == nil || results[0].Note.Fields["Front"] != "server-side" {
		t.Fatalf("want returned entity to be server's untouched state, got %+v", results[0].Note)
	}
	for _, c := range coll.calls {
		if c == "UpdateNote" {
			t.Fatal("want no UpdateNote call when server wins")
		}
	}
}

// Case 4: delete with stale base_usn (entity updated since) ->
// OutcomeServerWon, no DeleteNote call.
func TestApply_NoteDelete_StaleBaseUsn_ServerWonNoDeleteCall(t *testing.T) {
	const baseUSN, serverUSN = int64(5), int64(10) // entity moved on since base_usn
	server := &collectionv1.Note{Id: "n1", Usn: serverUSN, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, _ *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return &collectionv1.GetNoteResponse{Note: server}, nil
		},
		// deleteNote intentionally left nil: any call would fail via unsetMethod.
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{OpID: "op1", Type: MutationNoteDelete, EntityID: "n1", BaseUSN: baseUSN}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeServerWon {
		t.Fatalf("want OutcomeServerWon (deletes never LWW-merge against a concurrent update), got %+v", results)
	}
	for _, c := range coll.calls {
		if c == "DeleteNote" {
			t.Fatal("want no DeleteNote call when base_usn is stale")
		}
	}
}

// Case 5: delete of an already-deleted entity (Get returns NotFound) ->
// OutcomeApplied, reason "already deleted".
func TestApply_NoteDelete_AlreadyDeleted_IdempotentApplied(t *testing.T) {
	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, _ *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return nil, status.Error(codes.NotFound, "note not found")
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{OpID: "op1", Type: MutationNoteDelete, EntityID: "n1", BaseUSN: 1}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
	if results[0].Reason != "already deleted" {
		t.Fatalf("want reason %q, got %q", "already deleted", results[0].Reason)
	}
}

// Case 6: idempotency — a replayed op_id (fake Repository.GetAppliedOp
// returns a prior record) -> OutcomeReplayed, zero CollectionClient calls.
//
// Per Global Constraint 9, sync_applied_ops never caches an entity payload,
// so replayResult still does a best-effort live Get to populate the
// response — that Get is the one CollectionClient call this test expects;
// no Update/Delete call should ever happen for a replay.
func TestApply_ReplayedOpID_NoCollectionWriteCalls(t *testing.T) {
	prior := &AppliedOp{OpID: "op1", Outcome: OutcomeApplied, NewUSN: 7, AppliedAt: time.Now()}
	repo := &fakeRepository{appliedOps: map[string]*AppliedOp{"op1": prior}}

	refreshed := &collectionv1.Note{Id: "n1", Usn: 7}
	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, _ *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return &collectionv1.GetNoteResponse{Note: refreshed}, nil
		},
	}
	svc := newApplyTestService(repo, coll)

	m := Mutation{OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: 7, Note: &NotePayload{}}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeReplayed {
		t.Fatalf("want OutcomeReplayed, got %+v", results)
	}
	for _, c := range coll.calls {
		if c == "UpdateNote" || c == "DeleteNote" {
			t.Fatalf("want zero write calls for a replay, got call %q (all calls: %v)", c, coll.calls)
		}
	}
	if len(repo.recordedOps) != 0 {
		t.Fatalf("want no new RecordAppliedOp call for a replay, got %+v", repo.recordedOps)
	}
}

// Case 7: codes.Aborted from UpdateNote/DeleteNote (simulated race) ->
// OutcomeFailed, AND the batch continues — a 2-mutation batch with
// mutation 1 aborted still applies mutation 2 (Global Constraint 5).
func TestApply_AbortedRace_FailsButBatchContinues(t *testing.T) {
	server1 := &collectionv1.Note{Id: "n1", Usn: 5, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}
	server2 := &collectionv1.Note{Id: "n2", Usn: 5, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			if req.GetId() == "n1" {
				return &collectionv1.GetNoteResponse{Note: server1}, nil
			}
			return &collectionv1.GetNoteResponse{Note: server2}, nil
		},
		updateNote: func(_ context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
			if req.GetId() == "n1" {
				return nil, status.Error(codes.Aborted, "concurrent write race")
			}
			return &collectionv1.UpdateNoteResponse{Note: &collectionv1.Note{Id: "n2", Usn: 6}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	mutations := []Mutation{
		{OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: 5, Note: &NotePayload{}},
		{OpID: "op2", Type: MutationNoteUpdate, EntityID: "n2", BaseUSN: 5, Note: &NotePayload{}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", mutations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != OutcomeFailed {
		t.Fatalf("want mutation 1 OutcomeFailed (aborted race), got %+v", results[0])
	}
	if results[1].Outcome != OutcomeApplied {
		t.Fatalf("want mutation 2 to still apply despite mutation 1's failure, got %+v", results[1])
	}

	// Global Constraint 7: an Aborted outcome must never be recorded in
	// sync_applied_ops (so a retry of op1 re-resolves against fresh state).
	for _, r := range repoRecordedOps(svc) {
		if r.OpID == "op1" {
			t.Fatalf("want op1 (aborted) never recorded in sync_applied_ops, got %+v", r)
		}
	}
}

// repoRecordedOps is a small test-only accessor so TestApply_AbortedRace_*
// can inspect what got recorded without widening Service's exported surface.
func repoRecordedOps(svc *Service) []recordedOp {
	return svc.repo.(*fakeRepository).recordedOps
}

// Case 8: partial-batch failure — mutation 1 hard-fails (NotFound on an
// update, not a delete) -> both results returned, no early return/abort of
// the loop.
func TestApply_PartialBatchFailure_BothResultsReturnedNoEarlyAbort(t *testing.T) {
	server2 := &collectionv1.Note{Id: "n2", Usn: 5, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	coll := &fakeCollectionClient{
		getNote: func(_ context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			if req.GetId() == "n1" {
				return nil, status.Error(codes.NotFound, "note not found")
			}
			return &collectionv1.GetNoteResponse{Note: server2}, nil
		},
		updateNote: func(_ context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
			return &collectionv1.UpdateNoteResponse{Note: &collectionv1.Note{Id: "n2", Usn: 6}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	mutations := []Mutation{
		{OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: 5, Note: &NotePayload{}}, // hard-fails: not found
		{OpID: "op2", Type: MutationNoteUpdate, EntityID: "n2", BaseUSN: 5, Note: &NotePayload{}}, // should still apply
	}
	results, _, err := svc.Apply(context.Background(), "u1", mutations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want both results returned (no early abort), got %d: %+v", len(results), results)
	}
	if results[0].Outcome != OutcomeFailed {
		t.Fatalf("want mutation 1 OutcomeFailed (not found), got %+v", results[0])
	}
	if results[1].Outcome != OutcomeApplied {
		t.Fatalf("want mutation 2 to still apply, got %+v", results[1])
	}
}

// Case 9: one happy-path resolver test each for note_type update, deck
// update, deck_config update, and filtered_deck delete — pinning each
// resolver's wiring to the right Collection RPC.

func TestApply_NoteTypeUpdate_HappyPath_WiredToUpdateNoteType(t *testing.T) {
	const serverUSN = int64(3)
	server := &collectionv1.NoteType{Id: "nt1", Name: "Basic", Usn: serverUSN, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	var called bool
	coll := &fakeCollectionClient{
		getNoteType: func(_ context.Context, _ *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
			return &collectionv1.GetNoteTypeResponse{NoteType: server}, nil
		},
		updateNoteType: func(_ context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error) {
			called = true
			if req.GetExpectedUsn() != serverUSN {
				t.Fatalf("want ExpectedUsn=%d, got %d", serverUSN, req.GetExpectedUsn())
			}
			return &collectionv1.UpdateNoteTypeResponse{NoteType: &collectionv1.NoteType{Id: "nt1", Name: req.GetName(), Usn: serverUSN + 1}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationNoteTypeUpdate, EntityID: "nt1", BaseUSN: serverUSN,
		NoteType: &NoteTypePayload{Name: "Basic (Edited)"},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("want UpdateNoteType to be called")
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
	if results[0].NoteType == nil || results[0].NoteType.Name != "Basic (Edited)" {
		t.Fatalf("want resolver wired to UpdateNoteType's response, got %+v", results[0].NoteType)
	}
}

func TestApply_NoteTypeUpdate_KeepsFieldLanguage(t *testing.T) {
	server := &collectionv1.NoteType{Id: "nt1", Usn: 3, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}
	coll := &fakeCollectionClient{
		getNoteType: func(_ context.Context, _ *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
			return &collectionv1.GetNoteTypeResponse{NoteType: server}, nil
		},
		updateNoteType: func(_ context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error) {
			if fs := req.GetFields(); len(fs) != 1 || fs[0].GetLanguage() != "ru" {
				t.Fatalf("want one field with language ru sent to Collection, got %+v", fs)
			}
			return &collectionv1.UpdateNoteTypeResponse{NoteType: &collectionv1.NoteType{Id: "nt1", Usn: 4, Fields: req.GetFields()}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationNoteTypeUpdate, EntityID: "nt1", BaseUSN: 3,
		NoteType: &NoteTypePayload{Name: "Basic", Fields: []Field{{Name: "Front", Language: "ru"}}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if nt := results[0].NoteType; nt == nil || len(nt.Fields) != 1 || nt.Fields[0].Language != "ru" {
		t.Fatalf("want language ru in the resolved note type, got %+v", results[0].NoteType)
	}
}

func TestApply_RejectsUnkeyableBatch(t *testing.T) {
	tests := []struct {
		name      string
		mutations []Mutation
	}{
		{"too many", make([]Mutation, maxMutationsPerApply+1)},
		{"empty op_id", []Mutation{{Type: MutationNoteDelete, EntityID: "n1"}}},
		{"long op_id", []Mutation{{OpID: strings.Repeat("x", maxOpIDLen+1), Type: MutationNoteDelete, EntityID: "n1"}}},
		{"nul in op_id", []Mutation{{OpID: "op\x00", Type: MutationNoteDelete, EntityID: "n1"}}},
		{"bad op_id after a valid one", []Mutation{{OpID: "op1", Type: MutationNoteDelete, EntityID: "n1"}, {Type: MutationNoteDelete, EntityID: "n2"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, coll := &fakeRepository{}, &fakeCollectionClient{}
			_, _, err := newApplyTestService(repo, coll).Apply(context.Background(), "u1", tt.mutations)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
			if len(coll.calls) != 0 || len(repo.recordedOps) != 0 {
				t.Fatalf("want no side effects, got calls %v, recorded %v", coll.calls, repo.recordedOps)
			}
		})
	}
}

func TestApply_AcceptsLimits(t *testing.T) {
	ms := make([]Mutation, maxMutationsPerApply)
	for i := range ms {
		ms[i] = Mutation{OpID: fmt.Sprintf("%0*d", maxOpIDLen, i), Type: MutationNoteDelete, EntityID: "n1"}
	}
	coll := &fakeCollectionClient{getNote: func(context.Context, *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
		return nil, status.Error(codes.NotFound, "gone")
	}}
	results, _, err := newApplyTestService(&fakeRepository{}, coll).Apply(context.Background(), "u1", ms)
	if err != nil || len(results) != maxMutationsPerApply || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want %d mutations with %d-byte op_ids applied, got err %v", maxMutationsPerApply, maxOpIDLen, err)
	}
}

func TestApply_FailsMalformedMutationAlone(t *testing.T) {
	ms := []Mutation{
		{OpID: "nt", Type: MutationNoteTypeUpdate, EntityID: "nt1"},
		{OpID: "n", Type: MutationNoteUpdate, EntityID: "n1"},
		{OpID: "d", Type: MutationDeckUpdate, EntityID: "d1"},
		{OpID: "dc", Type: MutationDeckConfigUpdate, EntityID: "dc1"},
		{OpID: "no-entity", Type: MutationNoteDelete},
		{OpID: "ok", Type: MutationNoteDelete, EntityID: "n1"},
		{OpID: "unknown", Type: MutationType(99)},
	}
	repo := &fakeRepository{}
	coll := &fakeCollectionClient{getNote: func(context.Context, *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
		return nil, status.Error(codes.NotFound, "gone")
	}}
	results, _, err := newApplyTestService(repo, coll).Apply(context.Background(), "u1", ms)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, r := range results[:5] {
		if r.Outcome != OutcomeFailed {
			t.Fatalf("want %s to fail on its own, got %+v", r.OpID, r)
		}
	}
	if results[5].Outcome != OutcomeApplied || results[6].Outcome != OutcomeFailed {
		t.Fatalf("want the valid sibling applied and the unknown type failed, got %+v", results[5:])
	}
	if len(repo.recordedOps) != len(ms)-1 {
		t.Fatalf("want every op but the unknown type recorded, got %+v", repo.recordedOps)
	}
	if !slices.Equal(coll.calls, []string{"GetNote"}) {
		t.Fatalf("want only the valid op sent to Collection, got %v", coll.calls)
	}
}

func TestApply_ClampsFutureClientMtime(t *testing.T) {
	// Server mtime ahead of now: an unclamped 2100 mtime would win.
	server := &collectionv1.NoteType{Id: "nt1", Usn: 5, Mtime: timestamppb.New(time.Now().Add(time.Hour))}
	coll := &fakeCollectionClient{
		getNoteType: func(_ context.Context, _ *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
			return &collectionv1.GetNoteTypeResponse{NoteType: server}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationNoteTypeUpdate, EntityID: "nt1", BaseUSN: 3,
		ClientMtime: mustTime(t, "2100-01-01T00:00:00Z"), NoteType: &NoteTypePayload{Name: "Basic"},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if results[0].Outcome != OutcomeServerWon {
		t.Fatalf("want OutcomeServerWon once the future mtime is clamped, got %+v", results[0])
	}
}

func TestFieldProtoRoundTrip_KeepsLanguage(t *testing.T) {
	f := Field{Name: "Front", Sticky: true, RTL: true, Language: "ru"}
	if got := fieldFromProto(fieldToProto(f)); got != f {
		t.Fatalf("want %+v after round trip, got %+v", f, got)
	}
}

func TestApply_DeckUpdate_HappyPath_WiredToUpdateDeck(t *testing.T) {
	const serverUSN = int64(3)
	server := &collectionv1.Deck{Id: "d1", Name: "Old", Usn: serverUSN, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	var called bool
	coll := &fakeCollectionClient{
		getDeck: func(_ context.Context, _ *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error) {
			return &collectionv1.GetDeckResponse{Deck: server}, nil
		},
		updateDeck: func(_ context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error) {
			called = true
			if req.GetExpectedUsn() != serverUSN {
				t.Fatalf("want ExpectedUsn=%d, got %d", serverUSN, req.GetExpectedUsn())
			}
			return &collectionv1.UpdateDeckResponse{Deck: &collectionv1.Deck{Id: "d1", Name: req.GetName(), Usn: serverUSN + 1}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationDeckUpdate, EntityID: "d1", BaseUSN: serverUSN,
		Deck: &DeckPayload{Name: "New"},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("want UpdateDeck to be called")
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
	if results[0].Deck == nil || results[0].Deck.Name != "New" {
		t.Fatalf("want resolver wired to UpdateDeck's response, got %+v", results[0].Deck)
	}
}

func TestApply_DeckConfigUpdate_HappyPath_WiredToUpdateDeckConfig(t *testing.T) {
	const serverUSN = int64(3)
	server := &collectionv1.DeckConfig{Id: "dc1", Name: "Default", Usn: serverUSN, Mtime: timestamppb.New(mustTime(t, "2024-01-01T00:00:00Z"))}

	var called bool
	coll := &fakeCollectionClient{
		getDeckConfig: func(_ context.Context, _ *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error) {
			return &collectionv1.GetDeckConfigResponse{DeckConfig: server}, nil
		},
		updateDeckConfig: func(_ context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error) {
			called = true
			if req.GetExpectedUsn() != serverUSN {
				t.Fatalf("want ExpectedUsn=%d, got %d", serverUSN, req.GetExpectedUsn())
			}
			return &collectionv1.UpdateDeckConfigResponse{DeckConfig: &collectionv1.DeckConfig{Id: "dc1", Name: req.GetName(), Usn: serverUSN + 1}}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{
		OpID: "op1", Type: MutationDeckConfigUpdate, EntityID: "dc1", BaseUSN: serverUSN,
		DeckConfig: &DeckConfigPayload{Name: "Aggressive"},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("want UpdateDeckConfig to be called")
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
	if results[0].DeckConfig == nil || results[0].DeckConfig.Name != "Aggressive" {
		t.Fatalf("want resolver wired to UpdateDeckConfig's response, got %+v", results[0].DeckConfig)
	}
}

func TestApply_FilteredDeckDelete_HappyPath_WiredToDeleteFilteredDeck(t *testing.T) {
	var called bool
	var gotReq *collectionv1.DeleteFilteredDeckRequest
	coll := &fakeCollectionClient{
		deleteFilteredDeck: func(_ context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error) {
			called = true
			gotReq = req
			return &collectionv1.DeleteFilteredDeckResponse{}, nil
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)

	m := Mutation{OpID: "op1", Type: MutationFilteredDeckDelete, EntityID: "fd1", BaseUSN: 3}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("want DeleteFilteredDeck to be called")
	}
	if gotReq.GetId() != "fd1" {
		t.Fatalf("want resolver wired to DeleteFilteredDeck with the mutation's entity id, got %+v", gotReq)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %+v", results)
	}
}

// Subscribe/Notify are Redis-backed (see internal/syncer's package doc on why
// an in-process map doesn't work across replicas) and so need a real Redis;
// they're exercised in integration_test.go instead of here.

func TestApply_FailureReasonHidesDownstreamDetail(t *testing.T) {
	coll := &fakeCollectionClient{
		getNote: func(context.Context, *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return nil, status.Error(codes.Unavailable, "connection error: dial tcp 10.0.3.7:9090: connect: connection refused")
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)
	m := Mutation{
		OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: 1,
		ClientMtime: mustTime(t, "2024-01-02T00:00:00Z"),
		Note:        &NotePayload{Fields: map[string]string{"Front": "new"}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeFailed || results[0].Reason != "service unavailable" {
		t.Fatalf("want a failed result with only the public reason, got %+v", results)
	}
}

func TestApply_FailureReasonKeepsCollectionsVerdictOnTheMutation(t *testing.T) {
	coll := &fakeCollectionClient{
		getNote: func(context.Context, *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
			return nil, fmt.Errorf("sync: get note: %w", status.Error(codes.FailedPrecondition, "note type has notes"))
		},
	}
	svc := newApplyTestService(&fakeRepository{}, coll)
	m := Mutation{
		OpID: "op1", Type: MutationNoteUpdate, EntityID: "n1", BaseUSN: 1,
		ClientMtime: mustTime(t, "2024-01-02T00:00:00Z"),
		Note:        &NotePayload{Fields: map[string]string{"Front": "new"}},
	}
	results, _, err := svc.Apply(context.Background(), "u1", []Mutation{m})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Reason != "note type has notes" {
		t.Fatalf("want collection's own message on the user's mutation, got %+v", results)
	}
}
