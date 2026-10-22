// Package sync implements the USN delta-pull (read) and apply (push) paths:
// it reads the usn/mtime columns Collection (and Scheduler, for cards'
// scheduling columns) already stamp on every mutation, and assembles them
// into a per-user change feed so a user's open tabs/devices converge; it also
// accepts client-submitted mutations via /sync/apply, resolves conflicts
// (fast-forward / last-write-wins / scheduler-always-wins), and forwards them
// to Collection's existing mutation RPCs. Sync owns one table of its own —
// sync_applied_ops, used for op_id idempotency on the push path (see
// migrations/sync/) — and otherwise connects to the same Postgres/Citus
// cluster as Collection and Scheduler (see internal/scheduler/repo.go for the
// established precedent of one service reading another's colocated tables
// directly).
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

// Cursor is the keyset position of the last row returned for one entity type.
// An empty ID means "after every row with this USN" (the first-page position).
// Rows written by one transaction share a USN, so the USN alone can't resume mid-group.
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
	// NextCursors is non-nil when at least one entity type had more rows than
	// the page limit; callers encode it into the response's next_page_token.
	NextCursors Cursors
}

// Repository defines the storage operations Sync needs: read-only delta-pull
// projections, plus push-path idempotency bookkeeping for /sync/apply. Every
// method is tenant-scoped by userID, matching the convention in
// internal/collection and internal/scheduler.
type Repository interface {
	// LatestUSN returns the user's current monotonic usn counter (0 if the
	// user has never written anything yet).
	LatestUSN(ctx context.Context, userID string) (int64, error)

	ListNoteTypesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]NoteType, error)
	ListNotesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Note, error)
	ListCardsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Card, error)
	ListDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Deck, error)
	ListDeckConfigsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]DeckConfig, error)
	ListFilteredDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]FilteredDeck, error)
	ListTagsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tag, error)
	ListTombstonesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tombstone, error)

	// GetAppliedOp returns the previously recorded outcome for (userID, opID),
	// or (nil, nil) if this op_id has never been applied — the push-path
	// idempotency check. A non-nil result means the mutation was already
	// processed and must not be re-applied to Collection.
	GetAppliedOp(ctx context.Context, userID, opID string) (*AppliedOp, error)
	// RecordAppliedOp persists the outcome of applying op_id so a future
	// replay short-circuits via GetAppliedOp. Per Global Constraint 7, a
	// codes.Aborted outcome from Collection must never reach this method.
	RecordAppliedOp(ctx context.Context, userID, opID string, outcome ApplyOutcome, newUSN int64) error
}

// ─── Apply (push) path domain types ─────────────────────────────────────────
// These are plain Go mirrors of api/sync/v1/sync.proto's Apply RPC messages
// (Mutation/ApplyRequest/ApplyResult/ApplyResponse) — not generated, but kept
// field-for-field in sync with the proto so handler.go's translation layer
// (a later task) is a straight copy.

// MutationType identifies which Collection entity/operation a Mutation
// targets. Mirrors sync.v1.MutationType 1:1, offset by one so the zero value
// (no MUTATION_TYPE_UNSPECIFIED equivalent here) is invalid rather than
// silently meaning "note type update". No card variant exists — Collection
// exposes no UpdateCard RPC, so "scheduler always wins" is enforced by this
// omission (see plan Global Constraint 4).
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
	// OutcomeApplied means the mutation was written to Collection as-is
	// (fast-forward: base_usn matched the entity's current usn), or a delete
	// against an already-deleted entity (idempotent no-op success).
	OutcomeApplied ApplyOutcome = iota + 1
	// OutcomeMerged means a conflicting update was resolved client-wins via
	// last-write-wins and applied.
	OutcomeMerged
	// OutcomeServerWon means a conflicting mutation was resolved server-wins
	// (LWW lost, or a delete raced against a concurrent update) and the
	// client's mutation was discarded.
	OutcomeServerWon
	// OutcomeReplayed means this op_id was already applied previously; the
	// response reflects the recorded outcome, and no write was issued.
	OutcomeReplayed
	// OutcomeFailed means the mutation could not be applied (e.g. Collection
	// returned codes.Aborted because a third write raced in between Sync's
	// read and write). Per Global Constraint 7, a Failed outcome is never
	// recorded in sync_applied_ops, so a retry of the same op_id re-resolves
	// against fresh state.
	OutcomeFailed
)

// NoteTypePayload carries the fields of a NOTE_TYPE_UPDATE mutation. Mirrors
// collection.v1.UpdateNoteTypeRequest's body (minus id/expected_usn, which
// live on Mutation itself).
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
	LearningSteps    []int32
	RolloverHour     int32
	Timezone         string
}

// Mutation is a single client-submitted change in an ApplyRequest. Exactly
// one payload field is set, matching Type; *_DELETE and
// FILTERED_DECK_DELETE mutations carry no payload.
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

// ApplyResult is the per-mutation outcome returned in an ApplyResponse. At
// most one entity field is set, reflecting the post-apply (or post-replay)
// state of the entity the mutation targeted; it is nil for deletes and for
// OutcomeFailed.
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

// AppliedOp is the row shape sync_applied_ops stores for op_id idempotency.
// Per plan Global Constraint 9, only the outcome/usn are cached — never the
// entity payload; a replay re-fetches the entity live from Collection.
type AppliedOp struct {
	OpID      string
	Outcome   ApplyOutcome
	NewUSN    int64
	AppliedAt time.Time
}
