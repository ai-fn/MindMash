package events

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Relay polls the outbox table for unpublished entries, publishes them to Kafka
// via a Producer, and marks them published.  It is designed to be run as a
// long-lived goroutine under an errgroup; it exits when ctx is cancelled.
//
// Kafka unavailability is non-fatal: the relay logs the error and backs off,
// so in-flight requests are never slowed by messaging downtime. Postgres
// unavailability causes the relay to back off the same way.
type Relay struct {
	db       *pgxpool.Pool
	producer Producer
	log      *zap.Logger
	store    string        // store name logged for tracing
	batch    int           // rows per poll cycle
	interval time.Duration // normal poll interval (no work)
	backoff  time.Duration // backoff interval after an error
}

// RelayConfig holds tuning parameters for the relay. Zero values fall back to
// sensible defaults.
type RelayConfig struct {
	Store    string        // identifies which service's outbox this relay polls (e.g. "collection")
	Batch    int           // max rows per poll; default 100
	Interval time.Duration // idle poll interval; default 1s
	Backoff  time.Duration // post-error backoff; default 5s
}

// NewRelay creates a Relay. pool and producer must be non-nil.
func NewRelay(pool *pgxpool.Pool, producer Producer, log *zap.Logger, cfg RelayConfig) *Relay {
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 5 * time.Second
	}
	return &Relay{
		db:       pool,
		producer: producer,
		log:      log,
		store:    cfg.Store,
		batch:    cfg.Batch,
		interval: cfg.Interval,
		backoff:  cfg.Backoff,
	}
}

// Run starts the relay loop. It returns nil on clean context cancellation and a
// non-nil error only on an unrecoverable internal fault.
func (r *Relay) Run(ctx context.Context) error {
	r.log.Info("relay: starting", zap.String("store", r.store))
	for {
		n, err := r.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			r.log.Error("relay: poll error", zap.String("store", r.store), zap.Error(err))
			if !relaySleep(ctx, r.backoff) {
				return nil
			}
			continue
		}
		if n > 0 {
			// More work may be waiting; skip the sleep to keep throughput high.
			continue
		}
		if !relaySleep(ctx, r.interval) {
			return nil
		}
	}
}

// poll fetches one batch of unpublished outbox entries, publishes them, and
// marks them published.  Returns the number of entries processed.
//
// The SELECT FOR UPDATE SKIP LOCKED and the mark UPDATE both run inside the
// same Postgres transaction so that advisory row locks are held across the
// Publish call.  This prevents a concurrent relay instance from picking the
// same batch before the first one marks it published.
func (r *Relay) poll(ctx context.Context) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("relay: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		SELECT id, user_id, aggregate_type, aggregate_id, event_type, payload
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	type outboxRow struct {
		id            string
		userID        string
		aggregateType string
		aggregateID   string
		eventType     string
		payload       []byte
	}

	rows, err := tx.Query(ctx, q, r.batch)
	if err != nil {
		return 0, fmt.Errorf("relay: query outbox: %w", err)
	}
	pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (outboxRow, error) {
		var ro outboxRow
		err := row.Scan(&ro.id, &ro.userID, &ro.aggregateType, &ro.aggregateID, &ro.eventType, &ro.payload)
		return ro, err
	})
	if err != nil {
		return 0, fmt.Errorf("relay: scan outbox rows: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	msgs := make([]Message, 0, len(pending))
	userIDs := make([]string, len(pending))
	ids := make([]string, len(pending))
	for i, ro := range pending {
		userIDs[i] = ro.userID
		ids[i] = ro.id
		msgs = append(msgs, Message{
			Topic: ro.aggregateType,
			Key:   []byte(ro.userID), // partition by user_id for per-user ordering
			Value: ro.payload,
			Headers: map[string]string{
				"event_id":       ro.id, // stable dedup key for idempotent consumers
				"event_type":     ro.eventType,
				"aggregate_type": ro.aggregateType,
				"aggregate_id":   ro.aggregateID,
			},
		})
	}

	// Publish while the tx holds the row locks, preventing concurrent relay
	// instances from picking the same batch.
	if err := r.producer.Publish(ctx, msgs...); err != nil {
		return 0, fmt.Errorf("relay: publish %d messages: %w", len(msgs), err)
	}

	// Mark published with (user_id, id) pairs so Citus can route each row to
	// its shard without a scatter-gather.
	const mark = `
		UPDATE outbox SET published_at = now()
		FROM (SELECT unnest($1::text[]) AS uid, unnest($2::text[]) AS eid) AS t
		WHERE outbox.user_id = t.uid AND outbox.id = t.eid`
	if _, err := tx.Exec(ctx, mark, userIDs, ids); err != nil {
		r.log.Error("relay: mark published failed — messages may duplicate on retry",
			zap.String("store", r.store),
			zap.Error(err),
		)
		return 0, fmt.Errorf("relay: mark published: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("relay: commit: %w", err)
	}

	r.log.Debug("relay: published", zap.String("store", r.store), zap.Int("count", len(pending)))
	return len(pending), nil
}

// relaySleep blocks for d or until ctx is cancelled. Returns false when cancelled.
func relaySleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
