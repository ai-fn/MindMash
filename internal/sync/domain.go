// Package sync implements the read-only USN delta-pull service: it reads the
// usn/mtime columns Collection (and Scheduler, for cards' scheduling columns)
// already stamp on every mutation, and assembles them into a per-user change
// feed so a user's open tabs/devices converge. Sync owns no tables of its own;
// it connects to the same Postgres/Citus cluster as Collection and Scheduler
// (see internal/scheduler/repo.go for the established precedent of one
// service reading another's colocated tables directly).
package sync

import (
	"context"
	"time"
)

// Entity types as they appear in the tombstones table and in change feed
// pagination cursors. Mirrors internal/collection's Tombstone* constants.
const (
	EntityNoteType     = "note_type"
	EntityNote         = "note"
	EntityCard         = "card"
	EntityDeck         = "deck"
	EntityDeckConfig   = "deck_config"
	EntityFilteredDeck = "filtered_deck"
	EntityTag          = "tag"
)

// ─── Read-only domain projections ───────────────────────────────────────────
// These mirror internal/collection's domain entities field-for-field; Sync
// never writes them, so only the fields a client needs to converge are kept.

// Field mirrors collection.Field — a single field descriptor within a NoteType.
type Field struct {
	Name   string `json:"name"`
	Sticky bool   `json:"sticky"`
	RTL    bool   `json:"rtl"`
}

// CardTemplate mirrors collection.CardTemplate.
type CardTemplate struct {
	Name  string `json:"name"`
	Front string `json:"front"`
	Back  string `json:"back"`
}

type NoteType struct {
	ID            string
	Name          string
	CSS           string
	Fields        []Field
	CardTemplates []CardTemplate
	USN           int64
	Mtime         time.Time
}

type Note struct {
	ID         string
	NoteTypeID string
	Fields     map[string]string
	Tags       []string
	Flags      int32
	USN        int64
	Mtime      time.Time
}

type Card struct {
	ID          string
	NoteID      string
	DeckID      string
	TemplateOrd int
	State       int
	Queue       int
	Due         *time.Time
	Stability   *float64
	Difficulty  *float64
	Reps        int
	Lapses      int
	LastReview  *time.Time
	USN         int64
	Mtime       time.Time
}

type Deck struct {
	ID       string
	ParentID *string
	Name     string
	Path     string
	ConfigID *string
	USN      int64
	Mtime    time.Time
}

type DeckConfig struct {
	ID               string
	Name             string
	NewPerDay        int32
	RevPerDay        int32
	DesiredRetention float64
	FSRSWeights      []float64
	LearningSteps    []int32
	RolloverHour     int32
	Timezone         string
	USN              int64
	Mtime            time.Time
}

type FilteredDeck struct {
	ID    string
	Name  string
	Query string
	USN   int64
	Mtime time.Time
}

type Tag struct {
	ID    string
	Name  string
	USN   int64
	Mtime time.Time
}

// Tombstone records a hard delete the client must remove locally.
type Tombstone struct {
	Type      string
	ID        string
	USN       int64
	DeletedAt time.Time
}

// Cursors maps entity type -> the last usn already returned for that type.
// An empty/nil Cursors means "start from the request's since_usn for every
// type" (i.e. the first page).
type Cursors map[string]int64

// ChangeSet is one page of the delta feed.
type ChangeSet struct {
	NoteTypes     []NoteType
	Notes         []Note
	Cards         []Card
	Decks         []Deck
	DeckConfigs   []DeckConfig
	FilteredDecks []FilteredDeck
	Tags          []Tag
	Deleted       []Tombstone
	// NextCursors is non-nil when at least one entity type had more rows than
	// the page limit; callers encode it into the response's next_page_token.
	NextCursors Cursors
}

// Repository defines the read-only storage operations Sync needs. Every
// method is tenant-scoped by userID, matching the convention in
// internal/collection and internal/scheduler.
type Repository interface {
	// LatestUSN returns the user's current monotonic usn counter (0 if the
	// user has never written anything yet).
	LatestUSN(ctx context.Context, userID string) (int64, error)

	ListNoteTypesSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]NoteType, error)
	ListNotesSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]Note, error)
	ListCardsSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]Card, error)
	ListDecksSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]Deck, error)
	ListDeckConfigsSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]DeckConfig, error)
	ListFilteredDecksSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]FilteredDeck, error)
	ListTagsSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]Tag, error)
	ListTombstonesSince(ctx context.Context, userID string, sinceUSN int64, limit int) ([]Tombstone, error)
}
