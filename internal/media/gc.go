package media

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

const (
	pendingTTL     = 24 * time.Hour // far past presignTTL, so no PUT can still be in flight
	pendingGCEvery = time.Hour
	pendingGCBatch = 500
)

var pendingPurged = promauto.NewCounter(prometheus.CounterOpts{
	Name: "mindmesh_media_pending_purged_total",
	Help: "Abandoned pending media rows deleted by the GC.",
})

// RunPendingGC removes abandoned uploads on start and then hourly; every replica may run it, deletes are guarded per row.
func (s *Service) RunPendingGC(ctx context.Context) error {
	t := time.NewTicker(pendingGCEvery)
	defer t.Stop()
	for {
		if n, err := s.purgePending(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("media: pending gc failed", zap.Int("purged", n), zap.Error(err))
		} else if n > 0 {
			s.log.Info("media: purged pending uploads", zap.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (s *Service) purgePending(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-pendingTTL)
	total := 0
	for {
		stale, err := s.repo.ListStalePending(ctx, cutoff, pendingGCBatch)
		if err != nil {
			return total, err
		}
		for _, p := range stale {
			// Row first: a confirm racing the GC then keeps both its row and its blob.
			key, err := s.repo.DeleteStalePending(ctx, p, cutoff)
			if err != nil {
				return total, err
			}
			if key == "" {
				continue
			}
			total++
			pendingPurged.Inc()
			if err := s.blob.Delete(ctx, s.bucket, key); err != nil {
				s.log.Warn("media: pending blob delete failed", zap.String("key", key), zap.Error(err))
			}
		}
		if len(stale) < pendingGCBatch {
			return total, nil
		}
	}
}
