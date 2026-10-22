//go:build integration

package events

import (
	"context"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// partitionCount relies on the redpanda module's default auto_create_topics=false: metadata reads don't create.
func partitionCount(t *testing.T, broker, topic string) (int, error) {
	t.Helper()
	conn, err := kafka.Dial("tcp", broker)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	parts, err := conn.ReadPartitions(topic)
	return len(parts), err
}

func topicSetting(t *testing.T, broker, topic, name string) string {
	t.Helper()
	client := &kafka.Client{Addr: kafka.TCP(broker)}
	resp, err := client.DescribeConfigs(context.Background(), &kafka.DescribeConfigsRequest{
		Resources: []kafka.DescribeConfigRequestResource{{ResourceType: kafka.ResourceTypeTopic, ResourceName: topic, ConfigNames: []string{name}}},
	})
	require.NoError(t, err)
	for _, r := range resp.Resources {
		require.NoError(t, r.Error)
		for _, e := range r.ConfigEntries {
			if e.ConfigName == name {
				return e.ConfigValue
			}
		}
	}
	return ""
}

func requireAbsent(t *testing.T, broker, topic string) {
	t.Helper()
	_, err := partitionCount(t, broker, topic)
	require.ErrorIs(t, err, kafka.UnknownTopicOrPartition)
}

func requirePartitions(t *testing.T, broker string, want int, topics ...string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, tp := range topics {
			if n, err := partitionCount(t, broker, tp); err != nil || n != want {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond)
}

func TestTopicProvisioning(t *testing.T) {
	ctx := context.Background()
	broker := startRedpanda(t, ctx)
	cfg := Config{
		Brokers: []string{broker}, GroupID: "g-topics", ReadTimeout: time.Second,
		WriteTimeout: 5 * time.Second, MaxRetries: 3,
		CreateTopics: true, TopicPartitions: 3, TopicReplication: 1,
	}

	t.Run("EnsureTopics is idempotent", func(t *testing.T) {
		require.NoError(t, EnsureTopics(ctx, cfg, "deck", "note"))
		require.NoError(t, EnsureTopics(ctx, cfg, "deck", "note"))
		requirePartitions(t, broker, 3, "deck", "note")
	})

	t.Run("misconfiguration is permanent", func(t *testing.T) {
		bad := cfg
		bad.TopicReplication = 3 // single-broker cluster
		err := EnsureTopics(ctx, bad, "media")
		require.ErrorIs(t, err, ErrTopicsMisconfigured)

		c, err := NewConsumer(bad, zap.NewNop())
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		err = c.Subscribe(ctx, []string{"media"}, func(context.Context, Message) error { return nil })
		require.ErrorIs(t, err, ErrTopicsMisconfigured, "subscribe must not retry a misconfiguration forever")
	})

	t.Run("disabled is a no-op", func(t *testing.T) {
		off := cfg
		off.CreateTopics = false
		require.NoError(t, EnsureTopics(ctx, off, "filtered_deck"))
		requireAbsent(t, broker, "filtered_deck")
	})

	t.Run("publish creates unseen topics", func(t *testing.T) {
		p, err := NewProducer(cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = p.Close() })

		require.NoError(t, p.Publish(ctx, Message{Topic: "note_type", Key: []byte("u1"), Value: []byte("1")}))
		requirePartitions(t, broker, 3, "note_type")
	})

	t.Run("subscribe creates its topics and their DLQs", func(t *testing.T) {
		requireAbsent(t, broker, "review_log")
		requireAbsent(t, broker, "review_log.dlq")
		c, err := NewConsumer(cfg, zap.NewNop())
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		stop := runConsumer(t, c, "review_log", func(context.Context, Message) error { return nil })
		requirePartitions(t, broker, 3, "review_log", "review_log.dlq")
		stop()
		assert.Equal(t, "2592000000", topicSetting(t, broker, "review_log.dlq", "retention.ms"))
	})
}
