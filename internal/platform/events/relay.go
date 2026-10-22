package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Relay publishes unpublished outbox rows to Kafka and marks them published.
// Only the instance holding relayLockKey polls; the others stand by.
type Relay struct {
	db       *pgxpool.Pool
	producer Producer
	log      *zap.Logger
	store    string        // store name logged for tracing
	batch    int           // rows per poll cycle
	interval time.Duration // normal poll interval (no work)
	backoff  time.Duration // backoff interval after an error

	retention   time.Duration
	purgeEvery  time.Duration
	purgeWindow time.Duration
}

// RelayConfig holds tuning parameters for the relay. Zero values fall back to
// sensible defaults.
type RelayConfig struct {
	Store    string        // identifies which service's outbox this relay polls (e.g. "collection")
	Batch    int           // max rows per poll; default 100, capped at maxRelayBatch
	Interval time.Duration // idle poll interval; default 1s
	Backoff  time.Duration // post-error backoff; default 5s

	Retention  time.Duration // published rows older than this are deleted; default 24h
	PurgeEvery time.Duration // default 5m
}

// NewRelay creates a Relay. pool and producer must be non-nil.
func NewRelay(pool *pgxpool.Pool, producer Producer, log *zap.Logger, cfg RelayConfig) *Relay {
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	cfg.Batch = min(cfg.Batch, maxRelayBatch)
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 5 * time.Second
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 24 * time.Hour
	}
	if cfg.PurgeEvery <= 0 {
		cfg.PurgeEvery = 5 * time.Minute
	}
	relayLeader.WithLabelValues(cfg.Store).Set(0)
	return &Relay{
		db:       pool,
		producer: producer,
		log:      log,
		store:    cfg.Store,
		batch:    cfg.Batch,
		interval: cfg.Interval,
		backoff:  cfg.Backoff,

		retention:   cfg.Retention,
		purgeEvery:  cfg.PurgeEvery,
		purgeWindow: time.Minute,
	}
}

const (
	// relayLockKey is DB-wide: services sharing one outbox table elect a single relay.
	relayLockKey int64 = 0x48644b466c502486
	// maxRelayBatch bounds the mark UPDATE, whose ANY x ANY index probe grows quadratically.
	maxRelayBatch  = 500
	relayOpTimeout = 30 * time.Second
)

// relayKeepalive makes the server drop a half-open leader session (and its lock) in ~1m, not hours.
var relayKeepalive = map[string]string{
	"tcp_keepalives_idle":     "30",
	"tcp_keepalives_interval": "10",
	"tcp_keepalives_count":    "3",
	"tcp_user_timeout":        "60000",
}

// Run competes for leadership and relays until ctx is cancelled; it always returns nil.
func (r *Relay) Run(ctx context.Context) error {
	r.log.Info("relay: starting", zap.String("store", r.store))
	for {
		if err := r.lead(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("relay: leader loop failed", zap.String("store", r.store), zap.Error(err))
		}
		if !relaySleep(ctx, r.backoff) {
			return nil
		}
	}
}

