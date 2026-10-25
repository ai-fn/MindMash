//go:build integration

package collection

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
)

type integrationComponents struct {
	dsn  string
	pool *pgxpool.Pool
	repo *PostgresRepository
	svc  *Service
}

func setupIntegration(t *testing.T) integrationComponents {
	t.Helper()
	return setupIntegrationOn(t, "postgres:18-alpine", false)
}

func setupIntegrationOn(t *testing.T, image string, citus bool) integrationComponents {
	t.Helper()
	ctx := context.Background()

	// The postgres image restarts its server process after initdb; without an
	// explicit occurrence count the default wait strategy can report ready
	// after the first (pre-restart) boot, so the first connection attempt
	// hits a server that's about to restart ("connection reset by peer").
	pgCtr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("collection_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgCtr.Terminate(ctx) })

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pg connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	if citus {
		if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS citus`); err != nil {
			t.Fatalf("create citus extension: %v", err)
		}
	}
	if err := platformdb.Migrate(ctx, dsn, "../../migrations", "platform", "collection"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	repo := NewPostgresRepository(pool)
	svc := NewService(repo, zap.NewNop())
	return integrationComponents{dsn: dsn, pool: pool, repo: repo, svc: svc}
}

const (
	testUserID    = "00000000-0000-0000-0000-000000000001"
	testProfileID = "00000000-0000-0000-0000-000000000002"
)

func basicNoteType(t *testing.T, c integrationComponents) *NoteType {
	t.Helper()
	nt, err := c.svc.CreateNoteType(context.Background(), testUserID, testProfileID, "Basic", "",
		[]Field{{Name: "Front"}, {Name: "Back"}},
		[]CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	return nt
}

func defaultDeck(t *testing.T, c integrationComponents) *Deck {
	t.Helper()
	d, err := c.svc.CreateDeck(context.Background(), testUserID, testProfileID, DeckFields{Name: "Default"})
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	return d
}

// ─── NoteType CRUD ───────────────────────────────────────────────────────────

func TestNoteType_CRUD(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := basicNoteType(t, c)
	if nt.USN != 1 {
		t.Fatalf("want USN=1, got %d", nt.USN)
	}

	got, err := c.svc.GetNoteType(ctx, testUserID, nt.ID)
	if err != nil {
		t.Fatalf("GetNoteType: %v", err)
	}
	if got.Name != "Basic" {
		t.Fatalf("want Basic, got %q", got.Name)
	}

	updated, err := c.svc.UpdateNoteType(ctx, testUserID, nt.ID, nt.USN, "Basic v2", "", nt.Fields, nt.CardTemplates)
	if err != nil {
		t.Fatalf("UpdateNoteType: %v", err)
	}
	if updated.Name != "Basic v2" {
		t.Fatalf("want Basic v2, got %q", updated.Name)
	}

	items, _, err := c.svc.ListNoteTypes(ctx, testUserID, Page{})
	if err != nil {
		t.Fatalf("ListNoteTypes: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1, got %d", len(items))
	}

	// Outbox should have create + update entries.
	var n int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE user_id=$1 AND aggregate_type='note_type'`, testUserID).Scan(&n); err != nil {
		t.Fatalf("outbox count: %v", err)
	}
	if n < 2 {
		t.Fatalf("want ≥2 outbox rows, got %d", n)
	}
}

// ─── CreateNote generates cards ───────────────────────────────────────────────

func TestNote_CreateGeneratesCards(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)

	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "Hello", "Back": "World"},
		[]string{"lang"}, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil {
		t.Fatalf("ListCardsByNote: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	if cards[0].State != StateNew {
		t.Fatalf("want StateNew, got %v", cards[0].State)
	}

	// Tag registry should contain "lang".
	var tagCount int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tags WHERE user_id=$1 AND name='lang'`, testUserID).Scan(&tagCount); err != nil {
		t.Fatalf("tag count: %v", err)
	}
	if tagCount != 1 {
		t.Fatalf("want 1 tag, got %d", tagCount)
	}
}

func TestRenderCard_ClozeRevealedOnBack(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt, err := c.svc.CreateNoteType(ctx, testUserID, testProfileID, "Cloze", "",
		[]Field{{Name: "Text"}},
		[]CardTemplate{{Name: "Cloze", Front: "{{cloze:Text}}", Back: "{{cloze:Text}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Text": "Capital: {{c1::Paris}}"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v, %d cards", err, len(cards))
	}

	_, front, back, err := c.svc.RenderCard(ctx, testUserID, cards[0].ID)
	if err != nil {
		t.Fatalf("RenderCard: %v", err)
	}
	if strings.Contains(front, "Paris") || !strings.Contains(front, "[...]") {
		t.Errorf("front reveals the answer: %q", front)
	}
	if !strings.Contains(back, "Paris") {
		t.Errorf("back hides the answer: %q", back)
	}
}

func TestRenderCard_ReflectsNoteAndTemplateEdits(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "old-q", "Back": "old-a"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v, %d cards", err, len(cards))
	}
	if _, _, _, err := c.svc.RenderCard(ctx, testUserID, cards[0].ID); err != nil {
		t.Fatalf("RenderCard: %v", err)
	}

	if _, err := c.svc.UpdateNote(ctx, testUserID, note.ID, note.USN,
		map[string]string{"Front": "new-q", "Back": "new-a"}, nil, 0); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	_, front, back, err := c.svc.RenderCard(ctx, testUserID, cards[0].ID)
	if err != nil {
		t.Fatalf("RenderCard: %v", err)
	}
	if front != "new-q" || back != "new-a" {
		t.Fatalf("after note edit: front=%q back=%q", front, back)
	}

	if _, err := c.svc.UpdateNoteType(ctx, testUserID, nt.ID, nt.USN, nt.Name, nt.CSS, nt.Fields,
		[]CardTemplate{{Name: "Card 1", Front: "Q: {{Front}}", Back: "A: {{Back}}"}}); err != nil {
		t.Fatalf("UpdateNoteType: %v", err)
	}
	_, front, back, err = c.svc.RenderCard(ctx, testUserID, cards[0].ID)
	if err != nil {
		t.Fatalf("RenderCard: %v", err)
	}
	if front != "Q: new-q" || back != "A: new-a" {
		t.Fatalf("after template edit: front=%q back=%q", front, back)
	}
}

func TestRenderCard_NotFound(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	_, _, _, err := c.svc.RenderCard(ctx, testUserID, "00000000-0000-0000-0000-0000000000ff")
	if !apperr.IsNotFound(err) {
		t.Fatalf("missing card: got %v, want NotFound", err)
	}

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "q", "Back": "a"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v, %d cards", err, len(cards))
	}
	const otherUser = "00000000-0000-0000-0000-0000000000aa"
	if _, _, _, err := c.svc.RenderCard(ctx, otherUser, cards[0].ID); !apperr.IsNotFound(err) {
		t.Fatalf("other user's card: got %v, want NotFound", err)
	}
}

func TestRenderCards_RendersBatchAndSkipsMissing(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	var ids []string
	for _, q := range []string{"q1", "q2"} {
		note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": q, "Back": "a-" + q}, nil, 0)
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
		if err != nil || len(cards) != 1 {
			t.Fatalf("ListCardsByNote: %v, %d cards", err, len(cards))
		}
		ids = append(ids, cards[0].ID)
	}
	const missing = "00000000-0000-0000-0000-0000000000ff"

	got, err := c.svc.RenderCards(ctx, testUserID, []string{ids[1], missing, ids[0]})
	if err != nil {
		t.Fatalf("RenderCards: %v", err)
	}
	byID := map[string]RenderedFront{}
	for _, r := range got {
		byID[r.CardID] = r
	}
	if len(got) != 2 || byID[ids[0]].Front != "q1" || byID[ids[1]].Front != "q2" {
		t.Fatalf("want both cards rendered and the missing id skipped, got %+v", got)
	}

	const otherUser = "00000000-0000-0000-0000-0000000000aa"
	if got, err := c.svc.RenderCards(ctx, otherUser, ids); err != nil || len(got) != 0 {
		t.Fatalf("other user's cards: got %+v, %v; want none", got, err)
	}
}

// ─── UpdateNote produces delta cards ──────────────────────────────────────────

func TestNote_UpdateDeltaCards(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)

	// Basic+Reversed: 2 templates; Back empty → only 1 card initially.
	nt, _ := c.svc.CreateNoteType(ctx, testUserID, testProfileID, "Basic+Rev", "",
		[]Field{{Name: "Front"}, {Name: "Back"}},
		[]CardTemplate{
			{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"},
			{Name: "Card 2", Front: "{{Back}}", Back: "{{Front}}"},
		})
	note, _ := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "Hi", "Back": ""},
		nil, 0)

	cards, _ := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if len(cards) != 1 {
		t.Fatalf("want 1 card initially, got %d", len(cards))
	}
	origID := cards[0].ID

	// Fill in Back → second card created, first preserved.
	_, err := c.svc.UpdateNote(ctx, testUserID, note.ID, note.USN,
		map[string]string{"Front": "Hi", "Back": "There"},
		nil, 0)
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}

	cards2, _ := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if len(cards2) != 2 {
		t.Fatalf("want 2 cards after update, got %d", len(cards2))
	}
	found := false
	for _, card := range cards2 {
		if card.ID == origID {
			found = true
			if card.State != StateNew {
				t.Fatalf("original card state mutated: got %v", card.State)
			}
		}
	}
	if !found {
		t.Fatal("original card was deleted instead of preserved")
	}
}

// ─── Stale USN → Aborted ──────────────────────────────────────────────────────

func TestOptimisticConcurrency_StaleUSN(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := basicNoteType(t, c)
	_, err := c.svc.UpdateNoteType(ctx, testUserID, nt.ID, nt.USN+99, "X", "", nt.Fields, nt.CardTemplates)
	if err == nil {
		t.Fatal("want Aborted for stale USN, got nil")
	}
	if !apperr.IsAborted(err) {
		t.Fatalf("want Aborted, got %T: %v", err, err)
	}
}

// ─── FK violation → FailedPrecondition ───────────────────────────────────────

func TestDeleteNoteType_WithNotes_FailedPrecondition(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	_, _ = c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "x", "Back": "y"}, nil, 0)

	err := c.svc.DeleteNoteType(ctx, testUserID, nt.ID, 0)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !apperr.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %T: %v", err, err)
	}
}

// ─── DeleteNote cascades cards + tombstones ───────────────────────────────────

func TestDeleteNote_CascadesCardsAndTombstones(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	note, _ := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "hi", "Back": "bye"}, nil, 0)

	cards, _ := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	cardID := cards[0].ID

	if err := c.svc.DeleteNote(ctx, testUserID, note.ID, 0); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}

	_, err := c.repo.GetCard(ctx, testUserID, cardID)
	if !apperr.IsNotFound(err) {
		t.Fatalf("want card NotFound, got %v", err)
	}

	var tombCount int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tombstones WHERE user_id=$1`, testUserID).Scan(&tombCount); err != nil {
		t.Fatalf("tombstone count: %v", err)
	}
	if tombCount < 2 {
		t.Fatalf("want ≥2 tombstones, got %d", tombCount)
	}
}

