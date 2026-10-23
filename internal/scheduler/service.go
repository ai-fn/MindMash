package scheduler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
	"github.com/mindmesh/mindmesh/pkg/fsrs"
)

const (
	defaultQueueLimit = 20
	maxQueueLimit     = 200 // each queued card costs a RenderCard call
)

// Service orchestrates the Scheduler domain: study queue assembly, FSRS
// answer processing, interval previews, today's counts, and manual
// reschedule actions.
type Service struct {
	repo       Repository
	collClient collectionv1.CollectionInternalServiceClient
	log        *zap.Logger
}

// NewService wires a Repository, the Collection internal client (used to render queue cards), and a logger.
func NewService(repo Repository, collClient collectionv1.CollectionInternalServiceClient, log *zap.Logger) *Service {
	return &Service{repo: repo, collClient: collClient, log: log}
}

func fsrsParams(dc *DeckConfig) fsrs.Params {
	return fsrs.Params{
		Weights:          dc.FSRSWeights,
		DesiredRetention: dc.DesiredRetention,
	}
}

func toFSRSInput(c *Card) fsrs.SchedulingInput {
	return fsrs.SchedulingInput{
		State:      fsrs.CardState(c.State),
		Due:        c.Due,
		Stability:  c.Stability,
		Difficulty: c.Difficulty,
		Reps:       c.Reps,
		Lapses:     c.Lapses,
		LastReview: c.LastReview,
	}
}

func subtreeDeckIDs(decks []Deck) []string {
	ids := make([]string, len(decks))
	for i, d := range decks {
		ids[i] = d.ID
	}
	return ids
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// BuildQueue resolves the deck subtree rooted at deckID and pulls due learning
// cards, then reviews and new cards within what today's review_logs leave of the
// daily limits, and renders each card's front HTML via Collection's RenderCard RPC.
// Fetching reserves nothing: only answers consume the limits.
func (s *Service) BuildQueue(ctx context.Context, userID, profileID, deckID string, limit int) ([]QueueCard, error) {
	if err := validate.ID("deck_id", deckID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultQueueLimit
	}
	limit = min(limit, maxQueueLimit)

	deck, err := s.repo.GetDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListSubtreeDecks(ctx, userID, deck.Path)
	if err != nil {
		return nil, err
	}
	deckIDs := subtreeDeckIDs(decks)

	dc, err := s.repo.GetEffectiveDeckConfig(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	reviewed, err := s.repo.CountReviewedToday(ctx, userID, deckIDs, DayStart(now, dc.Timezone, dc.RolloverHour))
	if err != nil {
		return nil, err
	}

	// Learning steps are not capped by rev_per_day, as in Anki.
	learning, err := s.repo.ListDueCards(ctx, userID, deckIDs, []CardState{StateLearning, StateRelearning}, now, limit)
	if err != nil {
		return nil, err
	}
	reviewCap := min(limit-len(learning), int(dc.RevPerDay)-int(reviewed.Review))
	var reviews []Card
	if reviewCap > 0 {
		if reviews, err = s.repo.ListDueCards(ctx, userID, deckIDs, []CardState{StateReview}, now, reviewCap); err != nil {
			return nil, err
		}
	}
	due := append(learning, reviews...)

	newCap := min(limit-len(due), int(dc.NewPerDay)-int(reviewed.New))
	var newCards []Card
	if newCap > 0 {
		if newCards, err = s.repo.ListNewCards(ctx, userID, deckIDs, newCap); err != nil {
			return nil, err
		}
	}

	all := append(due, newCards...)
	queue := make([]QueueCard, 0, len(all))
	for _, c := range all {
		resp, err := s.collClient.RenderCard(ctx, &collectionv1.RenderCardRequest{CardId: c.ID})
		if err != nil {
			return nil, fmt.Errorf("scheduler: render card %s: %w", c.ID, err)
		}
		queue = append(queue, QueueCard{Card: c, FrontHTML: resp.FrontHtml})
	}
	return queue, nil
}

// Preview returns the four hypothetical next-interval estimates for a card
// without advancing its state.
func (s *Service) Preview(ctx context.Context, userID, cardID string) (fsrs.IntervalPreview, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return fsrs.IntervalPreview{}, err
	}
	c, err := s.repo.LoadCardForUpdate(ctx, userID, cardID)
	if err != nil {
		return fsrs.IntervalPreview{}, err
	}
	dc, err := s.repo.GetEffectiveDeckConfig(ctx, userID, c.DeckID)
	if err != nil {
		return fsrs.IntervalPreview{}, err
	}
	engine := fsrs.New(fsrsParams(dc))
	return engine.Preview(toFSRSInput(c), time.Now().UTC()), nil
}

// Answer submits a grade for a card: locks the card row, advances its FSRS
// state, persists the new schedule, appends a review_logs row, and writes
// the review.recorded outbox entry, all in one transaction. Returns the
// updated card and the next-answer interval previews.
func (s *Service) Answer(ctx context.Context, userID, profileID, cardID string, grade Grade, timeMs int32) (*Card, fsrs.IntervalPreview, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return nil, fsrs.IntervalPreview{}, err
	}
	var updated *Card
	var preview fsrs.IntervalPreview

	err := s.repo.WithTx(ctx, func(tx Repository) error {
		c, err := tx.LoadCardForUpdate(ctx, userID, cardID)
		if err != nil {
			return err
		}
		dc, err := tx.GetEffectiveDeckConfig(ctx, userID, c.DeckID)
		if err != nil {
			return err
		}

		prevState := c.State
		var prevIvl int32
		if c.Due != nil && c.LastReview != nil {
			prevIvl = int32(c.Due.Sub(*c.LastReview).Seconds())
		}

		engine := fsrs.New(fsrsParams(dc))
		now := time.Now().UTC()
		result := engine.Next(toFSRSInput(c), fsrs.Grade(grade), now)

		c.State = CardState(result.State)
		due := result.Due
		c.Due = &due
		stability := result.Stability
		c.Stability = &stability
		difficulty := result.Difficulty
		c.Difficulty = &difficulty
		c.Reps = result.Reps
		c.Lapses = result.Lapses
		c.LastReview = &now

		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		c.USN = usn
		c.Mtime = now

		if err := tx.UpdateCardSchedule(ctx, c); err != nil {
			return err
		}

		rl := &ReviewLog{
			ID:        ids.NewUUID(),
			UserID:    userID,
			ProfileID: profileID,
			CardID:    c.ID,
			DeckID:    c.DeckID,
			Grade:     grade,
			PrevState: prevState,
			NewState:  c.State,
			PrevIvl:   prevIvl,
			NewIvl:    int32(result.IntervalSec),
			TimeMs:    timeMs,
			Ts:        now,
		}
		if err := tx.InsertReviewLog(ctx, rl); err != nil {
			return err
		}
		if err := tx.WriteOutbox(ctx, []events.OutboxEntry{reviewLogEntry(rl)}); err != nil {
			return err
		}

		preview = engine.Preview(toFSRSInput(c), now)
		updated = c
		return nil
	})
	if err != nil {
		return nil, fsrs.IntervalPreview{}, err
	}
	return updated, preview, nil
}

