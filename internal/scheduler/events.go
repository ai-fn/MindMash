package scheduler

import (
	"encoding/json"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// Event type constants used as the event_type column in the outbox.
const (
	EventReviewRecorded  = "review.recorded"
	EventCardRescheduled = "card.rescheduled"
)

type reviewLogPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	CardID    string    `json:"card_id"`
	Grade     int8      `json:"grade"`
	PrevState int16     `json:"prev_state"`
	NewIvl    int32     `json:"new_ivl"`
	Ts        time.Time `json:"ts"`
}

type cardSchedulePayload struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	ProfileID string     `json:"profile_id"`
	State     int16      `json:"state"`
	Queue     int16      `json:"queue"`
	Due       *time.Time `json:"due"`
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("scheduler events: marshal payload: " + err.Error())
	}
	return b
}

// reviewLogEntry builds the outbox entry for a recorded answer.
func reviewLogEntry(rl *ReviewLog) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        rl.UserID,
		AggregateType: "review_log",
		AggregateID:   rl.ID,
		EventType:     EventReviewRecorded,
		Payload: mustMarshal(reviewLogPayload{
			ID: rl.ID, UserID: rl.UserID, ProfileID: rl.ProfileID, CardID: rl.CardID,
			Grade: int8(rl.Grade), PrevState: int16(rl.PrevState), NewIvl: rl.NewIvl, Ts: rl.Ts,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

// cardScheduleEntry builds the outbox entry for a manual reschedule action
// (suspend/bury/reset) that changed a card's state/queue/due.
func cardScheduleEntry(c *Card) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        c.UserID,
		AggregateType: "card",
		AggregateID:   c.ID,
		EventType:     EventCardRescheduled,
		Payload: mustMarshal(cardSchedulePayload{
			ID: c.ID, UserID: c.UserID, ProfileID: c.ProfileID,
			State: int16(c.State), Queue: c.Queue, Due: c.Due,
		}),
		CreatedAt: time.Now().UTC(),
	}
}