// ─── BulkCards move ──────────────────────────────────────────────────────────

func TestBulkCards_Move(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck1 := defaultDeck(t, c)
	deck2, _ := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Deck2"})
	nt := basicNoteType(t, c)
	note, _ := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck1.ID,
		map[string]string{"Front": "x", "Back": "y"}, nil, 0)

	cards, _ := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	ids := make([]string, len(cards))
	for i, card := range cards {
		ids[i] = card.ID
	}

	affected, err := c.svc.MoveCards(ctx, testUserID, ids, deck2.ID)
	if err != nil {
		t.Fatalf("MoveCards: %v", err)
	}
	if affected != 1 {
		t.Fatalf("want 1 affected, got %d", affected)
	}

	card, _ := c.repo.GetCard(ctx, testUserID, ids[0])
	if card.DeckID != deck2.ID {
		t.Fatalf("want deck2, got %s", card.DeckID)
	}
}

// ─── Outbox written atomically ────────────────────────────────────────────────

// ─── GetSearchDocs (Search enrichment) ───────────────────────────────────────

func TestGetSearchDocs_ByCardAndNote(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "hola", "Back": "hello"}, []string{"spanish"}, 2)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, _ := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	card := cards[0]

	want := func(docs []SearchDoc) {
		t.Helper()
		if len(docs) != 1 {
			t.Fatalf("want 1 doc, got %d", len(docs))
		}
		d := docs[0]
		if d.CardID != card.ID || d.NoteID != note.ID || d.DeckID != deck.ID {
			t.Errorf("id mismatch: %+v", d)
		}
		// DeckPath is the human-readable name path; a top-level deck's path is
		// just its name.
		if d.DeckPath != deck.Name {
			t.Errorf("DeckPath = %q, want %q", d.DeckPath, deck.Name)
		}
		if d.FieldsText != "hello hola" {
			t.Errorf("FieldsText = %q, want %q", d.FieldsText, "hello hola")
		}
		if len(d.Tags) != 1 || d.Tags[0] != "spanish" {
			t.Errorf("Tags = %v, want [spanish]", d.Tags)
		}
		if d.Flags != 2 {
			t.Errorf("Flags = %d, want 2", d.Flags)
		}
		if d.Version != card.USN {
			t.Errorf("Version = %d, want card USN %d", d.Version, card.USN)
		}
	}

	byCard, _, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{CardIDs: []string{card.ID}}, Page{})
	if err != nil {
		t.Fatalf("GetSearchDocs by card: %v", err)
	}
	want(byCard)

	byNote, _, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{NoteIDs: []string{note.ID}}, Page{})
	if err != nil {
		t.Fatalf("GetSearchDocs by note: %v", err)
	}
	want(byNote)

	// Tenant isolation: another user sees nothing for the same card id.
	const otherUser = "00000000-0000-0000-0000-0000000000ff"
	other, _, err := c.repo.GetSearchDocs(ctx, otherUser, SearchDocSelector{CardIDs: []string{card.ID}}, Page{})
	if err != nil {
		t.Fatalf("GetSearchDocs other user: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("cross-tenant leak: got %d docs for other user", len(other))
	}
}

