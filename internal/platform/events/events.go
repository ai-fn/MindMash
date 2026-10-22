// Package events provides Kafka producer/consumer wrappers and the transactional
// outbox helper for cross-service async messaging.
package events

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// Config holds Kafka client configuration.
type Config struct {
	Brokers      []string      // KAFKA_BROKERS comma-split, default ["localhost:9092"]
	GroupID      string        // KAFKA_GROUP_ID; no default, NewConsumer requires it
	ClientID     string        // KAFKA_CLIENT_ID, default "mindmesh-events"
	ReadTimeout  time.Duration // KAFKA_READ_TIMEOUT, default 10s
	WriteTimeout time.Duration // KAFKA_WRITE_TIMEOUT, default 10s
	MaxRetries   int           // KAFKA_MAX_RETRIES, default 3

	HandlerMaxAttempts int // KAFKA_HANDLER_MAX_ATTEMPTS, default 10; then the message is dead-lettered
}

const defaultHandlerMaxAttempts = 10

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

	handlerAttempts, err := envInt("KAFKA_HANDLER_MAX_ATTEMPTS", defaultHandlerMaxAttempts)
	if err != nil {
		return Config{}, fmt.Errorf("events: invalid KAFKA_HANDLER_MAX_ATTEMPTS: %w", err)
	}
	if handlerAttempts < 1 {
		return Config{}, errors.New("events: invalid KAFKA_HANDLER_MAX_ATTEMPTS: must be >= 1")
	}

	return Config{
		Brokers:      brokers,
		GroupID:      envStr("KAFKA_GROUP_ID", ""),
		ClientID:     envStr("KAFKA_CLIENT_ID", "mindmesh-events"),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		MaxRetries:   maxRetries,

		HandlerMaxAttempts: handlerAttempts,
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
	return &KafkaProducer{writer: newWriter(cfg)}, nil
}

func newWriter(cfg Config) *kafka.Writer {
	return &kafka.Writer{
		Addr: kafka.TCP(cfg.Brokers...),
		// key = user_id: one partition per user keeps their events ordered; murmur2 matches Java clients.
		Balancer:     &kafka.Murmur2Balancer{},
		RequiredAcks: kafka.RequireAll,
		// Publish is synchronous; the 1s default would stall every relay batch.
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: cfg.WriteTimeout,
		MaxAttempts:  cfg.MaxRetries,
	}
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
	cfg       Config
	log       *zap.Logger
	dlq       *kafka.Writer
	retryBase time.Duration
	retryMax  time.Duration
}

// NewConsumer creates a KafkaConsumer that will join the consumer group in cfg.
func NewConsumer(cfg Config, log *zap.Logger) (*KafkaConsumer, error) {
	// A shared fallback group would let unrelated services split each other's messages.
	if cfg.GroupID == "" {
		return nil, errors.New("events: consumer group id is required")
	}
	if cfg.HandlerMaxAttempts < 1 {
		cfg.HandlerMaxAttempts = defaultHandlerMaxAttempts
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &KafkaConsumer{
		cfg:       cfg,
		log:       log,
		dlq:       newWriter(cfg),
		retryBase: 200 * time.Millisecond,
		retryMax:  30 * time.Second,
	}, nil
}

// Subscribe starts a blocking read loop over topics, calling handler for each
// message. A failing message is retried in place with backoff (keeping per-key
// order), then dead-lettered to "<topic>.dlq"; its offset is committed only after
// one of those succeeds. The loop exits when ctx is cancelled or a fatal read error occurs.
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

		// process only fails on shutdown; the uncommitted message is redelivered after restart.
		if err := c.process(ctx, km, msg, handler); err != nil {
			return nil
		}

		if err := r.CommitMessages(ctx, km); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("events: commit offset: %w", err)
		}
	}
}

func (c *KafkaConsumer) process(ctx context.Context, km kafka.Message, msg Message, handler func(context.Context, Message) error) error {
	backoff := c.retryBase
	for attempt := 1; ; attempt++ {
		err := handler(ctx, msg)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		consumerHandlerFailures.WithLabelValues(km.Topic, c.cfg.GroupID).Inc()
		fields := []zap.Field{
			zap.String("topic", km.Topic), zap.Int("partition", km.Partition), zap.Int64("offset", km.Offset),
			zap.String("event_id", msg.Headers["event_id"]), zap.Int("attempt", attempt), zap.Error(err),
		}
		if attempt >= c.cfg.HandlerMaxAttempts {
			c.log.Error("events: handler failed, dead-lettering", fields...)
			return c.deadLetter(ctx, km, err)
		}
		c.log.Warn("events: handler failed, retrying", fields...)
		if !relaySleep(ctx, backoff) {
			return ctx.Err()
		}
		backoff = min(backoff*2, c.retryMax)
	}
}

const maxDLQErrorLen = 1024

func dlqMessage(km kafka.Message, cause error, group string) kafka.Message {
	headers := slices.DeleteFunc(slices.Clone(km.Headers), func(h kafka.Header) bool {
		return strings.HasPrefix(h.Key, "dlq_")
	})
	errText := cause.Error()
	if len(errText) > maxDLQErrorLen {
		// An oversized header would make the DLQ write fail forever.
		errText = strings.ToValidUTF8(errText[:maxDLQErrorLen], "")
	}
	headers = append(headers,
		kafka.Header{Key: "dlq_error", Value: []byte(errText)},
		kafka.Header{Key: "dlq_topic", Value: []byte(km.Topic)},
		kafka.Header{Key: "dlq_partition", Value: []byte(strconv.Itoa(km.Partition))},
		kafka.Header{Key: "dlq_offset", Value: []byte(strconv.FormatInt(km.Offset, 10))},
		kafka.Header{Key: "dlq_group", Value: []byte(group)},
	)
	return kafka.Message{Topic: km.Topic + ".dlq", Key: km.Key, Value: km.Value, Headers: headers}
}

// deadLetter retries until the DLQ write lands: committing without it would lose the message.
func (c *KafkaConsumer) deadLetter(ctx context.Context, km kafka.Message, cause error) error {
	dm := dlqMessage(km, cause, c.cfg.GroupID)
	backoff := c.retryBase
	for {
		err := c.dlq.WriteMessages(ctx, dm)
		if err == nil {
			consumerDeadLettered.WithLabelValues(km.Topic, c.cfg.GroupID).Inc()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		consumerDLQWriteFailures.WithLabelValues(km.Topic, c.cfg.GroupID).Inc()
		c.log.Error("events: dead-letter publish failed", zap.String("topic", dm.Topic), zap.Int64("offset", km.Offset), zap.Error(err))
		if !relaySleep(ctx, backoff) {
			return ctx.Err()
		}
		backoff = min(backoff*2, c.retryMax)
	}
}

// Close releases the dead-letter writer; each Subscribe owns its reader.
func (c *KafkaConsumer) Close() error {
	if err := c.dlq.Close(); err != nil {
		return fmt.Errorf("events: close dlq writer: %w", err)
	}
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
