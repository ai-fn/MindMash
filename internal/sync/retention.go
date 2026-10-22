package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

const (
	appliedOpsRetention   = 30 * 24 * time.Hour // older replays re-resolve against live state; clients don't retry that long
	appliedOpsPurgeEvery  = time.Hour
	appliedOpsPurgeWindow = 24 * time.Hour
	purgeStmtTimeout      = 30 * time.Second
	// Keeps replicas from running overlapping multi-shard DELETEs.
	appliedOpsPurgeLockKey int64 = 0x53796e634f705072
)

var (
	appliedOpsPurged = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mindmesh_sync_applied_ops_purged_total",
		Help: "Expired sync_applied_ops rows deleted by retention.",
	})
	appliedOpsPurgeLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mindmesh_sync_applied_ops_purge_last_success_timestamp_seconds",
		Help: "Unix time of the last completed applied-ops purge on this instance.",
	})
)

// RunAppliedOpsPurge purges expired op_ids on start and then hourly until ctx is done.
func RunAppliedOpsPurge(ctx context.Context, pool *pgxpool.Pool, log *zap.Logger) error {
	repo := NewPostgresRepository(pool)
	t := time.NewTicker(appliedOpsPurgeEvery)
	defer t.Stop()
	for {
		n, ran, err := purgeAppliedOpsOnce(ctx, pool, repo)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("sync: purge applied ops failed", zap.Int64("purged", n), zap.Error(err))
		case ran:
			appliedOpsPurgeLastSuccess.SetToCurrentTime()
			if n > 0 {
				log.Info("sync: purged applied ops", zap.Int64("count", n))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// purgeAppliedOpsOnce purges unless another replica holds the lock; ran reports whether it did.
func purgeAppliedOpsOnce(ctx context.Context, pool *pgxpool.Pool, repo *PostgresRepository) (int64, bool, error) {
	pc, err := pool.Acquire(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("sync: purge applied ops lock conn: %w", err)
	}
	// Hijacked so closing it always drops the session lock instead of returning it to the pool held.
	conn := pc.Hijack()
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()

	var locked bool
	if err := withStmtTimeout(ctx, func(ctx context.Context) error {
		return conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, appliedOpsPurgeLockKey).Scan(&locked)
	}); err != nil {
		return 0, false, fmt.Errorf("sync: purge applied ops lock: %w", err)
	}
	if !locked {
		return 0, false, nil
	}
	n, err := repo.PurgeAppliedOps(ctx, appliedOpsRetention, appliedOpsPurgeWindow)
	return n, err == nil, err
}

// PurgeAppliedOps deletes op_ids older than retention in applied_at windows, since Citus has no DELETE ... LIMIT.
func (r *PostgresRepository) PurgeAppliedOps(ctx context.Context, retention, window time.Duration) (int64, error) {
	var cutoff time.Time
	if err := withStmtTimeout(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx, `SELECT now() - make_interval(secs => $1)`, retention.Seconds()).Scan(&cutoff)
	}); err != nil {
		return 0, fmt.Errorf("sync: purge applied ops cutoff: %w", err)
	}

	var total int64
	for {
		var oldest *time.Time
		if err := withStmtTimeout(ctx, func(ctx context.Context) error {
			return r.db.QueryRow(ctx, `SELECT min(applied_at) FROM sync_applied_ops`).Scan(&oldest)
		}); err != nil {
			return total, fmt.Errorf("sync: purge applied ops oldest: %w", err)
		}
		if oldest == nil || !oldest.Before(cutoff) {
			return total, nil
		}
		upper := oldest.Add(window)
		if upper.After(cutoff) {
			upper = cutoff
		}

		var n int64
		if err := withStmtTimeout(ctx, func(ctx context.Context) error {
			tag, err := r.db.Exec(ctx, `DELETE FROM sync_applied_ops WHERE applied_at < $1`, upper)
			n = tag.RowsAffected()
			return err
		}); err != nil {
			// A burst can pack too many rows into one window; shrink it instead of stalling on it forever.
			if ctx.Err() != nil || window <= time.Second {
				return total, fmt.Errorf("sync: purge applied ops: %w", err)
			}
			window /= 2
			continue
		}
		total += n
		appliedOpsPurged.Add(float64(n))
	}
}

func withStmtTimeout(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, purgeStmtTimeout)
	defer cancel()
	return fn(ctx)
}
