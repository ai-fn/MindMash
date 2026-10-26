// Package collection owns note types, notes, cards, decks, deck configs, filtered decks and tags,
// all scoped by user_id and versioned with a per-user USN.
package collection

import (
	"context"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// CardState is the stored SMALLINT; the proto enum is offset by +1 in the handler.
type CardState int16

const (
	StateNew        CardState = 0
	StateLearning   CardState = 1
	StateReview     CardState = 2
	StateRelearning CardState = 3
)

// Tombstone entity types, written to the tombstones table on hard delete so the
// Sync service can propagate deletions to clients.
const (
	TombstoneNote         = "note"
	TombstoneCard         = "card"
	TombstoneDeck         = "deck"
	TombstoneNoteType     = "note_type"
	TombstoneTag          = "tag"
	TombstoneDeckConfig   = "deck_config"
	TombstoneFilteredDeck = "filtered_deck"
)

// ─── Domain entities ────────────────────────────────────────────────────────

// Field is one field descriptor within a NoteType (stored in the fields JSONB).
type Field struct {
	Name   string `json:"name"`
	Sticky bool   `json:"sticky"` // persist value across successive note creates
	RTL    bool   `json:"rtl"`    // render right-to-left
	// Language ("en", "ru", "zh") picks the search analyzer; empty means standard only.
	Language string `json:"language,omitempty"`
}

// CardTemplate is one card template within a NoteType (stored in the
// card_templates JSONB). Front/Back are Mustache-like templates (qfmt/afmt).
type CardTemplate struct {
	Name  string `json:"name"`
	Front string `json:"front"`
	Back  string `json:"back"`
}

// NoteType is the schema (fields), card templates, and CSS that notes are
// instantiated from.
type NoteType struct {
	ID            string
	UserID        string
	ProfileID     string
	Name          string
	Fields        []Field
	CardTemplates []CardTemplate
	CSS           string
	USN           int64
	Mtime         time.Time
}

// IsCloze reports whether this note type generates cloze cards. A note type is
// a cloze type iff any template references the {{cloze:...}} construct.
func (nt *NoteType) IsCloze() bool { return isCloze(nt.CardTemplates) }

func isCloze(templates []CardTemplate) bool {
	for _, t := range templates {
		if containsCloze(t.Front) || containsCloze(t.Back) {
			return true
		}
	}
	return false
}

// Note holds the field values for one unit of knowledge; it generates one or
// more cards via its note type's templates.
type Note struct {
	ID         string
	UserID     string
	ProfileID  string
	NoteTypeID string
	Fields     map[string]string
	Tags       []string
	Flags      int32 // colour-flag bitmask (Anki flags 1–4)
	USN        int64
	Mtime      time.Time
}

// Card's scheduling fields belong to Scheduler; Collection only sets defaults at generation.
type Card struct {
	ID          string
	UserID      string
	ProfileID   string
	NoteID      string
	DeckID      string
	TemplateOrd int // 0-based; for cloze cards, cloze index minus one
	State       CardState
	Queue       int // negative = suspended/buried
	Due         *time.Time
	Stability   *float64
	Difficulty  *float64
	Reps        int
	Lapses      int
	LastReview  *time.Time
	USN         int64
	Mtime       time.Time
}

// CardRenderSource is what RenderCard needs: the card plus its note fields and note-type templates.
type CardRenderSource struct {
	Card      *Card
	Fields    map[string]string
	Templates []CardTemplate
}

// RenderedFront is one card's front HTML.
type RenderedFront struct {
	CardID string
	Front  string
}

// DeckFields are the client-settable deck fields; empty ParentID or ConfigID means none.
type DeckFields struct {
	Name     string
	ParentID string
	ConfigID string
}

// Deck is a node in the hierarchical deck tree. Path is an ltree value encoding
// the ancestor chain; ParentID/ConfigID are nullable.
type Deck struct {
	ID        string
	UserID    string
	ProfileID string
	ParentID  *string
	Name      string
	Path      string
	ConfigID  *string
	USN       int64
	Mtime     time.Time
}

// DeckConfig holds per-deck scheduling parameters shared by one or more decks.
type DeckConfig struct {
	ID               string
	UserID           string
	ProfileID        string
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

// FilteredDeck is an immutable saved card query (execution lives in Scheduler +
// Search, not this service).
type FilteredDeck struct {
	ID        string
	UserID    string
	ProfileID string
	Name      string
	Query     string
	USN       int64
	Mtime     time.Time
}

// Tag is a normalized tag-registry entry for autocomplete; tags are also
// denormalized onto notes.tags.
type Tag struct {
	ID        string
	UserID    string
	ProfileID string
	Name      string
	USN       int64
	Mtime     time.Time
}

// Page bounds a keyset-pagination request. Token is the last id seen (empty =
// from the start); Size is the max number of items to return.
type Page struct {
	Token string
	Size  int
}

// ─── Repository ─────────────────────────────────────────────────────────────

// Repository maps a missing row to apperr.NotFound, a unique violation to AlreadyExists and an
// FK RESTRICT on delete to FailedPrecondition. Lists are keyset by id; "" means exhausted.
type Repository interface {
	// WithTx runs fn inside a single transaction; the Repository passed to fn is
	// scoped to that tx. Callers must not use the outer repository inside fn.
	WithTx(ctx context.Context, fn func(Repository) error) error

	// NextUSN atomically increments and returns the user's monotonic USN.
	NextUSN(ctx context.Context, userID string) (int64, error)

	// NoteType
	CreateNoteType(ctx context.Context, nt *NoteType) error
	GetNoteType(ctx context.Context, userID, id string) (*NoteType, error)
	ListNoteTypes(ctx context.Context, userID string, page Page) (items []NoteType, next string, err error)
	UpdateNoteType(ctx context.Context, nt *NoteType) error
	DeleteNoteType(ctx context.Context, userID, id string) error

	// Note
	CreateNote(ctx context.Context, n *Note) error
	GetNote(ctx context.Context, userID, id string) (*Note, error)
	UpdateNote(ctx context.Context, n *Note) error
	DeleteNote(ctx context.Context, userID, id string) error
	// DeleteOrphanNotes deletes those of noteIDs that have no cards left and returns them.
	DeleteOrphanNotes(ctx context.Context, userID string, noteIDs []string) ([]Note, error)

	// Card
	CreateCard(ctx context.Context, c *Card) error
	GetCard(ctx context.Context, userID, id string) (*Card, error)
	// GetCardRenderSource loads a card with its note fields and note-type templates in one query.
	GetCardRenderSource(ctx context.Context, userID, id string) (*CardRenderSource, error)
	// ListCardRenderSources is the batch form; ids that don't exist are omitted.
	ListCardRenderSources(ctx context.Context, userID string, ids []string) ([]CardRenderSource, error)
	GetCardsByIDs(ctx context.Context, userID string, ids []string) ([]Card, error)
	ListCardsByNote(ctx context.Context, userID, noteID string) ([]Card, error)
	DeleteCardsByNote(ctx context.Context, userID, noteID string) ([]Card, error)
	// ListCardIDs takes no query: query-based selection belongs to Search.
	ListCardIDs(ctx context.Context, userID string, page Page) (ids []string, next string, err error)
	// GetSearchDocs resolves the selector into denormalized card documents for
	// the Search indexer (card+note+deck joined, shard-local).
	GetSearchDocs(ctx context.Context, userID string, sel SearchDocSelector, page Page) (docs []SearchDoc, next string, err error)

	// Deck
	CreateDeck(ctx context.Context, d *Deck) error
	GetDeck(ctx context.Context, userID, id string) (*Deck, error)
	ListDecks(ctx context.Context, userID string, page Page) (items []Deck, next string, err error)
	UpdateDeck(ctx context.Context, d *Deck) error
	DeleteDeck(ctx context.Context, userID, id string) error
	// ListSubtreeDecks returns the deck with the given path and all its
	// descendants (path <@ root), ordered by path, for subtree moves.
	ListSubtreeDecks(ctx context.Context, userID, rootPath string) ([]Deck, error)
	ListDecksByConfig(ctx context.Context, userID, configID string) ([]Deck, error)

	// DeckConfig
	CreateDeckConfig(ctx context.Context, dc *DeckConfig) error
	GetDeckConfig(ctx context.Context, userID, id string) (*DeckConfig, error)
	ListDeckConfigs(ctx context.Context, userID string, page Page) (items []DeckConfig, next string, err error)
	UpdateDeckConfig(ctx context.Context, dc *DeckConfig) error
	DeleteDeckConfig(ctx context.Context, userID, id string) error

	// FilteredDeck
	CreateFilteredDeck(ctx context.Context, fd *FilteredDeck) error
	GetFilteredDeck(ctx context.Context, userID, id string) (*FilteredDeck, error)
	ListFilteredDecks(ctx context.Context, userID string, page Page) (items []FilteredDeck, next string, err error)
	DeleteFilteredDeck(ctx context.Context, userID, id string) error

	// Tag registry. UpsertTag is idempotent on (user_id, name).
	UpsertTag(ctx context.Context, t *Tag) error

	// WriteTombstones records hard deletes for the Sync service.
	WriteTombstones(ctx context.Context, userID, typ string, usn int64, ids ...string) error

	// WriteOutbox panics outside WithTx.
	WriteOutbox(ctx context.Context, entries []events.OutboxEntry) error

	// ── Bulk operations (scoped by userID, batched by card-id set) ──
	// Each returns the number of rows affected and stamps usn/mtime.

	// MoveCards reassigns the given cards to deckID and returns the IDs of rows
	// actually updated (input IDs that don't belong to userID are silently skipped).
	MoveCards(ctx context.Context, userID string, cardIDs []string, deckID string, usn int64) ([]string, error)
	// SuspendCards sets the cards' queue to the suspended sentinel and returns
	// the IDs of rows actually updated.
	SuspendCards(ctx context.Context, userID string, cardIDs []string, usn int64) ([]string, error)
	// DeleteCardsByIDs deletes the given cards and returns the rows deleted so
	// the caller can write tombstones in the same tx.
	DeleteCardsByIDs(ctx context.Context, userID string, cardIDs []string) ([]Card, error)
	// AddTagToNotesOfCards adds tag to the notes owning the given cards and
	// returns the IDs of the notes actually updated.
	AddTagToNotesOfCards(ctx context.Context, userID string, cardIDs []string, tag string, usn int64) ([]string, error)
	// RemoveTagFromNotesOfCards removes tag from the notes owning the given
	// cards and returns the IDs of the notes actually updated.
	RemoveTagFromNotesOfCards(ctx context.Context, userID string, cardIDs []string, tag string, usn int64) ([]string, error)
	// SetFlagOnNotesOfCards sets flags on the notes owning the given cards
	// (flag 0 clears) and returns the IDs of the notes actually updated.
	SetFlagOnNotesOfCards(ctx context.Context, userID string, cardIDs []string, flag int32, usn int64) ([]string, error)
}
