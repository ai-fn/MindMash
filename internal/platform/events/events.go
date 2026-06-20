// Package events provides Kafka producer/consumer wrappers and the transactional
// outbox helper for cross-service async messaging.
package events

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
)

// Config holds Kafka client configuration.
type Config struct {
	Brokers      []string      // KAFKA_BROKERS comma-split, default ["localhost:9092"]
	GroupID      string        // KAFKA_GROUP_ID, default "mindmesh"
	ClientID     string        // KAFKA_CLIENT_ID, default "mindmesh-events"
	ReadTimeout  time.Duration // KAFKA_READ_TIMEOUT, default 10s
	WriteTimeout time.Duration // KAFKA_WRITE_TIMEOUT, default 10s
	MaxRetries   int           // KAFKA_MAX_RETRIES, default 3
}

// LoadConfig reads Kafka configuration from environment variables.
// It returns an error when KAFKA_BROKERS is explicitly set to an empty string,
// resulting in no usable broker addresses.
func LoadConfig() (Config, error) {
	brokers := []string{"localhost:9092"}
	if v, ok := os.LookupEnv("KAFKA_BROKERS"); ok {
		parts := strings.Split(v, ",")
		// Filter out empty strings that would arise from a blank value.
		var filtered []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			return Config{}, fmt.Errorf("events: KAFKA_BROKERS must not be empty")
		}
		brokers = filtered
	}

	readTimeout, err := envDuration("KAFKA_READ_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("events: invalid KAFKA_READ_TIMEOUT: %w", err)
	}

	writeTimeout, err := envDuration("KAFKA_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("events: invalid KAFKA_WRITE_TIMEOUT: %w", err)
	}

	maxRetries, err := envInt("KAFKA_MAX_RETRIES", 3)
	if err != nil {
		return Config{}, fmt.Errorf("events: invalid KAFKA_MAX_RETRIES: %w", err)
	}

	return Config{
		Brokers:      brokers,
		GroupID:      envStr("KAFKA_GROUP_ID", "mindmesh"),
		ClientID:     envStr("KAFKA_CLIENT_ID", "mindmesh-events"),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		MaxRetries:   maxRetries,
	}, nil
}

// Message is a single Kafka message exchanged between producer and consumer.
type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers map[string]string
}

// Producer is the publishing side of the Kafka interface.
type Producer interface {
	Publish(ctx context.Context, msgs ...Message) error
	Close() error
}

// Consumer is the subscribing side of the Kafka interface.
type Consumer interface {
	Subscribe(ctx context.Context, topics []string, handler func(ctx context.Context, msg Message) error) error
	Close() error
}

// KafkaProducer wraps a kafka-go Writer and implements Producer.
type KafkaProducer struct {
	writer *kafka.Writer
}

// NewProducer creates a KafkaProducer connected to the brokers in cfg.
func NewProducer(cfg Config) (*KafkaProducer, error) {
	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Balancer:     &kafka.LeastBytes{},
		WriteTimeout: cfg.WriteTimeout,
		MaxAttempts:  cfg.MaxRetries,
	}
	return &KafkaProducer{writer: w}, nil
}

// Publish sends one or more messages to their respective topics.
func (p *KafkaProducer) Publish(ctx context.Context, msgs ...Message) error {
	km := make([]kafka.Message, 0, len(msgs))
	for _, m := range msgs {
		headers := make([]kafka.Header, 0, len(m.Headers))
		for k, v := range m.Headers {
			headers = append(headers, kafka.Header{Key: k, Value: []byte(v)})
		}
		km = append(km, kafka.Message{
			Topic:   m.Topic,
			Key:     m.Key,
			Value:   m.Value,
			Headers: headers,
		})
	}
	if err := p.writer.WriteMessages(ctx, km...); err != nil {
		return fmt.Errorf("events: publish: %w", err)
	}
	return nil
}

// Close shuts down the underlying Kafka writer, flushing any buffered messages.
func (p *KafkaProducer) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("events: close producer: %w", err)
	}
	return nil
}

// KafkaConsumer wraps a kafka-go Reader and implements Consumer.
type KafkaConsumer struct {
	cfg Config
}

