package scheduler

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
	"github.com/mindmesh/mindmesh/pkg/fsrs"
)

const (
	defaultQueueLimit = 20
	maxQueueLimit     = 200
	// Fronts are rendered in chunks: 20 fronts of a 1 MiB note plus 64 KiB template fit renderRecvBytes.
	renderBatch     = 20
	renderRecvBytes = 32 << 20
	maxAnswerTimeMs = 60_000 // Anki's default cap, so an idle tab doesn't skew time stats
)

// Service orchestrates the Scheduler domain.
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
		MaxIntervalDays:  int(dc.MaxIntervalDays),
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

// remaining is what's left of a daily limit, in int64 so a negative limit can't wrap.
func remaining(available, perDay, done int32) int32 {
	return int32(max(0, min(int64(available), int64(perDay)-int64(done))))
}

// BuildQueue fills learning, then review and new cards within what today's review_logs leave of
// the daily limits; fetching reserves nothing, only answers consume them.
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
	dayStart := DayStart(now, dc.Timezone, dc.RolloverHour)
	if err := s.unbury(ctx, userID, deckIDs, dayStart); err != nil {
		return nil, err
	}
	reviewed, err := s.repo.CountReviewedToday(ctx, userID, deckIDs, dayStart)
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
	if len(all) == 0 {
		return nil, nil
	}
	cardIDs := make([]string, len(all))
	for i, c := range all {
		cardIDs[i] = c.ID
	}
	front := make(map[string]string, len(cardIDs))
	for chunk := range slices.Chunk(cardIDs, renderBatch) {
		resp, err := s.collClient.RenderCards(ctx, &collectionv1.RenderCardsRequest{CardIds: chunk},
			grpc.MaxCallRecvMsgSize(renderRecvBytes))
		if err != nil {
			return nil, fmt.Errorf("scheduler: render cards: %w", err)
		}
		for _, r := range resp.GetCards() {
			front[r.GetCardId()] = r.GetFrontHtml()
		}
	}
	queue := make([]QueueCard, 0, len(all))
	for _, c := range all {
		// A card deleted since it was listed is missing from the render; drop it.
		if html, ok := front[c.ID]; ok {
			queue = append(queue, QueueCard{Card: c, FrontHTML: html})
		}
	}
	return queue, nil
}

// unbury returns cards buried before dayStart to the queue, as Anki does at rollover.
func (s *Service) unbury(ctx context.Context, userID string, deckIDs []string, dayStart time.Time) error {
	// Unlocked pre-check keeps the common no-op path free of a tx and a USN bump.
	if buried, err := s.repo.ListBuriedCardIDs(ctx, userID, deckIDs, dayStart); err != nil || len(buried) == 0 {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		// Cards before user_usn, the same lock order as Answer and Reschedule.
		buried, err := tx.ListBuriedCardIDs(ctx, userID, deckIDs, dayStart)
		if err != nil || len(buried) == 0 {
			return err
		}
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		cards, err := tx.UnburyCards(ctx, userID, buried, usn)
		if err != nil {
			return err
		}
		entries := make([]events.OutboxEntry, len(cards))
		for i := range cards {
			entries[i] = cardScheduleEntry(&cards[i])
		}
		return tx.WriteOutbox(ctx, entries)
	})
}

// Preview returns the four hypothetical next-interval estimates for a card
// without advancing its state.
func (s *Service) Preview(ctx context.Context, userID, cardID string) (fsrs.IntervalPreview, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return fsrs.IntervalPreview{}, err
	}
	c, err := s.repo.GetCard(ctx, userID, cardID)
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

// Answer schedules, logs and emits review.recorded in one transaction; a repeated clientReviewID
// returns the current state without answering again.
func (s *Service) Answer(ctx context.Context, userID, profileID, cardID, clientReviewID string, grade Grade, timeMs int32) (*Card, fsrs.IntervalPreview, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return nil, fsrs.IntervalPreview{}, err
	}
	if err := validate.OptionalID("client_review_id", clientReviewID); err != nil {
		return nil, fsrs.IntervalPreview{}, err
	}
	if grade < Again || grade > Easy {
		return nil, fsrs.IntervalPreview{}, apperr.InvalidArgument("grade must be 1-4")
	}
	timeMs = min(max(timeMs, 0), maxAnswerTimeMs)
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
		engine := fsrs.New(fsrsParams(dc))
		now := time.Now().UTC()

		// Checked under the card lock, so a concurrent retry sees the first attempt's committed log.
		if clientReviewID != "" {
			reviewedCard, found, err := tx.ReviewedCardID(ctx, userID, clientReviewID)
			if err != nil {
				return err
			}
			if found {
				if reviewedCard != cardID {
					return apperr.InvalidArgument("client_review_id was already used for another card")
				}
				preview = engine.Preview(toFSRSInput(c), now)
				updated = c
				return nil
			}
		}

		if c.Queue == QueueBuried && c.Mtime.Before(DayStart(now, dc.Timezone, dc.RolloverHour)) {
			c.Queue = 0
		}
		if c.Queue < 0 {
			return apperr.FailedPrecondition("card is suspended or buried")
		}

		prevState := c.State
		var prevIvl int64
		if c.Due != nil && c.LastReview != nil {
			prevIvl = int64(c.Due.Sub(*c.LastReview).Seconds())
		}
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
			NewIvl:    result.IntervalSec,
			TimeMs:    timeMs,
			Ts:        now,

			ClientReviewID: clientReviewID,
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
	if err := s.unbury(ctx, userID, deckIDs, dayStart); err != nil {
		return Counts{}, err
	}
	reviewed, err := s.repo.CountReviewedToday(ctx, userID, deckIDs, dayStart)
	if err != nil {
		return Counts{}, err
	}

	avail, err := s.repo.CountAvailable(ctx, userID, deckIDs, now)
	if err != nil {
		return Counts{}, err
	}
	return Counts{
		New:      remaining(avail.New, dc.NewPerDay, reviewed.New),
		Learning: avail.Learning,
		Review:   remaining(avail.Review, dc.RevPerDay, reviewed.Review),
	}, nil
}

// Reschedule applies a manual suspend/bury/reset action to a card.
func (s *Service) Reschedule(ctx context.Context, userID, cardID string, action RescheduleAction) (*Card, error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return nil, err
	}
	if action < ActionSuspend || action > ActionReset {
		return nil, apperr.InvalidArgument("unknown reschedule action")
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
