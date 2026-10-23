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
	descriptors := []Field{{Name: "Front", Language: "es"}, {Name: "Back", Language: "en"}}

	got := newSearchDoc(c, n, d, descriptors)

	if got.CardID != cardID || got.NoteID != "n1" || got.DeckID != "d1" {
		t.Errorf("ids mismatch: %+v", got)
	}
	if got.DeckPath != "Spanish::Verbs" {
		t.Errorf("DeckPath = %q, want Spanish::Verbs", got.DeckPath)
	}
	if !got.Suspended {
		t.Error("Suspended = false, want true (queue -1)")
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
	// lang_text buckets each field's value under its descriptor's language tag.
	if got.LangText["es"] != "hola" || got.LangText["en"] != "hello" {
		t.Errorf("LangText = %v, want es=hola en=hello", got.LangText)
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

	got := newSearchDoc(c, n, d, nil)
	if got.Ivl != 0 {
		t.Errorf("Ivl = %d, want 0 for unscheduled card", got.Ivl)
	}
	if got.Suspended {
		t.Error("Suspended = true, want false for queue 0")
	}
}

func TestNewSearchDocBuriedIsNotSuspended(t *testing.T) {
	c := &Card{ID: ids.NewUUID(), State: StateReview, Queue: -2, USN: 1}
	got := newSearchDoc(c, &Note{ID: "n", USN: 1}, &Deck{ID: "d", USN: 1}, nil)
	if got.Suspended {
		t.Error("Suspended = true, want false for a buried card (queue -2)")
	}
}

func TestGroupFieldsByLang(t *testing.T) {
	tests := []struct {
		name        string
		fields      map[string]string
		descriptors []Field
		want        map[string]string
	}{
		{
			name:        "untagged fields produce no buckets",
			fields:      map[string]string{"Front": "hello", "Back": "world"},
			descriptors: []Field{{Name: "Front"}, {Name: "Back"}},
			want:        nil,
		},
		{
			name:   "per-language bucketing, one field each",
			fields: map[string]string{"Hanzi": "你好", "Reading": "nǐ hǎo", "Gloss": "hello"},
			descriptors: []Field{
				{Name: "Hanzi", Language: "zh"},
				{Name: "Reading"}, // untagged → agnostic only
				{Name: "Gloss", Language: "en"},
			},
			want: map[string]string{"zh": "你好", "en": "hello"},
		},
		{
			name:   "multiple fields same language join in schema order",
			fields: map[string]string{"A": "первый", "B": "второй"},
			descriptors: []Field{
				{Name: "A", Language: "ru"},
				{Name: "B", Language: "ru"},
			},
			want: map[string]string{"ru": "первый второй"},
		},
		{
			name:        "empty values are skipped",
			fields:      map[string]string{"Front": "", "Back": "text"},
			descriptors: []Field{{Name: "Front", Language: "en"}, {Name: "Back", Language: "en"}},
			want:        map[string]string{"en": "text"},
		},
		{
			name:        "no descriptors yields nil",
			fields:      map[string]string{"Front": "x"},
			descriptors: nil,
			want:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupFieldsByLang(tt.fields, tt.descriptors)
			if len(got) != len(tt.want) {
				t.Fatalf("groupFieldsByLang() = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("bucket %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
