package collection

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SearchDoc's Version is the max of the card, note and deck USNs, so any edit bumps its
// external_gte version.
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
	FieldsText  string // all note field values, for full-text search (language-agnostic)
	// LangText maps a language tag to its fields' text; untagged fields live only in FieldsText.
	LangText map[string]string
	AddedAt  time.Time
	Version  int64
}

// SearchDocSelector sets exactly one field; DeckIDs covers descendant decks too.
type SearchDocSelector struct {
	CardIDs []string
	NoteIDs []string
	DeckIDs []string
}

func (d *SearchDoc) textBytes() int {
	n := len(d.FieldsText)
	for _, v := range d.LangText {
		n += len(v)
	}
	return n
}

// newSearchDoc takes nil descriptors when the note type is unknown; only FieldsText is then set.
func newSearchDoc(c *Card, n *Note, d *Deck, descriptors []Field) SearchDoc {
	return SearchDoc{
		CardID:      c.ID,
		UserID:      c.UserID,
		ProfileID:   c.ProfileID,
		NoteID:      c.NoteID,
		DeckID:      c.DeckID,
		DeckPath:    d.Path,
		TemplateOrd: c.TemplateOrd,
		State:       c.State,
		Suspended:   c.Queue == suspendedQueue, // buried (-2/-3) is not suspended
		Due:         c.Due,
		Ivl:         intervalDays(c.Due, c.LastReview),
		Reps:        c.Reps,
		Lapses:      c.Lapses,
		Flags:       n.Flags,
		Tags:        n.Tags,
		FieldsText:  fieldsText(n.Fields),
		LangText:    groupFieldsByLang(n.Fields, descriptors),
		AddedAt:     addedAtFromID(c.ID),
		Version:     maxUSN(c.USN, n.USN, d.USN),
	}
}

// groupFieldsByLang joins values in field-name order for deterministic output; nil when nothing is
// tagged, so the common untagged note allocates nothing.
func groupFieldsByLang(fields map[string]string, descriptors []Field) map[string]string {
	lang := make(map[string][]string)
	// Iterate descriptors in schema order so field grouping is deterministic without
	// a separate sort, and only tagged fields contribute.
	for _, f := range descriptors {
		if f.Language == "" {
			continue
		}
		if v := fields[f.Name]; v != "" {
			lang[f.Language] = append(lang[f.Language], v)
		}
	}
	if len(lang) == 0 {
		return nil
	}
	out := make(map[string]string, len(lang))
	for code, vals := range lang {
		out[code] = strings.Join(vals, " ")
	}
	return out
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

// addedAtFromID reads the UUIDv7 millisecond timestamp; every id comes from platform/ids.
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