func TestGetSearchDocs_ByDeckSubtree(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := basicNoteType(t, c)
	parent, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Spanish"})
	if err != nil {
		t.Fatalf("CreateDeck parent: %v", err)
	}
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Verbs", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, child.ID,
		map[string]string{"Front": "comer", "Back": "to eat"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// Selecting the parent deck reindexes the whole subtree, so the card in the
	// child deck must come back.
	docs, _, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{DeckIDs: []string{parent.ID}}, Page{})
	if err != nil {
		t.Fatalf("GetSearchDocs by deck subtree: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("want 1 doc for subtree, got %d", len(docs))
	}
	if docs[0].NoteID != note.ID || docs[0].DeckID != child.ID {
		t.Errorf("unexpected doc: %+v", docs[0])
	}
	// The name path joins ancestor names root→leaf with "::".
	if docs[0].DeckPath != "Spanish::Verbs" {
		t.Errorf("DeckPath = %q, want %q", docs[0].DeckPath, "Spanish::Verbs")
	}
}

// searchDocPages collects every page of a deck's search docs, returning the page sizes.
func searchDocPages(t *testing.T, c integrationComponents, deckID string, size int) ([]SearchDoc, []int) {
	t.Helper()
	var all []SearchDoc
	var sizes []int
	page := Page{Size: size}
	for {
		docs, next, err := c.repo.GetSearchDocs(context.Background(), testUserID, SearchDocSelector{DeckIDs: []string{deckID}}, page)
		if err != nil {
			t.Fatalf("GetSearchDocs: %v", err)
		}
		all = append(all, docs...)
		sizes = append(sizes, len(docs))
		if next == "" {
			return all, sizes
		}
		page.Token = next
	}
}

func TestGetSearchDocs_Pages(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	for i := range 3 {
		if _, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": fmt.Sprint("q", i), "Back": "a"}, nil, 0); err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
	}

	docs, sizes := searchDocPages(t, c, deck.ID, 2)
	if !slices.Equal(sizes, []int{2, 1}) {
		t.Fatalf("page sizes = %v, want [2 1]", sizes)
	}
	seen := map[string]bool{}
	for _, d := range docs {
		seen[d.CardID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("distinct cards = %d, want 3", len(seen))
	}
}

func TestGetSearchDocs_PageStopsAtByteBudget(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	const notes = 5
	big := strings.Repeat("a", maxNoteBytes-1024)
	for range notes {
		if _, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": big}, nil, 0); err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
	}

	docs, sizes := searchDocPages(t, c, deck.ID, 0)
	if len(docs) != notes || len(sizes) < 2 {
		t.Fatalf("got %d docs in pages %v, want %d docs over several pages", len(docs), sizes, notes)
	}
}

func TestOutbox_WrittenAtomically(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	var before int
	_ = c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE user_id=$1`, testUserID).Scan(&before)

	basicNoteType(t, c)

	var after int
	_ = c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE user_id=$1`, testUserID).Scan(&after)
	if after <= before {
		t.Fatalf("want new outbox rows after CreateNoteType, count unchanged at %d", after)
	}
}

// ─── Deck move cycle check ───────────────────────────────────────────────────

func TestUpdateDeck_RejectsCycles(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	root := defaultDeck(t, c)
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: root.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	grandchild, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Grandchild", ParentID: child.ID})
	if err != nil {
		t.Fatalf("CreateDeck grandchild: %v", err)
	}

	for name, parentID := range map[string]string{"self": root.ID, "child": child.ID, "grandchild": grandchild.ID} {
		t.Run(name, func(t *testing.T) {
			_, err := c.svc.UpdateDeck(ctx, testUserID, root.ID, 0, DeckFields{Name: root.Name, ParentID: parentID})
			if !apperr.IsInvalidArgument(err) {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
		})
	}

	got, err := c.svc.GetDeck(ctx, testUserID, root.ID)
	if err != nil {
		t.Fatalf("GetDeck: %v", err)
	}
	if got.ParentID != nil || got.Path != root.Path {
		t.Fatalf("root changed after rejected moves: parent=%v path=%q", got.ParentID, got.Path)
	}
}

func TestUpdateDeck_ConcurrentSwapNoCycle(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	for i := range 10 {
		a, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "A"})
		if err != nil {
			t.Fatalf("CreateDeck A: %v", err)
		}
		b, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "B"})
		if err != nil {
			t.Fatalf("CreateDeck B: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		move := func(id, name, parentID string) {
			<-start
			_, err := c.svc.UpdateDeck(ctx, testUserID, id, 0, DeckFields{Name: name, ParentID: parentID})
			errs <- err
		}
		go move(a.ID, "A", b.ID)
		go move(b.ID, "B", a.ID)
		close(start)
		var ok int
		for range 2 {
			err := <-errs
			switch {
			case err == nil:
				ok++
			case !apperr.IsInvalidArgument(err):
				t.Fatalf("iter %d: unexpected error %v", i, err)
			}
		}
		if ok != 1 {
			t.Fatalf("iter %d: %d moves succeeded, want exactly 1", i, ok)
		}

		gotA, err := c.svc.GetDeck(ctx, testUserID, a.ID)
		if err != nil {
			t.Fatalf("GetDeck A: %v", err)
		}
		gotB, err := c.svc.GetDeck(ctx, testUserID, b.ID)
		if err != nil {
			t.Fatalf("GetDeck B: %v", err)
		}
		if gotA.ParentID != nil && gotB.ParentID != nil {
			t.Fatalf("iter %d: cycle A↔B", i)
		}
		if gotA.Path != deckPath(a.ID, pathOfParent(t, c, gotA)) || gotB.Path != deckPath(b.ID, pathOfParent(t, c, gotB)) {
			t.Fatalf("iter %d: inconsistent paths A=%q B=%q", i, gotA.Path, gotB.Path)
		}
	}
}

