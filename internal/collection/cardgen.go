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

// CardDiff is the set of card mutations needed to make a note's cards exactly
// match the cards its note type + fields should produce. Cards for ordinals
// that already exist and are still wanted are absent from both lists — their
// scheduling state is left untouched.
type CardDiff struct {
	ToCreate []Card // new cards with FSRS defaults (usn/mtime unset — service stamps)
	ToDelete []Card // existing cards whose ordinal is no longer produced
}

// GenerateCardDiff computes the create/delete card set for a note.
//
// Card generation is diff-based and never resets existing cards:
//   - Cloze note types produce one card per distinct {{cN::}} index in the
//     note's fields; the card's TemplateOrd is N-1 (cloze indices are 1-based).
//   - Standard note types produce one card per template whose FRONT renders to
//     non-empty content for this note (Anki-style conditional generation), so a
//     Basic+Reversed note with an empty reverse field yields a single card.
//
// New cards get id from newID() and inherit user/profile from the note and the
// given deckID, with new-card FSRS defaults (State=StateNew, Queue=0, nil
// scheduling, zero reps/lapses). USN and Mtime are left zero for the service to
// stamp inside the write transaction.
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

// maxTemplateOrd bounds a card's template ordinal to the cards.template_ord
// SMALLINT range. Cloze indices come from user-authored note text, so an absurd
// {{cN::}} index is rejected here rather than silently wrapping on the int16
// write (which would corrupt or collide template ordinals).
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
