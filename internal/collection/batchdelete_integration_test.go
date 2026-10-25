//go:build integration

package collection

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

type stmtCounter struct{ n atomic.Int64 }

func (c *stmtCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *stmtCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// countingService returns a Service whose pool counts every statement it sends.
func countingService(t *testing.T, c integrationComponents) (*Service, func(func()) int64) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(c.dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	counter := &stmtCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	svc := NewService(NewPostgresRepository(pool), zap.NewNop())
	return svc, func(fn func()) int64 {
		before := counter.n.Load()
		fn()
		return counter.n.Load() - before
	}
}

func noteTypeWithTemplates(t *testing.T, c integrationComponents, name string, n int) *NoteType {
	t.Helper()
	templates := make([]CardTemplate, n)
	for i := range templates {
		templates[i] = CardTemplate{Name: name + string(rune('A'+i)), Front: "{{Front}}", Back: "{{Back}}"}
	}
	nt, err := c.svc.CreateNoteType(context.Background(), testUserID, testProfileID, name, "",
		[]Field{{Name: "Front"}, {Name: "Back"}}, templates)
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	return nt
}

func countTombstones(t *testing.T, c integrationComponents, typ string, ids []string) int {
	t.Helper()
	var n int
	if err := c.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM tombstones WHERE user_id = $1 AND type = $2 AND id = ANY($3::uuid[])`,
		testUserID, typ, ids).Scan(&n); err != nil {
		t.Fatalf("count tombstones: %v", err)
	}
	return n
}

func cardIDs(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

func TestDeleteNote_StatementsIndependentOfCardCount(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	svc, stmts := countingService(t, c)
	deck := defaultDeck(t, c)

	deleteNote := func(templates int) (int64, []Card, string) {
		nt := noteTypeWithTemplates(t, c, "T"+string(rune('0'+templates)), templates)
		note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": "q", "Back": "a"}, nil, 0)
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
		if err != nil || len(cards) != templates {
			t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
		}
		n := stmts(func() {
			if err := svc.DeleteNote(ctx, testUserID, note.ID, 0); err != nil {
				t.Fatalf("DeleteNote: %v", err)
			}
		})
		return n, cards, note.ID
	}

	one, _, _ := deleteNote(1)
	many, cards, noteID := deleteNote(4)
	if one != many {
		t.Fatalf("statements: 1 card = %d, 4 cards = %d; want equal", one, many)
	}
	if got := countTombstones(t, c, TombstoneCard, cardIDs(cards)); got != len(cards) {
		t.Fatalf("card tombstones = %d, want %d", got, len(cards))
	}
	if got := countTombstones(t, c, TombstoneNote, []string{noteID}); got != 1 {
		t.Fatalf("note tombstones = %d, want 1", got)
	}
}

func TestDeleteCards_StatementsIndependentOfNoteCount(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	svc, stmts := countingService(t, c)
	deck := defaultDeck(t, c)
	nt := noteTypeWithTemplates(t, c, "Two", 2)

	// Each note keeps one card, so only the fully deleted notes may go.
	deleteAll := func(notes int) (int64, []string, []string) {
		var del, kept, noteIDs []string
		for range notes {
			note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
				map[string]string{"Front": "q", "Back": "a"}, nil, 0)
			if err != nil {
				t.Fatalf("CreateNote: %v", err)
			}
			cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
			if err != nil || len(cards) != 2 {
				t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
			}
			del = append(del, cardIDs(cards)...)
			noteIDs = append(noteIDs, note.ID)
		}
		keep, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": "k", "Back": "k"}, nil, 0)
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		keepCards, err := c.repo.ListCardsByNote(ctx, testUserID, keep.ID)
		if err != nil || len(keepCards) != 2 {
			t.Fatalf("ListCardsByNote: %v, %d", err, len(keepCards))
		}
		del = append(del, keepCards[0].ID)
		kept = append(kept, keep.ID)

		var affected int64
		n := stmts(func() {
			if affected, err = svc.DeleteCards(ctx, testUserID, del); err != nil {
				t.Fatalf("DeleteCards: %v", err)
			}
		})
		if affected != int64(len(del)) {
			t.Fatalf("affected = %d, want %d", affected, len(del))
		}
		return n, noteIDs, kept
	}

	one, _, _ := deleteAll(1)
	many, gone, kept := deleteAll(4)
	if one != many {
		t.Fatalf("statements: 1 note = %d, 4 notes = %d; want equal", one, many)
	}
	for _, id := range gone {
		if _, err := c.svc.GetNote(ctx, testUserID, id); !apperr.IsNotFound(err) {
			t.Fatalf("orphan note %s: got %v, want NotFound", id, err)
		}
	}
	if got := countTombstones(t, c, TombstoneNote, gone); got != len(gone) {
		t.Fatalf("note tombstones = %d, want %d", got, len(gone))
	}
	if _, err := c.svc.GetNote(ctx, testUserID, kept[0]); err != nil {
		t.Fatalf("note with a card left was deleted: %v", err)
	}
	if got := countTombstones(t, c, TombstoneNote, kept); got != 0 {
		t.Fatalf("kept note tombstones = %d, want 0", got)
	}
	var events int
	if err := c.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM outbox WHERE user_id = $1 AND event_type = $2 AND aggregate_id = ANY($3::uuid[])`,
		testUserID, EventNoteDeleted, gone).Scan(&events); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if events != len(gone) {
		t.Fatalf("note.deleted events = %d, want %d", events, len(gone))
	}
}

