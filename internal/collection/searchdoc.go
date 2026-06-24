package collection

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SearchDoc is the denormalized card+note+deck projection the Search service
// indexes. It is assembled server-side (shard-local) by GetSearchDocs so the
// indexer never has to join across Collection's entities itself. Version is the
// max of the three source USNs so the indexer can use it for external_gte
// versioning — any later edit to the card, its note, or its deck bumps it.
type SearchDoc struct {
	CardID      string
	UserID      string
	ProfileID   string
	NoteID      string
	DeckID      string
	DeckPath    string
	TemplateOrd int
	State       CardState
	Suspended   bool
	Due         *time.Time
	Ivl         int // current scheduled interval in days (due - last_review)
	Reps        int
	Lapses      int
	Flags       int32 // colour-flag bitmask, carried from the owning note
	Tags        []string
	FieldsText  string // all note field values, for full-text search
	AddedAt     time.Time
	Version     int64
}

// SearchDocSelector chooses which cards GetSearchDocs resolves. Exactly one
// field is non-empty per call: CardIDs selects those cards; NoteIDs selects all
// cards of those notes; DeckIDs selects every card in those decks and their
// descendant decks (subtree, so a deck rename reindexes the whole subtree).
type SearchDocSelector struct {
	CardIDs []string
	NoteIDs []string
	DeckIDs []string
}

// newSearchDoc assembles a SearchDoc from a card and its owning note and deck.
func newSearchDoc(c *Card, n *Note, d *Deck) SearchDoc {
	return SearchDoc{
		CardID:      c.ID,
		UserID:      c.UserID,
		ProfileID:   c.ProfileID,
		NoteID:      c.NoteID,
		DeckID:      c.DeckID,
		DeckPath:    d.Path,
		TemplateOrd: c.TemplateOrd,
		State:       c.State,
		Suspended:   c.Queue < 0,
		Due:         c.Due,
		Ivl:         intervalDays(c.Due, c.LastReview),
		Reps:        c.Reps,
		Lapses:      c.Lapses,
		Flags:       n.Flags,
		Tags:        n.Tags,
		FieldsText:  fieldsText(n.Fields),
		AddedAt:     addedAtFromID(c.ID),
		Version:     maxUSN(c.USN, n.USN, d.USN),
	}
}

// intervalDays is the scheduled interval in whole days between the last review
// and the next due time. Unscheduled cards (no due or no last_review) have ivl 0.
func intervalDays(due, lastReview *time.Time) int {
	if due == nil || lastReview == nil {
		return 0
	}
	return int(math.Round(due.Sub(*lastReview).Hours() / 24))
}

// fieldsText concatenates a note's field values into one searchable string,
// ordered by field name so the output is deterministic.
func fieldsText(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := fields[k]; v != "" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, " ")
}

// addedAtFromID extracts the creation time from a UUIDv7 id (the first 48 bits
// are a Unix-millisecond timestamp). Non-v7 ids yield a zero-ish time, which is
// acceptable since all entity ids are minted as UUIDv7 by platform/ids.
func addedAtFromID(id string) time.Time {
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}
	}
	ms := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 |
		int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	return time.UnixMilli(ms).UTC()
}

func maxUSN(usns ...int64) int64 {
	var m int64
	for _, u := range usns {
		if u > m {
			m = u
		}
	}
	return m
}
