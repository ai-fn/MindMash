//go:build integration

package collection

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

type integrationComponents struct {
	pool *pgxpool.Pool
	repo *PostgresRepository
	svc  *Service
}

func setupIntegration(t *testing.T) integrationComponents {
	t.Helper()
	ctx := context.Background()

	// The postgres image restarts its server process after initdb; without an
	// explicit occurrence count the default wait strategy can report ready
	// after the first (pre-restart) boot, so the first connection attempt
	// hits a server that's about to restart ("connection reset by peer").
	pgCtr, err := postgres.Run(ctx, "postgres:18-alpine",
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
	if err := platformdb.RunMigrations(ctx, dsn, "../../migrations/collection"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := NewPostgresRepository(pool)
	svc := NewService(repo, zap.NewNop())
	return integrationComponents{pool: pool, repo: repo, svc: svc}
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
	d, err := c.svc.CreateDeck(context.Background(), testUserID, testProfileID, "Default", "", "")
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
	if !platformerrors.IsNotFound(err) {
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
	if _, _, _, err := c.svc.RenderCard(ctx, otherUser, cards[0].ID); !platformerrors.IsNotFound(err) {
		t.Fatalf("other user's card: got %v, want NotFound", err)
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
	if !platformerrors.IsAborted(err) {
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
	if !platformerrors.IsFailedPrecondition(err) {
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
	if !platformerrors.IsNotFound(err) {
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
	deck2, _ := c.svc.CreateDeck(ctx, testUserID, testProfileID, "Deck2", "", "")
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

	byCard, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{CardIDs: []string{card.ID}})
	if err != nil {
		t.Fatalf("GetSearchDocs by card: %v", err)
	}
	want(byCard)

	byNote, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{NoteIDs: []string{note.ID}})
	if err != nil {
		t.Fatalf("GetSearchDocs by note: %v", err)
	}
	want(byNote)

	// Tenant isolation: another user sees nothing for the same card id.
	const otherUser = "00000000-0000-0000-0000-0000000000ff"
	other, err := c.repo.GetSearchDocs(ctx, otherUser, SearchDocSelector{CardIDs: []string{card.ID}})
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
	parent, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "Spanish", "", "")
	if err != nil {
		t.Fatalf("CreateDeck parent: %v", err)
	}
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "Verbs", parent.ID, "")
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
	docs, err := c.repo.GetSearchDocs(ctx, testUserID, SearchDocSelector{DeckIDs: []string{parent.ID}})
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
	child, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "Child", root.ID, "")
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	grandchild, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "Grandchild", child.ID, "")
	if err != nil {
		t.Fatalf("CreateDeck grandchild: %v", err)
	}

	for name, parentID := range map[string]string{"self": root.ID, "child": child.ID, "grandchild": grandchild.ID} {
		t.Run(name, func(t *testing.T) {
			_, err := c.svc.UpdateDeck(ctx, testUserID, root.ID, 0, root.Name, parentID, "")
			if !platformerrors.IsInvalidArgument(err) {
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
		a, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "A", "", "")
		if err != nil {
			t.Fatalf("CreateDeck A: %v", err)
		}
		b, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "B", "", "")
		if err != nil {
			t.Fatalf("CreateDeck B: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		move := func(id, name, parentID string) {
			<-start
			_, err := c.svc.UpdateDeck(ctx, testUserID, id, 0, name, parentID, "")
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
			case !platformerrors.IsInvalidArgument(err):
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
		parent, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "P", "", "")
		if err != nil {
			t.Fatalf("CreateDeck P: %v", err)
		}
		target, err := c.svc.CreateDeck(ctx, testUserID, testProfileID, "T", "", "")
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
			child, err = c.svc.CreateDeck(ctx, testUserID, testProfileID, "C", parent.ID, "")
			createErr <- err
		}()
		go func() {
			<-start
			_, err := c.svc.UpdateDeck(ctx, testUserID, parent.ID, 0, "P", target.ID, "")
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