func TestUpdateNote_StatementsIndependentOfRemovedCards(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	svc, stmts := countingService(t, c)
	deck := defaultDeck(t, c)
	nt, err := c.svc.CreateNoteType(ctx, testUserID, testProfileID, "Cloze", "",
		[]Field{{Name: "Text"}}, []CardTemplate{{Name: "Cloze", Front: "{{cloze:Text}}", Back: "{{cloze:Text}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}

	shrink := func(text string, removed int) int64 {
		note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Text": text}, nil, 0)
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		before, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
		if err != nil || len(before) != removed+1 {
			t.Fatalf("ListCardsByNote: %v, %d", err, len(before))
		}
		n := stmts(func() {
			if _, err := svc.UpdateNote(ctx, testUserID, note.ID, note.USN,
				map[string]string{"Text": "{{c1::a}}"}, nil, 0); err != nil {
				t.Fatalf("UpdateNote: %v", err)
			}
		})
		after, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
		if err != nil || len(after) != 1 {
			t.Fatalf("cards after update: %v, %d", err, len(after))
		}
		if got := countTombstones(t, c, TombstoneCard, cardIDs(before)); got != removed {
			t.Fatalf("card tombstones = %d, want %d", got, removed)
		}
		return n
	}

	one := shrink("{{c1::a}} {{c2::b}}", 1)
	many := shrink("{{c1::a}} {{c2::b}} {{c3::c}} {{c4::d}}", 3)
	if one != many {
		t.Fatalf("statements: 1 removed = %d, 3 removed = %d; want equal", one, many)
	}
}

func TestBatchDeletes_Citus(t *testing.T) {
	c := setupIntegrationOn(t, "citusdata/citus:13.0", true)
	ctx := context.Background()
	deck := defaultDeck(t, c)
	nt := noteTypeWithTemplates(t, c, "Two", 2)

	newNote := func() (*Note, []Card) {
		note, err := c.svc.CreateNote(ctx, testUserID, testProfileID, nt.ID, deck.ID,
			map[string]string{"Front": "q", "Back": "a"}, nil, 0)
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		cards, err := c.repo.ListCardsByNote(ctx, testUserID, note.ID)
		if err != nil || len(cards) != 2 {
			t.Fatalf("ListCardsByNote: %v, %d", err, len(cards))
		}
		return note, cards
	}

	orphan, cards := newNote()
	if _, err := c.svc.DeleteCards(ctx, testUserID, cardIDs(cards)); err != nil {
		t.Fatalf("DeleteCards: %v", err)
	}
	if _, err := c.svc.GetNote(ctx, testUserID, orphan.ID); !apperr.IsNotFound(err) {
		t.Fatalf("orphan note: got %v, want NotFound", err)
	}

	note, cards := newNote()
	if err := c.svc.DeleteNote(ctx, testUserID, note.ID, 0); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if got := countTombstones(t, c, TombstoneCard, cardIDs(cards)); got != 2 {
		t.Fatalf("card tombstones = %d, want 2", got)
	}
	if got := countTombstones(t, c, TombstoneNote, []string{orphan.ID, note.ID}); got != 2 {
		t.Fatalf("note tombstones = %d, want 2", got)
	}

	q, args := tombstoneInsert(testUserID, TombstoneCard, 1, cardIDs(cards))
	rows, err := c.pool.Query(ctx, "EXPLAIN "+q, args...)
	if err != nil {
		t.Fatalf("explain tombstone insert: %v", err)
	}
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "Task Count: 1") || strings.Contains(text, "INSERT ... SELECT") {
		t.Fatalf("tombstone insert is not router-planned:\n%s", text)
	}
}
