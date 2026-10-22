package events

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestLoadConfig_Defaults(t *testing.T) {
	testhelpers.UnsetAll(t,
		"KAFKA_BROKERS",
		"KAFKA_GROUP_ID",
		"KAFKA_CLIENT_ID",
		"KAFKA_READ_TIMEOUT",
		"KAFKA_WRITE_TIMEOUT",
		"KAFKA_MAX_RETRIES",
		"KAFKA_HANDLER_MAX_ATTEMPTS",
		"KAFKA_CREATE_TOPICS",
		"KAFKA_TOPIC_PARTITIONS",
		"KAFKA_TOPIC_REPLICATION",
	)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, []string{"localhost:9092"}, cfg.Brokers)
	assert.Empty(t, cfg.GroupID, "each consumer must name its own group")
	assert.Equal(t, "mindmesh-events", cfg.ClientID)
	assert.Equal(t, 10*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 10*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 10, cfg.HandlerMaxAttempts)
	assert.True(t, cfg.CreateTopics)
	assert.Equal(t, 12, cfg.TopicPartitions)
	assert.Equal(t, 1, cfg.TopicReplication)
}

func TestLoadConfig_BrokerSplit(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker1:9092,broker2:9092")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, cfg.Brokers)
}

func TestLoadConfig_EmptyBrokers(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KAFKA_BROKERS")
}

func TestLoadConfig_InvalidEnvVars(t *testing.T) {
	testhelpers.UnsetAll(t,
		"KAFKA_BROKERS",
		"KAFKA_GROUP_ID",
		"KAFKA_CLIENT_ID",
		"KAFKA_READ_TIMEOUT",
		"KAFKA_WRITE_TIMEOUT",
		"KAFKA_MAX_RETRIES",
		"KAFKA_HANDLER_MAX_ATTEMPTS",
	)

	tests := []struct {
		name    string
		envKey  string
		envVal  string
		wantErr string
	}{
		{
			name:    "KAFKA_READ_TIMEOUT bad value",
			envKey:  "KAFKA_READ_TIMEOUT",
			envVal:  "bad",
			wantErr: "KAFKA_READ_TIMEOUT",
		},
		{
			name:    "KAFKA_WRITE_TIMEOUT bad value",
			envKey:  "KAFKA_WRITE_TIMEOUT",
			envVal:  "bad",
			wantErr: "KAFKA_WRITE_TIMEOUT",
		},
		{
			name:    "KAFKA_MAX_RETRIES not an int",
			envKey:  "KAFKA_MAX_RETRIES",
			envVal:  "notanint",
			wantErr: "KAFKA_MAX_RETRIES",
		},
		{
			name:    "KAFKA_CREATE_TOPICS not a bool",
			envKey:  "KAFKA_CREATE_TOPICS",
			envVal:  "maybe",
			wantErr: "KAFKA_CREATE_TOPICS",
		},
		{
			name:    "KAFKA_TOPIC_PARTITIONS zero",
			envKey:  "KAFKA_TOPIC_PARTITIONS",
			envVal:  "0",
			wantErr: "KAFKA_TOPIC_PARTITIONS",
		},
		{
			name:    "KAFKA_TOPIC_REPLICATION zero",
			envKey:  "KAFKA_TOPIC_REPLICATION",
			envVal:  "0",
			wantErr: "KAFKA_TOPIC_REPLICATION",
		},
		{
			name:    "KAFKA_HANDLER_MAX_ATTEMPTS zero",
			envKey:  "KAFKA_HANDLER_MAX_ATTEMPTS",
			envVal:  "0",
			wantErr: "KAFKA_HANDLER_MAX_ATTEMPTS",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNewRelayCapsBatch(t *testing.T) {
	r := NewRelay(nil, nil, nil, RelayConfig{Batch: 10_000})
	assert.Equal(t, maxRelayBatch, r.batch)
}

func TestNewProducerWriterConfig(t *testing.T) {
	p, err := NewProducer(Config{Brokers: []string{"localhost:9092"}, WriteTimeout: time.Second, MaxRetries: 3})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	assert.Equal(t, kafka.RequireAll, p.writer.RequiredAcks)
	assert.IsType(t, &kafka.Murmur2Balancer{}, p.writer.Balancer)
	assert.Equal(t, 10*time.Millisecond, p.writer.BatchTimeout)
}

func TestDLQMessage(t *testing.T) {
	km := kafka.Message{
		Topic: "card", Partition: 2, Offset: 41, Key: []byte("u1"), Value: []byte("v"),
		Headers: []kafka.Header{{Key: "event_id", Value: []byte("e1")}, {Key: "dlq_error", Value: []byte("old")}},
	}
	dm := dlqMessage(km, errors.New(strings.Repeat("x", 5000)), "search")

	got := map[string]string{}
	for _, h := range dm.Headers {
		_, dup := got[h.Key]
		assert.False(t, dup, "duplicate header %s", h.Key)
		got[h.Key] = string(h.Value)
	}
	assert.Equal(t, "card.dlq", dm.Topic)
	assert.Equal(t, []byte("u1"), dm.Key)
	assert.Equal(t, "e1", got["event_id"])
	assert.Len(t, got["dlq_error"], maxDLQErrorLen)

	multi := dlqMessage(km, errors.New(strings.Repeat("я", maxDLQErrorLen)), "search")
	for _, h := range multi.Headers {
		if h.Key == "dlq_error" {
			assert.True(t, utf8.Valid(h.Value), "truncation must not split a rune")
		}
	}
	assert.Equal(t, "card", got["dlq_topic"])
	assert.Equal(t, "2", got["dlq_partition"])
	assert.Equal(t, "41", got["dlq_offset"])
	assert.Equal(t, "search", got["dlq_group"])
	assert.Len(t, km.Headers, 2, "source headers must not be mutated")
}

func TestNewConsumerDefaults(t *testing.T) {
	c, err := NewConsumer(Config{Brokers: []string{"localhost:9092"}, GroupID: "g"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	assert.Equal(t, defaultHandlerMaxAttempts, c.cfg.HandlerMaxAttempts)
	assert.NotNil(t, c.log)
}

func TestNewConsumerRequiresGroup(t *testing.T) {
	_, err := NewConsumer(Config{Brokers: []string{"localhost:9092"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group")
}

func TestTopicConfig(t *testing.T) {
	single := topicConfig(Config{TopicPartitions: 6, TopicReplication: 1}, "card")
	assert.Equal(t, kafka.TopicConfig{Topic: "card", NumPartitions: 6, ReplicationFactor: 1}, single)

	assert.Empty(t, topicConfig(Config{TopicPartitions: 6, TopicReplication: 2}, "card").ConfigEntries,
		"RF=2 keeps the broker default so one broker can be lost")

	replicated := topicConfig(Config{TopicPartitions: 6, TopicReplication: 3}, "card")
	assert.Equal(t, []kafka.ConfigEntry{{ConfigName: "min.insync.replicas", ConfigValue: "2"}}, replicated.ConfigEntries)

	dlq := topicConfig(Config{TopicPartitions: 6, TopicReplication: 1}, "card.dlq")
	assert.Equal(t, []kafka.ConfigEntry{{ConfigName: "retention.ms", ConfigValue: "2592000000"}}, dlq.ConfigEntries)
}