func TestCreateDeck_ConcurrentWithParentMove(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	for i := range 10 {
		parent, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "P"})
		if err != nil {
			t.Fatalf("CreateDeck P: %v", err)
		}
		target, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "T"})
		if err != nil {
			t.Fatalf("CreateDeck T: %v", err)
		}

		start := make(chan struct{})
		var child *Deck
		createErr := make(chan error, 1)
		moved := make(chan error, 1)
		go func() {
			<-start
			var err error
			child, err = c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "C", ParentID: parent.ID})
			createErr <- err
		}()
		go func() {
			<-start
			_, err := c.svc.UpdateDeck(ctx, testUserID, parent.ID, 0, DeckFields{Name: "P", ParentID: target.ID})
			moved <- err
		}()
		close(start)
		cErr, mErr := <-createErr, <-moved
		if cErr != nil || mErr != nil {
			t.Fatalf("iter %d: create child: %v, move parent: %v", i, cErr, mErr)
		}

		got, err := c.svc.GetDeck(ctx, testUserID, child.ID)
		if err != nil {
			t.Fatalf("GetDeck child: %v", err)
		}
		if want := deckPath(child.ID, pathOfParent(t, c, got)); got.Path != want {
			t.Fatalf("iter %d: child path %q, want %q", i, got.Path, want)
		}
	}
}

