package syncer

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// notifyTopics are Collection's and Scheduler's aggregate types, hand-kept since those packages
// can't be imported; TestNotifyTopics_MatchesKnownAggregateTypes guards the list.
var notifyTopics = []string{
	"note_type", "note", "card", "deck", "deck_config", "filtered_deck", "review_log",
}

// userIDPayload extracts the one field every outbox payload shares.
type userIDPayload struct {
	UserID string `json:"user_id"`
}

// RunNotifier nudges each event's user via svc.Notify until ctx is cancelled.
func RunNotifier(ctx context.Context, consumer events.Consumer, svc *Service, log *zap.Logger) error {
	return consumer.Subscribe(ctx, notifyTopics, func(ctx context.Context, msg events.Message) error {
		var p userIDPayload
		if err := json.Unmarshal(msg.Value, &p); err != nil {
			// Ack: a message that never parses would block the topic forever.
			log.Warn("sync: notifier: unmarshal payload", zap.String("topic", msg.Topic), zap.Error(err))
			return nil
		}
		if p.UserID == "" {
			return nil
		}
		if err := svc.Notify(ctx, p.UserID); err != nil {
			// Ack anyway: a lost nudge loses no data, the next pull catches up.
			log.Warn("sync: notifier: notify failed", zap.String("user_id", p.UserID), zap.Error(err))
		}
		return nil
	})
}
