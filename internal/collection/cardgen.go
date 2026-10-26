package collection

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mindmesh/mindmesh/pkg/template"
)

// clozeTagRe detects a {{cloze:...}} reference (tolerant of inner whitespace),
// used to classify a note type as cloze vs standard.
var clozeTagRe = regexp.MustCompile(`\{\{\s*cloze:`)

func containsCloze(s string) bool { return clozeTagRe.MatchString(s) }

// CardDiff omits cards that exist and are still wanted, so their scheduling state survives.
type CardDiff struct {
	ToCreate []Card // new cards with FSRS defaults (usn/mtime unset — service stamps)
	ToDelete []Card // existing cards whose ordinal is no longer produced
}

// GenerateCardDiff yields one card per cloze index (ord N-1), or per template whose front renders
// non-empty (Anki-style). USN and Mtime are left for the write transaction to stamp.
func GenerateCardDiff(nt *NoteType, note *Note, existing []Card, deckID string, newID func() string) (CardDiff, error) {
	desired, err := desiredOrdinals(nt, note)
	if err != nil {
		return CardDiff{}, err
	}

	byOrd := make(map[int]Card, len(existing))
	for _, c := range existing {
		byOrd[c.TemplateOrd] = c
	}

	var diff CardDiff
	for ord := range desired {
		if _, ok := byOrd[ord]; !ok {
			diff.ToCreate = append(diff.ToCreate, Card{
				ID:          newID(),
				UserID:      note.UserID,
				ProfileID:   note.ProfileID,
				NoteID:      note.ID,
				DeckID:      deckID,
				TemplateOrd: ord,
				State:       StateNew,
				Queue:       0,
			})
		}
	}
	for _, c := range existing {
		if !desired[c.TemplateOrd] {
			diff.ToDelete = append(diff.ToDelete, c)
		}
	}

	// Deterministic ordering for reproducible writes and tests.
	sort.Slice(diff.ToCreate, func(i, j int) bool { return diff.ToCreate[i].TemplateOrd < diff.ToCreate[j].TemplateOrd })
	sort.Slice(diff.ToDelete, func(i, j int) bool { return diff.ToDelete[i].TemplateOrd < diff.ToDelete[j].TemplateOrd })
	return diff, nil
}

// maxTemplateOrd is the SMALLINT bound: a user-typed {{c99999::}} must fail, not wrap and collide.
const maxTemplateOrd = 32767

// desiredOrdinals returns the set of 0-based card ordinals the note should have.
func desiredOrdinals(nt *NoteType, note *Note) (map[int]bool, error) {
	desired := make(map[int]bool)
	if nt.IsCloze() {
		for _, n := range template.ClozeIndices(note.Fields) {
			ord := n - 1 // cloze indices are 1-based; ordinals are 0-based
			if ord < 0 || ord > maxTemplateOrd {
				continue // out-of-range cloze number; produces no card
			}
			desired[ord] = true
		}
		return desired, nil
	}
	for i, t := range nt.CardTemplates {
		front, err := template.Render(t.Front, note.Fields, template.Opts{})
		if err != nil {
			// A malformed template produces no card for that ordinal rather than
			// failing the whole note; the author sees the missing card.
			continue
		}
		if strings.TrimSpace(front) != "" {
			desired[i] = true
		}
	}
	return desired, nil
}
