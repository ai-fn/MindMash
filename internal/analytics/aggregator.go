// Package analytics is the Search-fed-equivalent for study statistics: a
// stats-aggregator worker mirrors Scheduler's review.recorded stream into
// ClickHouse, and a read API serves columnar aggregations over it.
package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// ReviewTopic is the Kafka topic the aggregator consumes. The relay publishes
// each outbox row to a topic named after its aggregate_type; Scheduler writes
// review.recorded under aggregate_type "review_log".
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

// Aggregator inserts review events into ClickHouse. It is at-least-once and
// idempotent: review_events is a ReplacingMergeTree keyed on review_id, so a
// redelivered review collapses to a single row on merge. Inserts use
// ClickHouse server-side async batching (async_insert) and wait for the buffer
// flush before returning, so an offset is committed only once the row is
// durable.
type Aggregator struct {
	conn driver.Conn
	log  *zap.Logger
}

// NewAggregator wires an Aggregator.
func NewAggregator(conn driver.Conn, log *zap.Logger) *Aggregator {
	return &Aggregator{conn: conn, log: log}
}

// asyncInsertCtx attaches the async-insert settings to ctx. async_insert=1 lets
// the server batch many small inserts into larger parts; wait_for_async_insert=1
// blocks until that batch is flushed, giving us durability for the offset commit.
func asyncInsertCtx(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"async_insert":          1,
		"wait_for_async_insert": 1,
	}))
}

const insertReviewEvent = `INSERT INTO review_events
	(review_id, user_id, profile_id, card_id, deck_id, grade, prev_state, new_state, ivl, time_ms, ts)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Handle processes one Kafka message. It is the handler passed to
// events.Consumer.Subscribe: returning nil commits the offset, an error leaves
// the message for redelivery. Malformed messages are dropped (logged + committed)
// rather than poison-looping the consumer.
func (a *Aggregator) Handle(ctx context.Context, msg events.Message) error {
	if et := msg.Headers["event_type"]; et != eventReviewRecorded {
		// Unknown/irrelevant event on this topic — nothing to do.
		return nil
	}

	var p reviewPayload
	if err := json.Unmarshal(msg.Value, &p); err != nil {
		a.log.Warn("aggregator: undecodable payload", zap.Error(err))
		return nil
	}
	if p.ID == "" || p.UserID == "" {
		a.log.Warn("aggregator: payload missing id/user_id")
		return nil
	}

	if err := a.conn.Exec(asyncInsertCtx(ctx), insertReviewEvent,
		p.ID, p.UserID, p.ProfileID, p.CardID, p.DeckID,
		p.Grade, p.PrevState, p.NewState, p.NewIvl, p.TimeMs, p.Ts,
	); err != nil {
		return fmt.Errorf("aggregator: insert review_event %s: %w", p.ID, err)
	}
	return nil
}
