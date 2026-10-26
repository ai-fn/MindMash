// Package analytics mirrors Scheduler's review.recorded stream into ClickHouse and serves
// aggregations over it.
package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// ReviewTopic is Scheduler's review.recorded aggregate type, which the relay uses as the topic.
const ReviewTopic = "review_log"

// eventReviewRecorded is the event_type the aggregator acts on. Other event
// types on the topic (none today) are ignored as successful no-ops.
const eventReviewRecorded = "review.recorded"

// reviewPayload mirrors Scheduler's reviewLogPayload (internal/scheduler/events.go).
// The field tags are the cross-service contract; see TestReviewLogEntry_PayloadContract.
type reviewPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	CardID    string    `json:"card_id"`
	DeckID    string    `json:"deck_id"`
	Grade     int8      `json:"grade"`
	PrevState int16     `json:"prev_state"`
	NewState  int16     `json:"new_state"`
	NewIvl    int64     `json:"new_ivl"`
	TimeMs    int32     `json:"time_ms"`
	Ts        time.Time `json:"ts"`
}

// Aggregator inserts once per Kafka batch; a redelivered review collapses on merge, since
// review_events is a ReplacingMergeTree keyed on review_id.
type Aggregator struct {
	conn driver.Conn
	log  *zap.Logger
}

// NewAggregator wires an Aggregator.
func NewAggregator(conn driver.Conn, log *zap.Logger) *Aggregator {
	return &Aggregator{conn: conn, log: log}
}

// Batch bounds for SubscribeBatch: few large inserts keep ClickHouse's part count down.
var Batch = events.BatchOptions{Size: 1000, Wait: time.Second}

const insertReviewEvents = `INSERT INTO review_events
	(review_id, user_id, profile_id, card_id, deck_id, grade, prev_state, new_state, ivl, time_ms, ts)`

// HandleBatch is the events.KafkaConsumer.SubscribeBatch handler: nil commits the batch, an error
// redelivers it. Malformed and unrelated messages are skipped rather than poison-looping.
func (a *Aggregator) HandleBatch(ctx context.Context, msgs []events.Message) error {
	var rows []reviewPayload
	for _, msg := range msgs {
		if p, ok := a.decode(msg); ok {
			rows = append(rows, p)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	b, err := a.conn.PrepareBatch(ctx, insertReviewEvents)
	if err != nil {
		return fmt.Errorf("aggregator: prepare batch: %w", err)
	}
	defer func() { _ = b.Abort() }()
	for _, p := range rows {
		if err := b.Append(p.ID, p.UserID, p.ProfileID, p.CardID, p.DeckID,
			p.Grade, p.PrevState, p.NewState, p.NewIvl, p.TimeMs, p.Ts); err != nil {
			return fmt.Errorf("aggregator: append review_event %s: %w", p.ID, err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("aggregator: insert %d review_events: %w", len(rows), err)
	}
	return nil
}

func (a *Aggregator) decode(msg events.Message) (reviewPayload, bool) {
	if msg.Headers["event_type"] != eventReviewRecorded {
		return reviewPayload{}, false
	}
	var p reviewPayload
	if err := json.Unmarshal(msg.Value, &p); err != nil {
		a.log.Warn("aggregator: undecodable payload", zap.String("event_id", msg.Headers["event_id"]), zap.Error(err))
		return reviewPayload{}, false
	}
	if p.ID == "" || p.UserID == "" {
		a.log.Warn("aggregator: payload missing id/user_id", zap.String("event_id", msg.Headers["event_id"]))
		return reviewPayload{}, false
	}
	return p, true
}