func pathOfParent(t *testing.T, c integrationComponents, d *Deck) string {
	t.Helper()
	if d.ParentID == nil {
		return ""
	}
	p, err := c.svc.GetDeck(context.Background(), testUserID, *d.ParentID)
	if err != nil {
		t.Fatalf("GetDeck parent: %v", err)
	}
	return p.Path
}

// ─── Deck / config deletion ──────────────────────────────────────────────────

func TestDeleteDeck_ReparentsChildrenToGrandparent(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	root := defaultDeck(t, c)
	mid, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Mid", ParentID: root.ID})
	if err != nil {
		t.Fatalf("CreateDeck mid: %v", err)
	}
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: mid.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	grandchild, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Grandchild", ParentID: child.ID})
	if err != nil {
		t.Fatalf("CreateDeck grandchild: %v", err)
	}

	if err := c.svc.DeleteDeck(ctx, testUserID, mid.ID, 0); err != nil {
		t.Fatalf("DeleteDeck: %v", err)
	}
	if _, err := c.svc.GetDeck(ctx, testUserID, mid.ID); !apperr.IsNotFound(err) {
		t.Fatalf("deleted deck: got %v, want NotFound", err)
	}

	gotChild, err := c.svc.GetDeck(ctx, testUserID, child.ID)
	if err != nil {
		t.Fatalf("GetDeck child: %v", err)
	}
	if gotChild.ParentID == nil || *gotChild.ParentID != root.ID {
		t.Fatalf("child parent = %v, want %s", gotChild.ParentID, root.ID)
	}
	if want := deckPath(child.ID, root.Path); gotChild.Path != want {
		t.Fatalf("child path %q, want %q", gotChild.Path, want)
	}
	gotGrand, err := c.svc.GetDeck(ctx, testUserID, grandchild.ID)
	if err != nil {
		t.Fatalf("GetDeck grandchild: %v", err)
	}
	if gotGrand.ParentID == nil || *gotGrand.ParentID != child.ID {
		t.Fatalf("grandchild parent = %v, want %s", gotGrand.ParentID, child.ID)
	}
	if want := deckPath(grandchild.ID, gotChild.Path); gotGrand.Path != want {
		t.Fatalf("grandchild path %q, want %q", gotGrand.Path, want)
	}
	if gotChild.USN <= child.USN || gotGrand.USN <= grandchild.USN {
		t.Fatalf("descendant USNs not bumped: child %d→%d, grandchild %d→%d",
			child.USN, gotChild.USN, grandchild.USN, gotGrand.USN)
	}

	var updates int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE user_id = $1 AND event_type = $2 AND aggregate_id = ANY($3)`,
		testUserID, EventDeckUpdated, []string{child.ID, grandchild.ID}).Scan(&updates); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if updates != 2 {
		t.Fatalf("deck.updated outbox rows for descendants = %d, want 2", updates)
	}
}

func TestDeleteDeck_RootChildrenBecomeRoots(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	root := defaultDeck(t, c)
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: root.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	if err := c.svc.DeleteDeck(ctx, testUserID, root.ID, 0); err != nil {
		t.Fatalf("DeleteDeck: %v", err)
	}
	got, err := c.svc.GetDeck(ctx, testUserID, child.ID)
	if err != nil {
		t.Fatalf("GetDeck child: %v", err)
	}
	if got.ParentID != nil || got.Path != deckPath(child.ID, "") {
		t.Fatalf("child not promoted to root: parent=%v path=%q", got.ParentID, got.Path)
	}
}

func TestDeleteDeck_WithCardsFailsAndKeepsChildren(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	deck := defaultDeck(t, c)
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: deck.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	nt := basicNoteType(t, c)
	if _, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "q", "Back": "a"}, nil, 0); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	if err := c.svc.DeleteDeck(ctx, testUserID, deck.ID, 0); !apperr.IsFailedPrecondition(err) {
		t.Fatalf("got %v, want FailedPrecondition", err)
	}
	got, err := c.svc.GetDeck(ctx, testUserID, child.ID)
	if err != nil {
		t.Fatalf("GetDeck child: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != deck.ID || got.Path != child.Path || got.USN != child.USN {
		t.Fatalf("child changed by failed delete: %+v", got)
	}
}

func TestDeckConfig_MaxIntervalDays(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	cfg, err := c.svc.CreateDeckConfig(ctx, testUserID, testProfileID, DeckConfig{Name: "Default cap", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("CreateDeckConfig: %v", err)
	}
	if cfg.MaxIntervalDays != maxIntervalDays {
		t.Fatalf("unset max_interval_days = %d, want %d", cfg.MaxIntervalDays, maxIntervalDays)
	}
	if _, err := c.svc.UpdateDeckConfig(ctx, testUserID, cfg.ID, 0, DeckConfig{Name: "Capped", Timezone: "UTC", MaxIntervalDays: 30}); err != nil {
		t.Fatalf("UpdateDeckConfig: %v", err)
	}
	got, err := c.svc.GetDeckConfig(ctx, testUserID, cfg.ID)
	if err != nil || got.MaxIntervalDays != 30 {
		t.Fatalf("stored max_interval_days = %v (err %v), want 30", got, err)
	}
}

func TestDeleteDeckConfig_ResetsDecks(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	cfg, err := c.svc.CreateDeckConfig(ctx, testUserID, testProfileID, DeckConfig{
		Name: "Custom", NewPerDay: 10, RevPerDay: 100, DesiredRetention: 0.9, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateDeckConfig: %v", err)
	}
	deck, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Uses config", ConfigID: cfg.ID})
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}

	if err := c.svc.DeleteDeckConfig(ctx, testUserID, cfg.ID, 0); err != nil {
		t.Fatalf("DeleteDeckConfig: %v", err)
	}
	got, err := c.svc.GetDeck(ctx, testUserID, deck.ID)
	if err != nil {
		t.Fatalf("GetDeck: %v", err)
	}
	if got.ConfigID != nil {
		t.Fatalf("deck still references deleted config %s", *got.ConfigID)
	}
	if got.USN <= deck.USN {
		t.Fatalf("deck USN not bumped: %d→%d", deck.USN, got.USN)
	}
}

func TestDecksForeignKeysRestrict(t *testing.T) {
	c := setupIntegration(t)
	rows, err := c.pool.Query(context.Background(),
		`SELECT conname, confdeltype FROM pg_constraint WHERE conrelid = 'decks'::regclass AND contype = 'f'`)
	if err != nil {
		t.Fatalf("query constraints: %v", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var name, delType string
		if err := rows.Scan(&name, &delType); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		if delType != "r" {
			t.Errorf("%s: ON DELETE %q, want RESTRICT", name, delType)
		}
	}
	if n != 2 {
		t.Fatalf("decks has %d foreign keys, want 2", n)
	}
}

func TestMigrations_Citus(t *testing.T) {
	c := setupIntegrationOn(t, "citusdata/citus:13.0", true)
	ctx := context.Background()

	var distributed int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_dist_partition WHERE logicalrelid::text IN ('decks','deck_configs','cards')`).Scan(&distributed); err != nil {
		t.Fatalf("query pg_dist_partition: %v", err)
	}
	if distributed != 3 {
		t.Fatalf("distributed tables = %d, want 3", distributed)
	}

	root := defaultDeck(t, c)
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: root.ID})
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	if err := c.svc.DeleteDeck(ctx, testUserID, root.ID, 0); err != nil {
		t.Fatalf("DeleteDeck: %v", err)
	}
	got, err := c.svc.GetDeck(ctx, testUserID, child.ID)
	if err != nil {
		t.Fatalf("GetDeck child: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child parent = %v, want root", *got.ParentID)
	}

	cfg, err := c.svc.CreateDeckConfig(ctx, testUserID, testProfileID, DeckConfig{
		Name: "Custom", NewPerDay: 10, RevPerDay: 100, DesiredRetention: 0.9, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateDeckConfig: %v", err)
	}
	if _, err := c.svc.UpdateDeck(ctx, testUserID, child.ID, 0, DeckFields{Name: child.Name, ConfigID: cfg.ID}); err != nil {
		t.Fatalf("UpdateDeck config: %v", err)
	}
	if err := c.svc.DeleteDeckConfig(ctx, testUserID, cfg.ID, 0); err != nil {
		t.Fatalf("DeleteDeckConfig: %v", err)
	}
}

