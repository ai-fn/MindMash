// Package scheduler contains the domain layer for the Scheduler service:
// FSRS-driven study queues, answer processing, interval previews, and manual
// reschedule actions. It shares the Collection Postgres database and writes
// the `cards` table's scheduling columns directly (state, queue, due,
// stability, difficulty, reps, lapses, last_review); Collection owns the
// structural columns (note_id, deck_id, template_ord) and never resets these.
package scheduler

import (
	"context"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// CardState mirrors the cards.state SMALLINT value used by Collection
// (internal/collection/domain.go). The wire/proto enum is offset by +1;
// that mapping lives in the handler, not here.
type CardState int16

const (
	StateNew        CardState = 0
	StateLearning   CardState = 1
	StateReview     CardState = 2
	StateRelearning CardState = 3
)

// Queue sentinels for the cards.queue column (mirrors Collection's convention
// of negative values meaning suspended/buried).
const (
	QueueSuspended int16 = -1
	QueueBuried    int16 = -2
)

// Grade is the FSRS answer rating submitted by the client.
type Grade int8

const (
	Again Grade = iota + 1
	Hard
	Good
	Easy
)

// RescheduleAction is a manual scheduling operation.
type RescheduleAction int8

const (
	ActionSuspend RescheduleAction = iota + 1
	ActionBury
	ActionReset
)

// Card is the scheduling-relevant subset of a Collection `cards` row.
type Card struct {
	ID         string
	UserID     string
	ProfileID  string
	DeckID     string
	State      CardState
	Queue      int16
	Due        *time.Time
	Stability  *float64
	Difficulty *float64
	Reps       int32
	Lapses     int32
	LastReview *time.Time
	USN        int64
	Mtime      time.Time
}

// Deck is the minimal deck shape the scheduler needs to resolve subtrees and
// effective configs (mirrors internal/collection/domain.go's Deck).
type Deck struct {
	ID       string
	Path     string
	ConfigID *string
}

// DeckConfig holds the per-deck scheduling parameters that drive the FSRS
// engine and the daily new/review caps.
type DeckConfig struct {
	ID               string
	NewPerDay        int32
	RevPerDay        int32
	DesiredRetention float64
	FSRSWeights      []float64
	LearningSteps    []int32 // minutes
	RolloverHour     int32
	Timezone         string
}

// DefaultDeckConfig is used when a deck has no config_id set.
func DefaultDeckConfig() *DeckConfig {
	return &DeckConfig{
		NewPerDay:        20,
		RevPerDay:        200,
		DesiredRetention: 0.9,
		LearningSteps:    []int32{1, 10},
		RolloverHour:     4,
		Timezone:         "UTC",
	}
}

// ReviewLog is one answer record persisted to review_logs. Interval fields are
// stored in seconds so sub-day learning steps are representable.
type ReviewLog struct {
	ID        string
	UserID    string
	ProfileID string
	CardID    string
	DeckID    string
	Grade     Grade
	PrevState CardState
	NewState  CardState
	PrevIvl   int64
	NewIvl    int64
	TimeMs    int32
	Ts        time.Time
	// ClientReviewID is the optional Answer idempotency key; "" stores NULL.
	ClientReviewID string
}

// QueueCard pairs a card with its server-rendered front HTML for the study
// queue response.
type QueueCard struct {
	Card      Card
	FrontHTML string
}

// Counts holds today's remaining new/learning/review card counts for a deck.
type Counts struct {
	New      int32
	Learning int32
	Review   int32
}

// Repository defines all persistent-storage operations for the Scheduler
// domain. Every method is tenant-scoped by userID.
type Repository interface {
	// WithTx runs fn inside a single transaction; the Repository passed to fn is
	// scoped to that tx. Callers must not use the outer repository inside fn.
	WithTx(ctx context.Context, fn func(Repository) error) error

	// NextUSN atomically increments and returns the user's monotonic USN
	// (shared user_usn table with Collection).
	NextUSN(ctx context.Context, userID string) (int64, error)

	// LoadCardForUpdate locks the card row for the duration of the answer
	// transaction so concurrent answers on the same card serialize.
	LoadCardForUpdate(ctx context.Context, userID, cardID string) (*Card, error)
	// UpdateCardSchedule writes the scheduling columns back to `cards` and
	// bumps usn/mtime.
	UpdateCardSchedule(ctx context.Context, c *Card) error
	// InsertReviewLog appends one review_logs row.
	InsertReviewLog(ctx context.Context, rl *ReviewLog) error
	// ReviewedCardID returns the card answered under clientReviewID, if any.
	ReviewedCardID(ctx context.Context, userID, clientReviewID string) (cardID string, found bool, err error)

	// ListDueCards returns cards in deckIDs with one of states due at or before now,
	// ordered by due ascending, capped at limit.
	ListDueCards(ctx context.Context, userID string, deckIDs []string, states []CardState, now time.Time, limit int) ([]Card, error)
	// ListNewCards returns unsuspended, unburied new cards in deckIDs ordered by id (ULID, time-sortable)
	// ascending, capped at limit.
	ListNewCards(ctx context.Context, userID string, deckIDs []string, limit int) ([]Card, error)
	// ListBuriedCardIDs locks and returns cards in deckIDs buried before the given time.
	ListBuriedCardIDs(ctx context.Context, userID string, deckIDs []string, before time.Time) ([]string, error)
	// UnburyCards returns the given buried cards to the queue and returns the cards it changed.
	UnburyCards(ctx context.Context, userID string, cardIDs []string, usn int64) ([]Card, error)

	// GetDeck returns a deck's id/path/config_id.
	GetDeck(ctx context.Context, userID, deckID string) (*Deck, error)
	// ListSubtreeDecks returns the deck at rootPath and all its descendants.
	ListSubtreeDecks(ctx context.Context, userID, rootPath string) ([]Deck, error)
	// GetEffectiveDeckConfig resolves the deck's config_id to a DeckConfig,
	// falling back to DefaultDeckConfig when unset.
	GetEffectiveDeckConfig(ctx context.Context, userID, deckID string) (*DeckConfig, error)
	// CountReviewedToday returns how many answers since dayStart were given in
	// deckIDs, split by the card's state at answer time (review_logs.prev_state).
	CountReviewedToday(ctx context.Context, userID string, deckIDs []string, dayStart time.Time) (Counts, error)

	// WriteOutbox appends outbox entries within the current transaction. Must be
	// called inside a WithTx callback.
	WriteOutbox(ctx context.Context, entries []events.OutboxEntry) error
}

// DayStart returns the start of the study "day" for the given IANA timezone
// and rollover hour (0-23). A review answered before the day's rollover hour
// counts toward the previous day's quota. Falls back to UTC on an invalid tz.
func DayStart(now time.Time, tz string, rolloverHour int32) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), int(rolloverHour), 0, 0, 0, loc)
	if local.Before(day) {
		day = day.AddDate(0, 0, -1)
	}
	return day.UTC()
}
