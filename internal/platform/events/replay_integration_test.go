//go:build integration

package events

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestReplayDLQ(t *testing.T) {
	ctx := context.Background()
	const topic = "card"
	broker := startBroker(t, ctx, topic, 1)
	createTopic(t, broker, dlqTopic(topic), 1)

	w := &kafka.Writer{Addr: kafka.TCP(broker), RequiredAcks: kafka.RequireAll, BatchTimeout: 10 * time.Millisecond}
	t.Cleanup(func() { _ = w.Close() })
	dead := func(id, group string) kafka.Message {
		return dlqMessage(kafka.Message{
			Topic: topic, Key: []byte("u-" + id), Value: []byte(id),
			Headers: []kafka.Header{{Key: "event_id", Value: []byte(id)}},
		}, assert.AnError, group)
	}
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	require.NoError(t, w.WriteMessages(ctx, dead(a, "search-indexer"), dead(b, "stats-aggregator"), dead(c, "search-indexer")))

	cfg := Config{Brokers: []string{broker}, GroupID: DefaultReplayGroup(topic, "search-indexer"), WriteTimeout: 5 * time.Second, MaxRetries: 3}
	opts := ReplayOptions{Topic: topic, OnlyGroup: "search-indexer", Stall: time.Minute}

	t.Run("missing DLQ topic is an error", func(t *testing.T) {
		_, err := ReplayDLQ(ctx, cfg, ReplayOptions{Topic: "nope", Stall: time.Minute}, zap.NewNop())
		require.ErrorIs(t, err, kafka.UnknownTopicOrPartition)
	})

	t.Run("dry run publishes nothing", func(t *testing.T) {
		dry := cfg
		dry.GroupID = "dlq-dry-" + uuid.NewString()
		o := opts
		o.DryRun = true
		n, err := ReplayDLQ(ctx, dry, o, zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, 2, n)
		assert.Empty(t, readPartition(t, ctx, broker, topic, 0))
	})

	t.Run("replays matching dead letters without dlq headers", func(t *testing.T) {
		n, err := ReplayDLQ(ctx, cfg, opts, zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, 2, n)

		got := readPartition(t, ctx, broker, topic, 0)
		require.Len(t, got, 2)
		for i, id := range []string{a, c} {
			assert.Equal(t, []byte(id), got[i].Value)
			assert.Equal(t, []byte("u-"+id), got[i].Key)
			for _, h := range got[i].Headers {
				assert.NotContains(t, h.Key, "dlq_")
			}
			assert.Contains(t, got[i].Headers, kafka.Header{Key: "event_id", Value: []byte(id)})
		}
	})

	t.Run("second run resumes after committed offsets", func(t *testing.T) {
		n, err := ReplayDLQ(ctx, cfg, opts, zap.NewNop())
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Len(t, readPartition(t, ctx, broker, topic, 0), 2)
	})

	t.Run("another target keeps its own progress", func(t *testing.T) {
		other := cfg
		other.GroupID = DefaultReplayGroup(topic, "stats-aggregator")
		o := opts
		o.OnlyGroup = "stats-aggregator"
		n, err := ReplayDLQ(ctx, other, o, zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got := readPartition(t, ctx, broker, topic, 0)
		require.Len(t, got, 3)
		assert.Equal(t, []byte(b), got[2].Value)
	})
}

func TestReplayDLQMultiPartitionLimit(t *testing.T) {
	ctx := context.Background()
	const topic = "deck"
	broker := startBroker(t, ctx, topic, 1)
	createTopic(t, broker, dlqTopic(topic), 2)

	w := &kafka.Writer{Addr: kafka.TCP(broker), RequiredAcks: kafka.RequireAll, BatchTimeout: 10 * time.Millisecond, Balancer: kafka.BalancerFunc(
		func(m kafka.Message, _ ...int) int { return int(m.Key[0] - '0') },
	)}
	t.Cleanup(func() { _ = w.Close() })
	var msgs []kafka.Message
	for _, key := range []string{"0a", "1a", "1b", "1c"} {
		msgs = append(msgs, dlqMessage(kafka.Message{Topic: topic, Key: []byte(key), Value: []byte(key)}, assert.AnError, "g"))
	}
	require.NoError(t, w.WriteMessages(ctx, msgs...))

	cfg := Config{Brokers: []string{broker}, GroupID: DefaultReplayGroup(topic, ""), WriteTimeout: 5 * time.Second, MaxRetries: 3}
	opts := ReplayOptions{Topic: topic, Stall: time.Minute, Limit: 2}

	n, err := ReplayDLQ(ctx, cfg, opts, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "limit stops mid-way")

	opts.Limit = 0
	n, err = ReplayDLQ(ctx, cfg, opts, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "rerun resumes with the rest")

	n, err = ReplayDLQ(ctx, cfg, opts, zap.NewNop())
	require.NoError(t, err)
	assert.Zero(t, n)

	got := map[string]int{}
	for _, m := range readPartition(t, ctx, broker, topic, 0) {
		got[string(m.Value)]++
	}
	assert.Equal(t, map[string]int{"0a": 1, "1a": 1, "1b": 1, "1c": 1}, got, "each dead letter replayed exactly once")
}