func TestRepoDeleteDeck_WithSubdecksFailedPrecondition(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	root := defaultDeck(t, c)
	if _, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Child", ParentID: root.ID}); err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	err := c.repo.DeleteDeck(ctx, testUserID, root.ID)
	if !apperr.IsFailedPrecondition(err) || !strings.Contains(err.Error(), "subdecks") {
		t.Fatalf("got %v, want FailedPrecondition about subdecks", err)
	}
}

// ─── Note deck handling ──────────────────────────────────────────────────────

func TestCreateNote_DeckValidation(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	fields := map[string]string{"Front": "q", "Back": "a"}

	tests := []struct {
		name   string
		deckID string
		fields map[string]string
		want   func(error) bool
	}{
		{"missing deck_id", "", fields, apperr.IsInvalidArgument},
		{"unknown deck", "00000000-0000-0000-0000-0000000000dd", fields, apperr.IsNotFound},
		{"no cards generated", deck.ID, map[string]string{"Front": "", "Back": "a"}, apperr.IsInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, tc.deckID, tc.fields, nil, 0); !tc.want(err) {
				t.Fatalf("got %v", err)
			}
		})
	}

	var notes int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM notes WHERE user_id = $1`, testUserID).Scan(&notes); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 0 {
		t.Fatalf("rejected creates left %d notes", notes)
	}
}

func TestUpdateNote_RejectsRemovingAllCards(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt := basicNoteType(t, c)
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "q", "Back": "a"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	_, err = c.svc.UpdateNote(ctx, testUserID, note.ID, note.USN, map[string]string{"Front": "", "Back": "a"}, nil, 0)
	if !apperr.IsInvalidArgument(err) {
		t.Fatalf("got %v, want InvalidArgument", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards after rejected update: %v, %d", err, len(cards))
	}
}

func TestUpdateNote_NewCardJoinsFirstCardsDeck(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	other, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, DeckFields{Name: "Other"})
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	nt, err := c.svc.CreateNoteType(ctx, testUserID, testProfileID, "Cloze", "",
		[]Field{{Name: "Text"}}, []CardTemplate{{Name: "Cloze", Front: "{{cloze:Text}}", Back: "{{cloze:Text}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Text": "{{c1::a}}"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
	}
	if _, err := c.svc.MoveCards(ctx, testUserID, []string{cards[0].ID}, other.ID); err != nil {
		t.Fatalf("MoveCards: %v", err)
	}
	note, err = c.svc.GetNote(ctx, testUserID, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}

	if _, err := c.svc.UpdateNote(ctx, testUserID, note.ID, note.USN,
		map[string]string{"Text": "{{c1::a}} {{c2::b}}"}, nil, 0); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	cards, err = c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 2 {
		t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
	}
	if cards[1].DeckID != other.ID {
		t.Fatalf("new card deck = %s, want %s", cards[1].DeckID, other.ID)
	}
}

func TestDeleteCards_LastCardDeletesNote(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt, err := c.svc.CreateNoteType(ctx, testUserID, testProfileID, "Basic+Rev", "",
		[]Field{{Name: "Front"}, {Name: "Back"}},
		[]CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}, {Name: "Card 2", Front: "{{Back}}", Back: "{{Front}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "q", "Back": "a"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
	if err != nil || len(cards) != 2 {
		t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
	}

	if _, err := c.svc.DeleteCards(ctx, testUserID, []string{cards[0].ID}); err != nil {
		t.Fatalf("DeleteCards first: %v", err)
	}
	if _, err := c.svc.GetNote(ctx, testUserID, note.ID); err != nil {
		t.Fatalf("note deleted with a card left: %v", err)
	}

	if _, err := c.svc.DeleteCards(ctx, testUserID, []string{cards[1].ID}); err != nil {
		t.Fatalf("DeleteCards last: %v", err)
	}
	if _, err := c.svc.GetNote(ctx, testUserID, note.ID); !apperr.IsNotFound(err) {
		t.Fatalf("note after last card deleted: got %v, want NotFound", err)
	}
	var tombstones int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tombstones WHERE user_id = $1 AND type = $2 AND id = $3`,
		testUserID, TombstoneNote, note.ID).Scan(&tombstones); err != nil {
		t.Fatalf("count tombstones: %v", err)
	}
	if tombstones != 1 {
		t.Fatalf("note tombstones = %d, want 1", tombstones)
	}

	both, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
		map[string]string{"Front": "q2", "Back": "a2"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err = c.repo.ListCardsByNote(ctx, testUserID, both.ID)
	if err != nil || len(cards) != 2 {
		t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
	}
	affected, err := c.svc.DeleteCards(ctx, testUserID, []string{cards[0].ID, cards[1].ID})
	if err != nil || affected != 2 {
		t.Fatalf("DeleteCards both: affected=%d err=%v", affected, err)
	}
	var noteDeletes int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE user_id = $1 AND event_type = $2 AND aggregate_id = $3`,
		testUserID, EventNoteDeleted, both.ID).Scan(&noteDeletes); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if noteDeletes != 1 {
		t.Fatalf("note.deleted events = %d, want 1", noteDeletes)
	}
}
