package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

const (
	expiredRetention = 24 * time.Hour // expired rows are already unusable; the delay only eases debugging
	purgeEvery       = time.Hour
	purgeBatch       = 5000
	purgeStmtTimeout = 30 * time.Second
)

var (
	rowsPurged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_identity_expired_rows_purged_total",
		Help: "Expired refresh_tokens / email_verifications rows deleted by retention.",
	}, []string{"table"})
	// Starts at process start so the stale-purge alert gives a new pod 3h, not 0s.
	purgeLastSuccess = observability.NewStartedGauge(prometheus.GaugeOpts{
		Name: "mindmesh_identity_purge_last_success_timestamp_seconds",
		Help: "Unix time of the last completed expired-row purge on this instance (process start until the first).",
	})
)

// RunPurge deletes expired tokens on start and then hourly until ctx is done.
func RunPurge(ctx context.Context, repo *PostgresRepository, log *zap.Logger) error {
	t := time.NewTicker(purgeEvery)
	defer t.Stop()
	for {
		tokens, verifications, err := repo.PurgeExpired(ctx, time.Now().Add(-expiredRetention))
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("identity: purge expired rows failed", zap.Error(err))
		case err == nil:
			purgeLastSuccess.SetToCurrentTime()
			if tokens+verifications > 0 {
				log.Info("identity: purged expired rows", zap.Int64("refresh_tokens", tokens), zap.Int64("email_verifications", verifications))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// PurgeExpired deletes rows that expired before cutoff in batches. SKIP LOCKED lets replicas run it concurrently.
func (r *PostgresRepository) PurgeExpired(ctx context.Context, cutoff time.Time) (tokens, verifications int64, err error) {
	if tokens, err = r.purgeTable(ctx, "refresh_tokens", "jti", cutoff); err != nil {
		return tokens, 0, err
	}
	verifications, err = r.purgeTable(ctx, "email_verifications", "token_hash", cutoff)
	return tokens, verifications, err
}

func (r *PostgresRepository) purgeTable(ctx context.Context, table, key string, cutoff time.Time) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %[1]s WHERE %[2]s IN (
			SELECT %[2]s FROM %[1]s WHERE expires_at < $1 LIMIT %[3]d FOR UPDATE SKIP LOCKED)`, table, key, purgeBatch)
	var total int64
	for {
		sctx, cancel := context.WithTimeout(ctx, purgeStmtTimeout)
		tag, err := r.db.Exec(sctx, query, cutoff)
		cancel()
		if err != nil {
			return total, fmt.Errorf("identity: purge %s: %w", table, err)
		}
		total += tag.RowsAffected()
		rowsPurged.WithLabelValues(table).Add(float64(tag.RowsAffected()))
		if tag.RowsAffected() < purgeBatch {
			return total, nil
		}
	}
}
