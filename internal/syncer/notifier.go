package syncer

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// notifyTopics lists every outbox-relayed Kafka topic (== AggregateType, see
// internal/collection/events.go and internal/scheduler/events.go) whose
// messages mean "this user's data changed, nudge their open tabs." The
// message body's shape varies per topic but every one carries user_id, which
// is all the notifier needs — the client re-pulls GetChanges for the rest.
//
// This list is hand-maintained: nothing here is compiler-checked against
// Collection's/Scheduler's actual AggregateType strings (internal/<service>
// packages can't be imported cross-service). See notifier_test.go's
// TestNotifyTopics_MatchesKnownAggregateTypes for the regression guard —
// update both together if either side adds/renames an aggregate type.
var notifyTopics = []string{
	"note_type", "note", "card", "deck", "deck_config", "filtered_deck", "review_log",
}

// userIDPayload extracts the one field every outbox payload shares.
type userIDPayload struct {
	UserID string `json:"user_id"`
}

// RunNotifier subscribes to every notifyTopics entry in a single multi-topic
// Subscribe call (one Kafka reader, not one per topic — see
// events.Consumer.Subscribe's doc comment) and calls svc.Notify for each
// message's user_id, fanning Collection/Scheduler's mutation events out to
// open SSE connections via Redis. It blocks until ctx is cancelled.
func RunNotifier(ctx context.Context, consumer events.Consumer, svc *Service, log *zap.Logger) error {
	return consumer.Subscribe(ctx, notifyTopics, func(ctx context.Context, msg events.Message) error {
		var p userIDPayload
		if err := json.Unmarshal(msg.Value, &p); err != nil {
			// Malformed payload: log and ack rather than blocking the topic
			// forever on a message that will never parse.
			log.Warn("sync: notifier: unmarshal payload", zap.String("topic", msg.Topic), zap.Error(err))
			return nil
		}
		if p.UserID == "" {
			return nil
		}
		if err := svc.Notify(ctx, p.UserID); err != nil {
			// Redis hiccup: log and ack the Kafka message anyway. The nudge
			// is best-effort — failing it doesn't lose data, since the
			// client's next GetChanges pull always reflects true state.
			log.Warn("sync: notifier: notify failed", zap.String("user_id", p.UserID), zap.Error(err))
		}
		return nil
	})
}
