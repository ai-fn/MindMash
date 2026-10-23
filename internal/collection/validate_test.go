package collection

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
)

// unreachableRepo panics on any call, so a case passes only if validation rejects the input first.
type unreachableRepo struct{ Repository }

func TestServiceRejectsInvalidInput(t *testing.T) {
	const (
		u   = "0190f3a8-7b2c-7d4e-9f00-123456789abc"
		bad = "not-a-uuid"
	)
	ctx := context.Background()
	s := NewService(unreachableRepo{}, zap.NewNop())

	okFields := []Field{{Name: "Front"}}
	okTemplates := []CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Front}}"}}
	tooManyIDs := make([]string, validate.MaxBulkIDs+1)
	for i := range tooManyIDs {
		tooManyIDs[i] = u
	}
	long := func(n int) string { return strings.Repeat("a", n) }

	tests := []struct {
		name string
		call func() error
	}{
		{"GetNoteType id", func() error { _, err := s.GetNoteType(ctx, u, bad); return err }},
		{"ListNoteTypes token", func() error { _, _, err := s.ListNoteTypes(ctx, u, Page{Token: bad}); return err }},
		{"CreateNoteType blank name", func() error {
			_, err := s.CreateNoteType(ctx, u, u, " ", "", okFields, okTemplates)
			return err
		}},
		{"CreateNoteType long name", func() error {
			_, err := s.CreateNoteType(ctx, u, u, long(maxNameRunes+1), "", okFields, okTemplates)
			return err
		}},
		{"CreateNoteType long css", func() error {
			_, err := s.CreateNoteType(ctx, u, u, "Basic", long(maxTemplateBytes+1), okFields, okTemplates)
			return err
		}},
		{"CreateNoteType blank field name", func() error {
			_, err := s.CreateNoteType(ctx, u, u, "Basic", "", []Field{{Name: ""}}, okTemplates)
			return err
		}},
		{"CreateNoteType too many fields", func() error {
			_, err := s.CreateNoteType(ctx, u, u, "Basic", "", make([]Field, maxFields+1), okTemplates)
			return err
		}},
		{"CreateNoteType long template", func() error {
			_, err := s.CreateNoteType(ctx, u, u, "Basic", "", okFields,
				[]CardTemplate{{Name: "Card 1", Front: long(maxTemplateBytes + 1)}})
			return err
		}},
		{"CreateNoteType too many templates", func() error {
			_, err := s.CreateNoteType(ctx, u, u, "Basic", "", okFields, make([]CardTemplate, maxTemplates+1))
			return err
		}},
		{"UpdateNoteType id", func() error {
			_, err := s.UpdateNoteType(ctx, u, bad, 1, "Basic", "", okFields, okTemplates)
			return err
		}},
		{"UpdateNoteType blank name", func() error {
			_, err := s.UpdateNoteType(ctx, u, u, 1, "", "", okFields, okTemplates)
			return err
		}},
		{"DeleteNoteType id", func() error { return s.DeleteNoteType(ctx, u, bad, 1) }},

		{"GetNote id", func() error { _, err := s.GetNote(ctx, u, bad); return err }},
		{"CreateNote note type id", func() error {
			_, err := s.CreateNote(ctx, u, u, bad, u, map[string]string{"Front": "x"}, nil, 0)
			return err
		}},
		{"CreateNote deck id", func() error {
			_, err := s.CreateNote(ctx, u, u, u, bad, map[string]string{"Front": "x"}, nil, 0)
			return err
		}},
		{"CreateNote large fields", func() error {
			_, err := s.CreateNote(ctx, u, u, u, u, map[string]string{"Front": long(maxNoteBytes + 1)}, nil, 0)
			return err
		}},
		{"CreateNote too many tags", func() error {
			_, err := s.CreateNote(ctx, u, u, u, u, nil, make([]string, maxTags+1), 0)
			return err
		}},
		{"CreateNote tag with space", func() error {
			_, err := s.CreateNote(ctx, u, u, u, u, nil, []string{"a b"}, 0)
			return err
		}},
		{"UpdateNote id", func() error { _, err := s.UpdateNote(ctx, u, bad, 1, nil, nil, 0); return err }},
		{"UpdateNote nul in field", func() error {
			_, err := s.UpdateNote(ctx, u, u, 1, map[string]string{"Front": "a\x00"}, nil, 0)
			return err
		}},
		{"CreateNote negative flags", func() error {
			_, err := s.CreateNote(ctx, u, u, u, u, nil, nil, -1)
			return err
		}},
		{"UpdateNote flags over smallint", func() error { _, err := s.UpdateNote(ctx, u, u, 1, nil, nil, 40000); return err }},
		{"DeleteNote id", func() error { return s.DeleteNote(ctx, u, bad, 1) }},

		{"RenderCard id", func() error { _, _, _, err := s.RenderCard(ctx, u, bad); return err }},
		{"RenderCards element", func() error { _, err := s.RenderCards(ctx, u, []string{u, bad}); return err }},
		{"RenderCards cap", func() error { _, err := s.RenderCards(ctx, u, tooManyIDs); return err }},
		{"GetCardsByIDs element", func() error { _, err := s.GetCardsByIDs(ctx, u, []string{u, bad}); return err }},
		{"GetCardsByIDs cap", func() error { _, err := s.GetCardsByIDs(ctx, u, tooManyIDs); return err }},
		{"ListCardIDs token", func() error { _, _, err := s.ListCardIDs(ctx, u, Page{Token: bad}); return err }},
		{"GetSearchDocs card", func() error {
			_, _, err := s.GetSearchDocs(ctx, u, SearchDocSelector{CardIDs: []string{bad}}, Page{})
			return err
		}},
		{"GetSearchDocs note", func() error {
			_, _, err := s.GetSearchDocs(ctx, u, SearchDocSelector{NoteIDs: []string{bad}}, Page{})
			return err
		}},
		{"GetSearchDocs deck cap", func() error {
			_, _, err := s.GetSearchDocs(ctx, u, SearchDocSelector{DeckIDs: tooManyIDs}, Page{})
			return err
		}},

		{"MoveCards ids", func() error { _, err := s.MoveCards(ctx, u, []string{bad}, u); return err }},
		{"MoveCards cap", func() error { _, err := s.MoveCards(ctx, u, tooManyIDs, u); return err }},
		{"MoveCards deck", func() error { _, err := s.MoveCards(ctx, u, []string{u}, bad); return err }},
		{"SuspendCards ids", func() error { _, err := s.SuspendCards(ctx, u, []string{bad}); return err }},
		{"DeleteCards cap", func() error { _, err := s.DeleteCards(ctx, u, tooManyIDs); return err }},
		{"AddTagToCards ids", func() error { _, err := s.AddTagToCards(ctx, u, []string{bad}, "t"); return err }},
		{"AddTagToCards blank tag", func() error { _, err := s.AddTagToCards(ctx, u, []string{u}, ""); return err }},
		{"RemoveTagFromCards long tag", func() error {
			_, err := s.RemoveTagFromCards(ctx, u, []string{u}, long(maxTagRunes+1))
			return err
		}},
		{"SetFlagOnCards flag", func() error { _, err := s.SetFlagOnCards(ctx, u, []string{u}, 40000); return err }},
		{"SetFlagOnCards ids", func() error { _, err := s.SetFlagOnCards(ctx, u, []string{bad}, 1); return err }},

		{"GetDeck id", func() error { _, err := s.GetDeck(ctx, u, bad); return err }},
		{"ListDecks token", func() error { _, _, err := s.ListDecks(ctx, u, Page{Token: bad}); return err }},
		{"CreateDeck blank name", func() error { _, err := s.CreateDeck(ctx, u, u, "", "", ""); return err }},
		{"CreateDeck parent", func() error { _, err := s.CreateDeck(ctx, u, u, "D", bad, ""); return err }},
		{"CreateDeck config", func() error { _, err := s.CreateDeck(ctx, u, u, "D", "", bad); return err }},
		{"UpdateDeck id", func() error { _, err := s.UpdateDeck(ctx, u, bad, 1, "D", "", ""); return err }},
		{"UpdateDeck long name", func() error {
			_, err := s.UpdateDeck(ctx, u, u, 1, long(maxNameRunes+1), "", "")
			return err
		}},
		{"UpdateDeck parent", func() error { _, err := s.UpdateDeck(ctx, u, u, 1, "D", bad, ""); return err }},
		{"DeleteDeck id", func() error { return s.DeleteDeck(ctx, u, bad, 1) }},

		{"GetDeckConfig id", func() error { _, err := s.GetDeckConfig(ctx, u, bad); return err }},
		{"ListDeckConfigs token", func() error { _, _, err := s.ListDeckConfigs(ctx, u, Page{Token: bad}); return err }},
		{"CreateDeckConfig blank name", func() error { _, err := s.CreateDeckConfig(ctx, u, u, DeckConfig{}); return err }},
		{"CreateDeckConfig long timezone", func() error {
			_, err := s.CreateDeckConfig(ctx, u, u, DeckConfig{Name: "C", Timezone: long(maxTimezoneBytes + 1)})
			return err
		}},
		{"CreateDeckConfig too many weights", func() error {
			_, err := s.CreateDeckConfig(ctx, u, u, DeckConfig{Name: "C", FSRSWeights: make([]float64, maxConfigListLen+1)})
			return err
		}},
		{"UpdateDeckConfig id", func() error { _, err := s.UpdateDeckConfig(ctx, u, bad, 1, DeckConfig{Name: "C"}); return err }},
		{"UpdateDeckConfig too many steps", func() error {
			_, err := s.UpdateDeckConfig(ctx, u, u, 1, DeckConfig{Name: "C", LearningSteps: make([]int32, maxConfigListLen+1)})
			return err
		}},
		{"DeleteDeckConfig id", func() error { return s.DeleteDeckConfig(ctx, u, bad, 1) }},

		{"CreateFilteredDeck blank name", func() error { _, err := s.CreateFilteredDeck(ctx, u, u, "", "q"); return err }},
		{"CreateFilteredDeck long query", func() error {
			_, err := s.CreateFilteredDeck(ctx, u, u, "F", long(validate.MaxQueryBytes+1))
			return err
		}},
		{"GetFilteredDeck id", func() error { _, err := s.GetFilteredDeck(ctx, u, bad); return err }},
		{"ListFilteredDecks token", func() error { _, _, err := s.ListFilteredDecks(ctx, u, Page{Token: bad}); return err }},
		{"DeleteFilteredDeck id", func() error { return s.DeleteFilteredDeck(ctx, u, bad) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("reached the repository: input was not validated")
				}
			}()
			if err := tt.call(); !platformerrors.IsInvalidArgument(err) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
		})
	}
}
