// Package syncer serves the USN delta pull straight from Collection's and Scheduler's colocated
// tables, and the push path that resolves conflicts and forwards mutations to Collection.
package syncer

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

// ─── Read-only projections of Collection's entities ─────────────────────────

// Field mirrors collection.Field — a single field descriptor within a NoteType.
type Field struct {
	Name     string `json:"name"`
	Sticky   bool   `json:"sticky"`
	RTL      bool   `json:"rtl"`
	Language string `json:"language,omitempty"`
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
	RolloverHour     int32
	Timezone         string
	MaxIntervalDays  int32
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

// Cursor is a (usn, id) keyset position: one transaction's rows share a usn, so usn alone can't
// resume mid-group. An empty ID means after every row with this usn.
type Cursor struct {
	USN  int64  `json:"u"`
	Type string `json:"t,omitempty"` // tombstones only: their key is (type, id)
	ID   string `json:"i,omitempty"`
}

// Cursors maps entity type -> its cursor; a missing type starts at the request's since_usn.
type Cursors map[string]Cursor

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
	NextCursors   Cursors // nil on the last page
}

// Repository is Sync's tenant-scoped storage: delta-pull reads plus op_id idempotency bookkeeping.
type Repository interface {
	// LatestUSN is 0 for a user who has never written.
	LatestUSN(ctx context.Context, userID string) (int64, error)

	ListNoteTypesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]NoteType, error)
	ListNotesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Note, error)
	ListCardsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Card, error)
	ListDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Deck, error)
	ListDeckConfigsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]DeckConfig, error)
	ListFilteredDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]FilteredDeck, error)
	ListTagsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tag, error)
	ListTombstonesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tombstone, error)

	// GetAppliedOp returns (nil, nil) for an op_id never applied.
	GetAppliedOp(ctx context.Context, userID, opID string) (*AppliedOp, error)
	// RecordAppliedOp must not get a codes.Aborted failure, or a retry would replay the race, not re-resolve it.
	RecordAppliedOp(ctx context.Context, userID, opID string, outcome ApplyOutcome, newUSN int64) error
}

// ─── Apply (push) path: hand-kept mirrors of sync.proto's Apply messages ────

// MutationType mirrors sync.v1.MutationType; zero is invalid. There is no card variant, which is
// how "scheduler always wins" is enforced.
type MutationType int

const (
	MutationNoteTypeUpdate MutationType = iota + 1
	MutationNoteTypeDelete
	MutationNoteUpdate
	MutationNoteDelete
	MutationDeckUpdate
	MutationDeckDelete
	MutationDeckConfigUpdate
	MutationDeckConfigDelete
	MutationFilteredDeckDelete
)

// ApplyOutcome reports how a single Mutation was resolved.
type ApplyOutcome int16

const (
	// OutcomeApplied: fast-forwarded, or a delete of an already-deleted entity.
	OutcomeApplied ApplyOutcome = iota + 1
	// OutcomeMerged: conflicting update won by the client via LWW.
	OutcomeMerged
	// OutcomeServerWon: LWW lost, or a delete raced an update; the mutation was discarded.
	OutcomeServerWon
	// OutcomeReplayed: op_id already applied; no write was issued.
	OutcomeReplayed
	// OutcomeFailed: not applied, e.g. a write raced between Sync's read and write.
	OutcomeFailed
)

// NoteTypePayload carries the fields of a NOTE_TYPE_UPDATE mutation.
type NoteTypePayload struct {
	Name          string
	Fields        []Field
	CardTemplates []CardTemplate
	CSS           string
}

// NotePayload carries the fields of a NOTE_UPDATE mutation.
type NotePayload struct {
	Fields map[string]string
	Tags   []string
	Flags  int32
}

// DeckPayload carries the fields of a DECK_UPDATE mutation.
type DeckPayload struct {
	Name     string
	ParentID string
	ConfigID string
}

// DeckConfigPayload carries the fields of a DECK_CONFIG_UPDATE mutation.
type DeckConfigPayload struct {
	Name             string
	NewPerDay        int32
	RevPerDay        int32
	DesiredRetention float64
	FSRSWeights      []float64
	RolloverHour     int32
	Timezone         string
	MaxIntervalDays  int32
}

// Mutation is one client change; exactly one payload matches Type, and deletes carry none.
type Mutation struct {
	OpID        string // client-generated; replays are no-ops
	Type        MutationType
	EntityID    string
	BaseUSN     int64
	ClientMtime time.Time // LWW tie-breaking on conflict only

	NoteType   *NoteTypePayload   // NOTE_TYPE_UPDATE only
	Note       *NotePayload       // NOTE_UPDATE only
	Deck       *DeckPayload       // DECK_UPDATE only
	DeckConfig *DeckConfigPayload // DECK_CONFIG_UPDATE only
}

// ApplyResult carries at most one entity, in its current state after the op; none for deletes and failures.
type ApplyResult struct {
	OpID    string
	Outcome ApplyOutcome
	Reason  string
	NewUSN  int64

	NoteType     *NoteType
	Note         *Note
	Deck         *Deck
	DeckConfig   *DeckConfig
	FilteredDeck *FilteredDeck
}

// AppliedOp is a sync_applied_ops row: outcome and usn only, so a replay re-fetches the entity live.
type AppliedOp struct {
	OpID      string
	Outcome   ApplyOutcome
	NewUSN    int64
	AppliedAt time.Time
}
