//go:build integration

package events

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
)

func startBroker(t *testing.T, ctx context.Context, topic string, partitions int) string {
	t.Helper()
	broker := startRedpanda(t, ctx)
	createTopic(t, broker, topic, partitions)
	return broker
}

func startRedpanda(t *testing.T, ctx context.Context) string {
	t.Helper()
	rp, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:v24.1.1")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := rp.Terminate(ctx); err != nil {
			t.Errorf("cleanup: terminate redpanda: %v", err)
		}
	})
	broker, err := rp.KafkaSeedBroker(ctx)
	require.NoError(t, err)
	return broker
}

func createTopic(t *testing.T, broker, topic string, partitions int) {
	t.Helper()
	conn, err := kafka.Dial("tcp", broker)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	controller, err := conn.Controller()
	require.NoError(t, err)
	cc, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	require.NoError(t, err)
	defer func() { _ = cc.Close() }()
	require.NoError(t, cc.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: partitions, ReplicationFactor: 1}))
}

func readPartition(t *testing.T, ctx context.Context, broker, topic string, partition int) []kafka.Message {
	t.Helper()
	conn, err := kafka.DialLeader(ctx, "tcp", broker, topic, partition)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	last, err := conn.ReadLastOffset()
	require.NoError(t, err)
	var out []kafka.Message
	for int64(len(out)) < last {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		m, err := conn.ReadMessage(10 << 20)
		require.NoError(t, err)
		out = append(out, m)
	}
	return out
}

func TestKafkaProducerKeysToOnePartitionInOrder(t *testing.T) {
	ctx := context.Background()
	const topic, partitions, keys, perKey = "card", 8, 6, 30
	broker := startBroker(t, ctx, topic, partitions)

	p, err := NewProducer(Config{Brokers: []string{broker}, WriteTimeout: 10 * time.Second, MaxRetries: 3})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	start := time.Now()
	require.NoError(t, p.Publish(ctx, Message{Topic: topic, Key: []byte("warmup"), Value: []byte("0")}))
	t.Logf("first publish took %s", time.Since(start))

	msgs := make([]Message, 0, keys*perKey)
	for i := range perKey {
		for k := range keys {
			msgs = append(msgs, Message{Topic: topic, Key: []byte(fmt.Sprintf("user-%d", k)), Value: []byte(strconv.Itoa(i))})
		}
	}
	start = time.Now()
	require.NoError(t, p.Publish(ctx, msgs...))
	assert.Less(t, time.Since(start), 500*time.Millisecond, "a sync publish must not wait out BatchTimeout")

	partitionsByKey := map[string]map[int]bool{}
	seqByKey := map[string][]int{}
	total := 0
	for part := range partitions {
		for _, m := range readPartition(t, ctx, broker, topic, part) {
			k := string(m.Key)
			if k == "warmup" {
				continue
			}
			if partitionsByKey[k] == nil {
				partitionsByKey[k] = map[int]bool{}
			}
			partitionsByKey[k][part] = true
			v, err := strconv.Atoi(string(m.Value))
			require.NoError(t, err)
			seqByKey[k] = append(seqByKey[k], v)
			total++
		}
	}
	assert.Equal(t, keys*perKey, total)
	for k, parts := range partitionsByKey {
		assert.Len(t, parts, 1, "key %s spread over partitions %v", k, parts)
		assert.IsIncreasing(t, seqByKey[k], "key %s out of order", k)
	}
}