// lead waits for the relay lock, then polls while holding it. The lock is session-scoped on a
// dedicated connection (DB_DSN must be direct, not via PgBouncer), so it dies with the session.
func (r *Relay) lead(ctx context.Context) error {
	cfg := r.db.Config().ConnConfig
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	for k, v := range relayKeepalive {
		cfg.RuntimeParams[k] = v
	}
	var conn *pgx.Conn
	err := r.withTimeout(ctx, func(ctx context.Context) (err error) {
		conn, err = pgx.ConnectConfig(ctx, cfg)
		return err
	})
	if err != nil {
		return fmt.Errorf("relay: connect: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx)) //nolint:errcheck

	for {
		var leader bool
		if err := r.withTimeout(ctx, func(ctx context.Context) error {
			return conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, relayLockKey).Scan(&leader)
		}); err != nil {
			return fmt.Errorf("relay: try lock: %w", err)
		}
		if leader {
			break
		}
		if !relaySleep(ctx, r.backoff) {
			return nil
		}
	}
	r.log.Info("relay: acquired leadership", zap.String("store", r.store))
	relayLeader.WithLabelValues(r.store).Set(1)
	defer func() {
		relayLeader.WithLabelValues(r.store).Set(0)
		outboxOldestAge.DeleteLabelValues(r.store)
	}()

	// Purge runs on pool conns: a timed-out DELETE must not close the lock session.
	purgeCtx, stopPurge := context.WithCancel(ctx)
	purgeDone := make(chan struct{})
	go func() { defer close(purgeDone); r.purgeLoop(purgeCtx) }()
	defer func() { stopPurge(); <-purgeDone }()

	for {
		var n int
		err := r.withTimeout(ctx, func(ctx context.Context) (err error) {
			n, err = r.poll(ctx, conn)
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if pingErr := r.withTimeout(ctx, conn.Ping); pingErr != nil {
				return errors.Join(err, pingErr)
			}
			r.log.Error("relay: poll error", zap.String("store", r.store), zap.Error(err))
			if !relaySleep(ctx, r.backoff) {
				return nil
			}
			continue
		}
		if n > 0 {
			continue
		}
		if !relaySleep(ctx, r.interval) {
			return nil
		}
	}
}

func (r *Relay) withTimeout(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, relayOpTimeout)
	defer cancel()
	return fn(ctx)
}

// poll publishes one batch of unpublished entries and marks them. Delivery is
// at-least-once: a failed mark re-sends the batch, consumers dedupe by event_id.
func (r *Relay) poll(ctx context.Context, conn *pgx.Conn) (int, error) {
	const q = `
		SELECT id, user_id, aggregate_type, aggregate_id, event_type, payload,
		       EXTRACT(EPOCH FROM now() - created_at)::float8
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1`

	type outboxRow struct {
		id            string
		userID        string
		aggregateType string
		aggregateID   string
		eventType     string
		payload       []byte
		ageSeconds    float64
	}

	rows, err := conn.Query(ctx, q, r.batch)
	if err != nil {
		return 0, fmt.Errorf("relay: query outbox: %w", err)
	}
	pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (outboxRow, error) {
		var ro outboxRow
		err := row.Scan(&ro.id, &ro.userID, &ro.aggregateType, &ro.aggregateID, &ro.eventType, &ro.payload, &ro.ageSeconds)
		return ro, err
	})
	if err != nil {
		return 0, fmt.Errorf("relay: scan outbox rows: %w", err)
	}
	if len(pending) == 0 {
		outboxOldestAge.WithLabelValues(r.store).Set(0)
		return 0, nil
	}
	outboxOldestAge.WithLabelValues(r.store).Set(max(0, pending[0].ageSeconds))

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

	if err := r.producer.Publish(ctx, msgs...); err != nil {
		return 0, fmt.Errorf("relay: publish %d messages: %w", len(msgs), err)
	}

	// ANY on user_id lets Citus prune shards; published_at guards the cross product if ids ever collide.
	const mark = `
		UPDATE outbox SET published_at = now()
		WHERE user_id = ANY($1::uuid[]) AND id = ANY($2::uuid[]) AND published_at IS NULL`
	// Mark even on shutdown: the batch is already in Kafka.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayOpTimeout)
	defer cancel()
	tag, err := conn.Exec(mctx, mark, userIDs, ids)
	if err != nil {
		return 0, fmt.Errorf("relay: mark %d published (will re-send): %w", len(ids), err)
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return 0, fmt.Errorf("relay: marked %d of %d published rows (will re-send)", tag.RowsAffected(), len(ids))
	}

	outboxPublished.WithLabelValues(r.store).Add(float64(len(pending)))
	r.log.Debug("relay: published", zap.String("store", r.store), zap.Int("count", len(pending)))
	return len(pending), nil
}

func (r *Relay) purgeLoop(ctx context.Context) {
	t := time.NewTicker(r.purgeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := r.purge(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("relay: purge failed", zap.String("store", r.store), zap.Error(err))
		}
	}
}

// purge deletes expired published rows in published_at windows, since Citus has no DELETE ... LIMIT.
func (r *Relay) purge(ctx context.Context) (int64, error) {
	var cutoff time.Time
	if err := r.withTimeout(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx, `SELECT now() - make_interval(secs => $1)`, r.retention.Seconds()).Scan(&cutoff)
	}); err != nil {
		return 0, fmt.Errorf("relay: purge cutoff: %w", err)
	}

	var total int64
	window := r.purgeWindow
	for {
		var oldest *time.Time
		if err := r.withTimeout(ctx, func(ctx context.Context) error {
			return r.db.QueryRow(ctx, `SELECT min(published_at) FROM outbox WHERE published_at IS NOT NULL`).Scan(&oldest)
		}); err != nil {
			return total, fmt.Errorf("relay: purge oldest: %w", err)
		}
		if oldest == nil || !oldest.Before(cutoff) {
			return total, nil
		}
		upper := oldest.Add(window)
		if upper.After(cutoff) {
			upper = cutoff
		}

		var n int64
		if err := r.withTimeout(ctx, func(ctx context.Context) error {
			tag, err := r.db.Exec(ctx, `DELETE FROM outbox WHERE published_at < $1`, upper)
			n = tag.RowsAffected()
			return err
		}); err != nil {
			// A burst can pack too many rows into one window; shrink it instead of stalling on it forever.
			if ctx.Err() != nil || window <= time.Second {
				return total, fmt.Errorf("relay: purge outbox: %w", err)
			}
			window /= 2
			continue
		}
		total += n
		outboxPurged.WithLabelValues(r.store).Add(float64(n))
	}
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
