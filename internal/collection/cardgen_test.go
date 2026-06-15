package collection

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func mustTime() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

// fakeIDs returns a deterministic id generator: card-1, card-2, ...
func fakeIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("card-%d", n)
	}
}

func basicType() *NoteType {
	return &NoteType{
		ID:     "nt-basic",
		UserID: "u1",
		CardTemplates: []CardTemplate{
			{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"},
		},
	}
}

func reversedType() *NoteType {
	return &NoteType{
		ID:     "nt-rev",
		UserID: "u1",
		CardTemplates: []CardTemplate{
			{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"},
			{Name: "Card 2", Front: "{{Back}}", Back: "{{Front}}"},
		},
	}
}

func clozeType() *NoteType {
	return &NoteType{
		ID:     "nt-cloze",
		UserID: "u1",
		CardTemplates: []CardTemplate{
			{Name: "Cloze", Front: "{{cloze:Text}}", Back: "{{cloze:Text}}"},
		},
	}
}

func ordsOf(cards []Card) []int {
	out := make([]int, len(cards))
	for i, c := range cards {
		out[i] = c.TemplateOrd
	}
	sort.Ints(out)
	return out
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGenerateCardDiff_Generation(t *testing.T) {
	tests := []struct {
		name       string
		nt         *NoteType
		fields     map[string]string
		existing   []Card
		wantCreate []int
		wantDelete []int
	}{
		{
			name:       "basic note type makes one card",
			nt:         basicType(),
			fields:     map[string]string{"Front": "q", "Back": "a"},
			wantCreate: []int{0},
		},
		{
			name:       "reversed makes two cards when both fronts non-empty",
			nt:         reversedType(),
			fields:     map[string]string{"Front": "q", "Back": "a"},
			wantCreate: []int{0, 1},
		},
		{
			name:       "reversed with empty reverse-front makes one card",
			nt:         reversedType(),
			fields:     map[string]string{"Front": "q", "Back": ""},
			wantCreate: []int{0},
		},
		{
			name:       "cloze with c1 and c2 makes ordinals 0 and 1",
			nt:         clozeType(),
			fields:     map[string]string{"Text": "{{c1::a}} and {{c2::b}}"},
			wantCreate: []int{0, 1},
		},
		{
			name:       "cloze with gap c1,c3 makes ordinals 0 and 2",
			nt:         clozeType(),
			fields:     map[string]string{"Text": "{{c1::a}} {{c3::c}}"},
			wantCreate: []int{0, 2},
		},
		{
			name:       "no-op when existing matches desired",
			nt:         reversedType(),
			fields:     map[string]string{"Front": "q", "Back": "a"},
			existing:   []Card{{ID: "x0", TemplateOrd: 0}, {ID: "x1", TemplateOrd: 1}},
			wantCreate: nil,
			wantDelete: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note := &Note{ID: "n1", UserID: "u1", ProfileID: "p1", Fields: tt.fields}
			diff, err := GenerateCardDiff(tt.nt, note, tt.existing, "deck1", fakeIDs())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := ordsOf(diff.ToCreate); !eqInts(got, tt.wantCreate) {
				t.Errorf("ToCreate ords = %v, want %v", got, tt.wantCreate)
			}
			if got := ordsOf(diff.ToDelete); !eqInts(got, tt.wantDelete) {
				t.Errorf("ToDelete ords = %v, want %v", got, tt.wantDelete)
			}
		})
	}
}

func TestGenerateCardDiff_NewCardDefaults(t *testing.T) {
	note := &Note{ID: "n1", UserID: "u1", ProfileID: "p1", Fields: map[string]string{"Front": "q", "Back": "a"}}
	diff, err := GenerateCardDiff(basicType(), note, nil, "deck1", fakeIDs())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.ToCreate) != 1 {
		t.Fatalf("want 1 new card, got %d", len(diff.ToCreate))
	}
	c := diff.ToCreate[0]
	if c.ID != "card-1" {
		t.Errorf("id = %q, want card-1", c.ID)
	}
	if c.NoteID != "n1" || c.UserID != "u1" || c.ProfileID != "p1" || c.DeckID != "deck1" {
		t.Errorf("ownership wrong: %+v", c)
	}
	if c.State != StateNew || c.Queue != 0 {
		t.Errorf("state/queue = %v/%d, want StateNew/0", c.State, c.Queue)
	}
	if c.Due != nil || c.Stability != nil || c.Difficulty != nil || c.LastReview != nil {
		t.Errorf("new card must have nil scheduling fields: %+v", c)
	}
	if c.Reps != 0 || c.Lapses != 0 {
		t.Errorf("reps/lapses must be zero: %d/%d", c.Reps, c.Lapses)
	}
	if c.USN != 0 || !c.Mtime.IsZero() {
		t.Errorf("usn/mtime must be left unset for the service to stamp: %d/%v", c.USN, c.Mtime)
	}
}

func TestGenerateCardDiff_AddClozePreservesExisting(t *testing.T) {
	// Existing cloze cards for ord 0 and 1 with real scheduling state; adding c3
	// (ord 2) must create only the new card and leave the others untouched.
	due := mustTime()
	stab := 12.5
	existing := []Card{
		{ID: "c0", TemplateOrd: 0, State: StateReview, Queue: 0, Due: &due, Stability: &stab, Reps: 7, Lapses: 1},
		{ID: "c1", TemplateOrd: 1, State: StateLearning, Queue: 0, Reps: 2},
	}
	note := &Note{ID: "n1", UserID: "u1", Fields: map[string]string{"Text": "{{c1::a}} {{c2::b}} {{c3::c}}"}}

	diff, err := GenerateCardDiff(clozeType(), note, existing, "deck1", fakeIDs())
	if err != nil {
		t.Fatal(err)
	}
	if got := ordsOf(diff.ToCreate); !eqInts(got, []int{2}) {
		t.Fatalf("ToCreate ords = %v, want [2]", got)
	}
	if len(diff.ToDelete) != 0 {
		t.Fatalf("ToDelete = %v, want empty", ordsOf(diff.ToDelete))
	}
	// The diff must not contain (and therefore cannot mutate) the kept cards.
	for _, c := range diff.ToCreate {
		if c.TemplateOrd == 0 || c.TemplateOrd == 1 {
			t.Fatalf("kept ordinal %d wrongly re-created", c.TemplateOrd)
		}
	}
}

func TestGenerateCardDiff_RemoveClozePreservesOthers(t *testing.T) {
	due := mustTime()
	existing := []Card{
		{ID: "c0", TemplateOrd: 0, State: StateReview, Due: &due, Reps: 9},
		{ID: "c1", TemplateOrd: 1, State: StateReview, Reps: 3},
		{ID: "c2", TemplateOrd: 2, State: StateNew},
	}
	// Note now only has c1 and c2 -> ordinals 0 and 1; ordinal 2 (c3) is dropped.
	note := &Note{ID: "n1", UserID: "u1", Fields: map[string]string{"Text": "{{c1::a}} {{c2::b}}"}}

	diff, err := GenerateCardDiff(clozeType(), note, existing, "deck1", fakeIDs())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.ToCreate) != 0 {
		t.Fatalf("ToCreate = %v, want empty", ordsOf(diff.ToCreate))
	}
	if got := ordsOf(diff.ToDelete); !eqInts(got, []int{2}) {
		t.Fatalf("ToDelete ords = %v, want [2]", got)
	}
	if diff.ToDelete[0].ID != "c2" {
		t.Errorf("deleted card id = %q, want c2", diff.ToDelete[0].ID)
	}
}

func TestNoteType_IsCloze(t *testing.T) {
	if !clozeType().IsCloze() {
		t.Error("clozeType should be cloze")
	}
	if basicType().IsCloze() {
		t.Error("basicType should not be cloze")
	}
	// Tolerant of inner whitespace.
	nt := &NoteType{CardTemplates: []CardTemplate{{Front: "{{ cloze:Text }}"}}}
	if !nt.IsCloze() {
		t.Error("whitespace cloze tag should be detected")
	}
}
