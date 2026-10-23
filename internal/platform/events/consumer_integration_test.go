//go:build integration

package events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

type seen struct {
	mu   sync.Mutex
	vals []string
}

func (s *seen) add(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals = append(s.vals, v)
}

func (s *seen) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.vals...)
}

func newTestConsumer(t *testing.T, broker, group string, maxAttempts int) *KafkaConsumer {
	t.Helper()
	c, err := NewConsumer(Config{
		Brokers: []string{broker}, GroupID: group, ReadTimeout: time.Second,
		WriteTimeout: 5 * time.Second, MaxRetries: 3, HandlerMaxAttempts: maxAttempts,
	}, zap.NewNop())
	require.NoError(t, err)
	c.retryBase, c.retryMax = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func runConsumer(t *testing.T, c *KafkaConsumer, topic string, h func(context.Context, Message) error) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Subscribe(ctx, []string{topic}, h) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			assert.NoError(t, <-done)
		})
	}
	t.Cleanup(stop)
	return stop
}

// waitCommitted blocks until group has committed everything currently in topic's partition 0.
func waitCommitted(t *testing.T, broker, group, topic string) {
	t.Helper()
	ctx := context.Background()
	conn, err := kafka.DialLeader(ctx, "tcp", broker, topic, 0)
	require.NoError(t, err)
	hw, err := conn.ReadLastOffset()
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	client := &kafka.Client{Addr: kafka.TCP(broker)}
	require.Eventually(t, func() bool {
		resp, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: {0}}})
		if err != nil || len(resp.Topics[topic]) == 0 {
			return false
		}
		return resp.Topics[topic][0].CommittedOffset == hw
	}, 30*time.Second, 50*time.Millisecond)
}

func TestKafkaConsumer(t *testing.T) {
	ctx := context.Background()
	const topic = "card"
	broker := startBroker(t, ctx, topic, 1)
	createTopic(t, broker, topic+".dlq", 1)
	p, err := NewProducer(Config{Brokers: []string{broker}, WriteTimeout: 5 * time.Second, MaxRetries: 3})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	publish := func(vals ...string) {
		msgs := make([]Message, len(vals))
		for i, v := range vals {
			msgs[i] = Message{Topic: topic, Key: []byte("u1"), Value: []byte(v), Headers: map[string]string{"event_id": v}}
		}
		require.NoError(t, p.Publish(ctx, msgs...))
	}

	t.Run("transient failure is retried before the next message", func(t *testing.T) {
		group := "g-" + uuid.NewString()
		a, b := "a-"+uuid.NewString(), "b-"+uuid.NewString()
		publish(a, b)

		var got seen
		fails := 2
		stop := runConsumer(t, newTestConsumer(t, broker, group, 5), topic, func(_ context.Context, m Message) error {
			v := string(m.Value)
			if v != a && v != b {
				return nil
			}
			got.add(v)
			if v == a && fails > 0 {
				fails--
				return errors.New("opensearch down")
			}
			return nil
		})
		require.Eventually(t, func() bool { return len(got.get()) == 4 }, 30*time.Second, 50*time.Millisecond)
		waitCommitted(t, broker, group, topic)
		stop()
		assert.Equal(t, []string{a, a, a, b}, got.get())
	})

	t.Run("exhausted retries dead-letter and commit", func(t *testing.T) {
		sr := testhelpers.RecordSpans(t)
		group := "g-" + uuid.NewString()
		bad, good := "bad-"+uuid.NewString(), "good-"+uuid.NewString()
		publish(bad, good)

		var got seen
		stop := runConsumer(t, newTestConsumer(t, broker, group, 3), topic, func(_ context.Context, m Message) error {
			v := string(m.Value)
			if v == bad {
				got.add(v)
				return errors.New("always fails")
			}
			if v == good {
				got.add(v)
			}
			return nil
		})
		require.Eventually(t, func() bool { return len(got.get()) == 4 }, 30*time.Second, 50*time.Millisecond)
		waitCommitted(t, broker, group, topic)
		stop()
		assert.Equal(t, []string{bad, bad, bad, good}, got.get())
		status := map[string]codes.Code{}
		for _, sp := range sr.Ended() {
			for _, a := range sp.Attributes() {
				if a.Key == "messaging.message.id" {
					status[a.Value.AsString()] = sp.Status().Code
				}
			}
		}
		assert.Equal(t, codes.Error, status[bad], "the dead-lettered message's span must be an error")
		assert.Equal(t, codes.Unset, status[good])

		deadCh := make(chan Message, 1)
		stopDLQ := runConsumer(t, newTestConsumer(t, broker, "dlq-"+uuid.NewString(), 1), topic+".dlq", func(_ context.Context, m Message) error {
			if string(m.Value) == bad {
				select {
				case deadCh <- m:
				default:
				}
			}
			return nil
		})
		var deadMsg Message
		select {
		case deadMsg = <-deadCh:
		case <-time.After(30 * time.Second):
			t.Fatal("dead-lettered message never arrived")
		}
		stopDLQ()
		assert.Equal(t, []byte("u1"), deadMsg.Key)
		assert.Equal(t, bad, deadMsg.Headers["event_id"])
		assert.Equal(t, "always fails", deadMsg.Headers["dlq_error"])
		assert.Equal(t, topic, deadMsg.Headers["dlq_topic"])
		assert.Equal(t, group, deadMsg.Headers["dlq_group"])
		assert.NotEmpty(t, deadMsg.Headers["dlq_offset"])

		// Same group again: both offsets were committed, so only the new message arrives.
		next := "next-" + uuid.NewString()
		publish(next)
		var again seen
		stopAgain := runConsumer(t, newTestConsumer(t, broker, group, 3), topic, func(_ context.Context, m Message) error {
			again.add(string(m.Value))
			return nil
		})
		require.Eventually(t, func() bool { return len(again.get()) >= 1 }, 30*time.Second, 50*time.Millisecond)
		waitCommitted(t, broker, group, topic)
		stopAgain()
		assert.Equal(t, []string{next}, again.get())
	})
}
