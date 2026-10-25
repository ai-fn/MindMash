//go:build integration

package events

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Pull-to-coordinator INSERT ... SELECT would add a coordinator round trip to every write.
func TestOutboxInsert_RouterPlannedOnCitus(t *testing.T) {
	ctx := context.Background()
	pool := startOutboxDB(t, ctx, "citusdata/citus:13.0", true)
	userID := uuid.NewString()

	for _, n := range []int{1, 3} {
		entries := make([]OutboxEntry, n)
		for i := range entries {
			entries[i] = OutboxEntry{
				ID: uuid.NewString(), UserID: userID, AggregateType: "card", AggregateID: uuid.NewString(),
				EventType: "card.created", Payload: []byte(`{}`), CreatedAt: time.Now(),
			}
		}
		q, args := outboxInsert(entries, nil)
		rows, err := pool.Query(ctx, "EXPLAIN "+q, args...)
		require.NoError(t, err)
		var plan []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan = append(plan, line)
		}
		require.NoError(t, rows.Err())
		text := strings.Join(plan, "\n")
		require.Contains(t, text, "Citus Adaptive", "n=%d plan:\n%s", n, text)
		require.Contains(t, text, "Task Count: 1", "n=%d plan:\n%s", n, text)
		require.NotContains(t, text, "INSERT ... SELECT", "n=%d plan:\n%s", n, text)
	}
}
