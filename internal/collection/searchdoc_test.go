package collection

import (
	"testing"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

func TestNewSearchDoc(t *testing.T) {
	last := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	due := last.Add(21 * 24 * time.Hour)
	cardID := ids.NewUUID()

	c := &Card{
		ID:          cardID,
		UserID:      "u1",
		ProfileID:   "p1",
		NoteID:      "n1",
		DeckID:      "d1",
		TemplateOrd: 2,
		State:       StateReview,
		Queue:       -1, // suspended
		Due:         &due,
		LastReview:  &last,
		Reps:        9,
		Lapses:      3,
		USN:         5,
	}
	n := &Note{
		ID:     "n1",
		Fields: map[string]string{"Front": "hola", "Back": "hello"},
		Tags:   []string{"spanish", "greeting"},
		Flags:  2,
		USN:    8,
	}
	d := &Deck{ID: "d1", Path: "Spanish::Verbs", USN: 3}

	got := newSearchDoc(c, n, d)

	if got.CardID != cardID || got.NoteID != "n1" || got.DeckID != "d1" {
		t.Errorf("ids mismatch: %+v", got)
	}
	if got.DeckPath != "Spanish::Verbs" {
		t.Errorf("DeckPath = %q, want Spanish::Verbs", got.DeckPath)
	}
	if !got.Suspended {
		t.Error("Suspended = false, want true (queue<0)")
	}
	if got.Ivl != 21 {
		t.Errorf("Ivl = %d, want 21 (due-last_review days)", got.Ivl)
	}
	if got.Flags != 2 {
		t.Errorf("Flags = %d, want 2 (from note)", got.Flags)
	}
	// version = max(card.usn, note.usn, deck.usn) = max(5,8,3) = 8
	if got.Version != 8 {
		t.Errorf("Version = %d, want 8 (max of card/note/deck usn)", got.Version)
	}
	// fields_text joins all field values (deterministic: sorted by field name).
	if got.FieldsText != "hello hola" {
		t.Errorf("FieldsText = %q, want %q", got.FieldsText, "hello hola")
	}
	// added_at derived from the time-sortable UUIDv7 id, ~now.
	if time.Since(got.AddedAt) > time.Minute || got.AddedAt.After(time.Now()) {
		t.Errorf("AddedAt = %v, want ~now (from UUIDv7)", got.AddedAt)
	}
}

func TestNewSearchDocNoSchedule(t *testing.T) {
	// New card: no due / last_review → ivl 0, not suspended.
	c := &Card{ID: ids.NewUUID(), State: StateNew, Queue: 0, USN: 1}
	n := &Note{ID: "n", Fields: map[string]string{}, USN: 1}
	d := &Deck{ID: "d", Path: "Default", USN: 1}

	got := newSearchDoc(c, n, d)
	if got.Ivl != 0 {
		t.Errorf("Ivl = %d, want 0 for unscheduled card", got.Ivl)
	}
	if got.Suspended {
		t.Error("Suspended = true, want false for queue 0")
	}
}