// NewConsumer creates a KafkaConsumer that will join the consumer group in cfg.
func NewConsumer(cfg Config) (*KafkaConsumer, error) {
	return &KafkaConsumer{cfg: cfg}, nil
}

// Subscribe starts a blocking read loop over topics, calling handler for each
// message. It commits the offset only after handler returns nil.  The loop
// exits when ctx is cancelled or a fatal read error occurs.
//
// A single topic uses kafka-go's plain Topic field; two or more use
// GroupTopics, which makes one Reader (one consumer-group member, one set of
// broker connections) consume all of them — callers needing several topics
// should pass them all in one Subscribe call rather than calling Subscribe
// once per topic, which would open a redundant reader per topic.
func (c *KafkaConsumer) Subscribe(ctx context.Context, topics []string, handler func(ctx context.Context, msg Message) error) (retErr error) {
	if len(topics) == 0 {
		return fmt.Errorf("events: subscribe: topics must not be empty")
	}

	readerCfg := kafka.ReaderConfig{
		Brokers:        c.cfg.Brokers,
		GroupID:        c.cfg.GroupID,
		ReadBackoffMax: c.cfg.ReadTimeout,
	}
	if len(topics) == 1 {
		readerCfg.Topic = topics[0]
	} else {
		readerCfg.GroupTopics = topics
	}
	r := kafka.NewReader(readerCfg)
	defer func() {
		if err := r.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("events: close reader: %w", err)
		}
	}()

	for {
		km, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Normal shutdown via context cancellation.
				return nil
			}
			return fmt.Errorf("events: fetch message: %w", err)
		}

		headers := make(map[string]string, len(km.Headers))
		for _, h := range km.Headers {
			headers[h.Key] = string(h.Value)
		}

		msg := Message{
			Topic:   km.Topic,
			Key:     km.Key,
			Value:   km.Value,
			Headers: headers,
		}

		if err := handler(ctx, msg); err != nil {
			// Do not commit; kafka-go will redeliver on next fetch.
			continue
		}

		if err := r.CommitMessages(ctx, km); err != nil {
			return fmt.Errorf("events: commit offset: %w", err)
		}
	}
}

// Close is a no-op because each Subscribe call owns its own reader lifetime.
func (c *KafkaConsumer) Close() error {
	return nil
}

// OutboxEntry is a single row written to the transactional outbox table.
type OutboxEntry struct {
	ID            string // ULID/UUID assigned by the caller
	UserID        string // tenant + Kafka partition key (Citus shard key)
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	CreatedAt     time.Time
}

// WriteOutbox inserts outbox entries into the outbox table within an existing
// transaction. Using the caller's tx keeps the domain write and the outbox
// append atomic, which is the core guarantee of the transactional outbox pattern.
// user_id is required: the outbox is sharded by it and the relay uses it as the
// Kafka partition key so a user's events stay ordered.
func WriteOutbox(ctx context.Context, tx pgx.Tx, entries []OutboxEntry) error {
	const q = `INSERT INTO outbox
		(id, user_id, aggregate_type, aggregate_id, event_type, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	for _, e := range entries {
		// user_id is the shard key + Kafka partition key; an empty value would
		// otherwise surface only as an opaque invalid-UUID error mid-transaction
		// and break per-user event ordering. Fail fast and clearly instead.
		if e.UserID == "" {
			return fmt.Errorf("events: write outbox entry %s: user_id is required", e.ID)
		}
		if _, err := tx.Exec(ctx, q,
			e.ID, e.UserID, e.AggregateType, e.AggregateID, e.EventType, e.Payload, e.CreatedAt,
		); err != nil {
			return fmt.Errorf("events: write outbox entry %s: %w", e.ID, err)
		}
	}
	return nil
}

// envStr returns the env var value for key, or def when the variable is unset.
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// envInt reads an integer from the environment variable key.
// Returns def when unset, and an error when the value cannot be parsed.
func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("events: %s must be an integer, got %q: %w", key, v, err)
	}
	return n, nil
}

// envDuration reads a duration string from the environment variable key.
// Returns def when unset, and an error when the value cannot be parsed.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("events: %s: cannot parse %q as duration: %w", key, v, err)
	}
	return d, nil
}