// GetCounts returns today's remaining new/learning/review card counts for a
// deck subtree, accounting for cards already answered today.
func (s *Service) GetCounts(ctx context.Context, userID, deckID string) (Counts, error) {
	if err := validate.ID("deck_id", deckID); err != nil {
		return Counts{}, err
	}
	const availabilityCap = 9999

	deck, err := s.repo.GetDeck(ctx, userID, deckID)
	if err != nil {
		return Counts{}, err
	}
	decks, err := s.repo.ListSubtreeDecks(ctx, userID, deck.Path)
	if err != nil {
		return Counts{}, err
	}
	deckIDs := subtreeDeckIDs(decks)

	dc, err := s.repo.GetEffectiveDeckConfig(ctx, userID, deckID)
	if err != nil {
		return Counts{}, err
	}

	now := time.Now().UTC()
	dayStart := DayStart(now, dc.Timezone, dc.RolloverHour)
	reviewed, err := s.repo.CountReviewedToday(ctx, userID, deckIDs, dayStart)
	if err != nil {
		return Counts{}, err
	}

	due, err := s.repo.ListDueCards(ctx, userID, deckIDs, []CardState{StateLearning, StateReview, StateRelearning}, now, availabilityCap)
	if err != nil {
		return Counts{}, err
	}
	var learningDue, reviewDue int32
	for _, c := range due {
		if c.State == StateLearning || c.State == StateRelearning {
			learningDue++
		} else {
			reviewDue++
		}
	}
	newAvail, err := s.repo.ListNewCards(ctx, userID, deckIDs, availabilityCap)
	if err != nil {
		return Counts{}, err
	}

	newRemaining := minInt32(int32(len(newAvail)), dc.NewPerDay-reviewed.New)
	reviewRemaining := minInt32(reviewDue, dc.RevPerDay-reviewed.Review)
	if newRemaining < 0 {
		newRemaining = 0
	}
	if reviewRemaining < 0 {
		reviewRemaining = 0
	}

	return Counts{New: newRemaining, Learning: learningDue, Review: reviewRemaining}, nil
}

// Reschedule applies a manual suspend/bury/reset action to a card.
func (s *Service) Reschedule(ctx context.Context, userID, cardID string, action RescheduleAction) (*Card, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return nil, err
	}
	var updated *Card

	err := s.repo.WithTx(ctx, func(tx Repository) error {
		c, err := tx.LoadCardForUpdate(ctx, userID, cardID)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		switch action {
		case ActionSuspend:
			c.Queue = QueueSuspended
		case ActionBury:
			c.Queue = QueueBuried
		case ActionReset:
			c.State = StateNew
			c.Queue = 0
			c.Due = nil
			c.Stability = nil
			c.Difficulty = nil
			c.Reps = 0
			c.Lapses = 0
			c.LastReview = nil
		default:
			return platformerrors.InvalidArgument("unknown reschedule action")
		}

		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		c.USN = usn
		c.Mtime = now

		if err := tx.UpdateCardSchedule(ctx, c); err != nil {
			return err
		}
		if err := tx.WriteOutbox(ctx, []events.OutboxEntry{cardScheduleEntry(c)}); err != nil {
			return err
		}

		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}
