//go:build integration

package events

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/zap"
)

type recordingProducer struct {
	mu   sync.Mutex
	msgs []Message
	err  error
}

func (p *recordingProducer) Publish(_ context.Context, msgs ...Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.msgs = append(p.msgs, msgs...)
	return nil
}

func (p *recordingProducer) Close() error { return nil }

func (p *recordingProducer) snapshot() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Message(nil), p.msgs...)
}

func startOutboxDB(t *testing.T, ctx context.Context, image string, citus bool) *pgxpool.Pool {
	t.Helper()
	pgc, err := postgres.Run(ctx,
		image,
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := pgc.Terminate(ctx); err != nil {
			t.Errorf("cleanup: terminate postgres container: %v", err)
		}
	})
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	apply := func(f string) {
		ddl, err := os.ReadFile("../../../migrations/collection/" + f)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, string(ddl))
		require.NoError(t, err)
	}
	apply("000011_outbox.up.sql")
	if citus {
		_, err = pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS citus`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `SELECT create_distributed_table('outbox', 'user_id')`)
		require.NoError(t, err)
	}
	apply("000014_outbox_published.up.sql")
	return pool
}

func seedOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) []OutboxEntry {
	t.Helper()
	entries := make([]OutboxEntry, n)
	for i := range entries {
		entries[i] = OutboxEntry{
			ID:            uuid.NewString(),
			UserID:        uuid.NewString(),
			AggregateType: "card",
			AggregateID:   uuid.NewString(),
			EventType:     "card.created",
			Payload:       []byte(`{}`),
			CreatedAt:     time.Now().Add(time.Duration(i) * time.Millisecond),
		}
	}
	require.NoError(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return WriteOutbox(ctx, tx, entries)
	}))
	return entries
}

func countUnpublished(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n))
	return n
}

func connect(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *pgx.Conn {
	t.Helper()
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestRelay(t *testing.T) {
	dbs := []struct {
		name  string
		image string
		citus bool
	}{
		{"postgres", "postgres:18-alpine", false},
		{"citus", "citusdata/citus:13.0", true},
	}
	for _, db := range dbs {
		t.Run(db.name, func(t *testing.T) {
			ctx := context.Background()
			pool := startOutboxDB(t, ctx, db.image, db.citus)
			reset := func(t *testing.T) {
				_, err := pool.Exec(ctx, `TRUNCATE outbox`)
				require.NoError(t, err)
			}

			t.Run("poll publishes and marks", func(t *testing.T) {
				reset(t)
				entries := seedOutbox(t, ctx, pool, 3)
				prod := &recordingProducer{}
				r := NewRelay(pool, prod, zap.NewNop(), RelayConfig{Store: "test"})
				conn := connect(t, ctx, pool)

				n, err := r.poll(ctx, conn)
				require.NoError(t, err)
				assert.Equal(t, 3, n)
				msgs := prod.snapshot()
				require.Len(t, msgs, 3)
				for i, e := range entries {
					assert.Equal(t, e.AggregateType, msgs[i].Topic)
					assert.Equal(t, []byte(e.UserID), msgs[i].Key)
					assert.Equal(t, e.ID, msgs[i].Headers["event_id"])
					assert.Equal(t, e.EventType, msgs[i].Headers["event_type"])
				}
				assert.Zero(t, countUnpublished(t, ctx, pool))

				n, err = r.poll(ctx, conn)
				require.NoError(t, err)
				assert.Zero(t, n, "published rows must not be re-sent")
			})

			t.Run("publish failure leaves rows unpublished", func(t *testing.T) {
				reset(t)
				seedOutbox(t, ctx, pool, 2)
				prod := &recordingProducer{err: errors.New("kafka down")}
				r := NewRelay(pool, prod, zap.NewNop(), RelayConfig{Store: "test"})

				_, err := r.poll(ctx, connect(t, ctx, pool))
				require.Error(t, err)
				assert.Equal(t, 2, countUnpublished(t, ctx, pool))
			})

			t.Run("single leader then failover", func(t *testing.T) {
				reset(t)
				cfg := RelayConfig{Store: "test", Interval: 20 * time.Millisecond, Backoff: 50 * time.Millisecond}
				prodA, prodB := &recordingProducer{}, &recordingProducer{}
				ctxA, cancelA := context.WithCancel(ctx)
				ctxB, cancelB := context.WithCancel(ctx)
				t.Cleanup(cancelA)
				t.Cleanup(cancelB)
				doneA := make(chan struct{})
				doneB := make(chan struct{})
				go func() { defer close(doneA); _ = NewRelay(pool, prodA, zap.NewNop(), cfg).Run(ctxA) }()
				// Let A win the election before B starts.
				require.Eventually(t, func() bool { return relayLockHolder(ctx, pool) != 0 }, 10*time.Second, 10*time.Millisecond)
				go func() { defer close(doneB); _ = NewRelay(pool, prodB, zap.NewNop(), cfg).Run(ctxB) }()

				seedOutbox(t, ctx, pool, 5)
				require.Eventually(t, func() bool { return countUnpublished(t, ctx, pool) == 0 }, 10*time.Second, 20*time.Millisecond)
				assert.Len(t, prodA.snapshot(), 5)
				assert.Empty(t, prodB.snapshot(), "follower must not publish")

				cancelA()
				<-doneA
				seedOutbox(t, ctx, pool, 4)
				require.Eventually(t, func() bool { return countUnpublished(t, ctx, pool) == 0 }, 10*time.Second, 20*time.Millisecond)
				assert.Len(t, prodA.snapshot(), 5)
				assert.Len(t, prodB.snapshot(), 4)

				cancelB()
				<-doneB
			})

			t.Run("leader session killed fails over", func(t *testing.T) {
				reset(t)
				cfg := RelayConfig{Store: "test", Interval: 20 * time.Millisecond, Backoff: 50 * time.Millisecond}
				prodA, prodB := &recordingProducer{}, &recordingProducer{}
				rctx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				var wg sync.WaitGroup
				for _, p := range []*recordingProducer{prodA, prodB} {
					wg.Add(1)
					go func() { defer wg.Done(); _ = NewRelay(pool, p, zap.NewNop(), cfg).Run(rctx) }()
				}
				var pid int32
				require.Eventually(t, func() bool { pid = relayLockHolder(ctx, pool); return pid != 0 }, 10*time.Second, 10*time.Millisecond)

				_, err := pool.Exec(ctx, `SELECT pg_terminate_backend($1::int)`, pid)
				require.NoError(t, err)
				require.Eventually(t, func() bool {
					h := relayLockHolder(ctx, pool)
					return h != 0 && h != pid
				}, 10*time.Second, 10*time.Millisecond)

				seedOutbox(t, ctx, pool, 3)
				require.Eventually(t, func() bool { return countUnpublished(t, ctx, pool) == 0 }, 10*time.Second, 20*time.Millisecond)
				assert.Len(t, append(prodA.snapshot(), prodB.snapshot()...), 3)

				cancel()
				wg.Wait()
			})

			t.Run("purge deletes only old published rows in windows", func(t *testing.T) {
				reset(t)
				entries := seedOutbox(t, ctx, pool, 6)
				for i, age := range []string{"5 hours", "3 hours", "2 hours", "10 minutes", "0"} {
					_, err := pool.Exec(ctx, `UPDATE outbox SET published_at = now() - $2::interval WHERE id = $1`, entries[i].ID, age)
					require.NoError(t, err)
				}
				store := "purge-" + db.name
				r := NewRelay(pool, &recordingProducer{}, zap.NewNop(), RelayConfig{Store: store, Retention: time.Hour})
				before := testutil.ToFloat64(outboxPurged.WithLabelValues(store))

				n, err := r.purge(ctx)
				require.NoError(t, err)
				assert.EqualValues(t, 3, n)
				rows, err := pool.Query(ctx, `SELECT id::text FROM outbox ORDER BY created_at`)
				require.NoError(t, err)
				left, err := pgx.CollectRows(rows, pgx.RowTo[string])
				require.NoError(t, err)
				assert.Equal(t, []string{entries[3].ID, entries[4].ID, entries[5].ID}, left)
				assert.InDelta(t, 3, testutil.ToFloat64(outboxPurged.WithLabelValues(store))-before, 0)
			})

			t.Run("leader purges in background", func(t *testing.T) {
				reset(t)
				entries := seedOutbox(t, ctx, pool, 1)
				_, err := pool.Exec(ctx, `UPDATE outbox SET published_at = now() - interval '2 hours' WHERE id = $1`, entries[0].ID)
				require.NoError(t, err)
				rctx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				done := make(chan struct{})
				cfg := RelayConfig{Store: "test", Interval: 20 * time.Millisecond, Backoff: 50 * time.Millisecond, Retention: time.Hour, PurgeEvery: 50 * time.Millisecond}
				go func() { defer close(done); _ = NewRelay(pool, &recordingProducer{}, zap.NewNop(), cfg).Run(rctx) }()

				require.Eventually(t, func() bool {
					var n int
					return pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n) == nil && n == 0
				}, 10*time.Second, 20*time.Millisecond)
				cancel()
				<-done
			})

			t.Run("poll reports backlog age", func(t *testing.T) {
				reset(t)
				store := "age-" + db.name
				entries := seedOutbox(t, ctx, pool, 1)
				_, err := pool.Exec(ctx, `UPDATE outbox SET created_at = now() - interval '90 seconds' WHERE id = $1`, entries[0].ID)
				require.NoError(t, err)
				r := NewRelay(pool, &recordingProducer{err: errors.New("kafka down")}, zap.NewNop(), RelayConfig{Store: store})

				_, err = r.poll(ctx, connect(t, ctx, pool))
				require.Error(t, err)
				assert.GreaterOrEqual(t, testutil.ToFloat64(outboxOldestAge.WithLabelValues(store)), 90.0)
				assert.Zero(t, testutil.ToFloat64(outboxPublished.WithLabelValues(store)), "failed publish must not count")
			})

			t.Run("mark failure re-sends batch", func(t *testing.T) {
				if db.citus {
					t.Skip("triggers on distributed tables need citus.enable_unsafe_triggers")
				}
				reset(t)
				seedOutbox(t, ctx, pool, 2)
				_, err := pool.Exec(ctx, `
					CREATE FUNCTION fail_mark() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'mark blocked'; END $$;
					CREATE TRIGGER fail_mark BEFORE UPDATE ON outbox FOR EACH ROW EXECUTE FUNCTION fail_mark();`)
				require.NoError(t, err)
				prod := &recordingProducer{}
				r := NewRelay(pool, prod, zap.NewNop(), RelayConfig{Store: "test"})
				conn := connect(t, ctx, pool)

				_, err = r.poll(ctx, conn)
				require.Error(t, err)
				assert.Equal(t, 2, countUnpublished(t, ctx, pool))

				_, err = pool.Exec(ctx, `DROP TRIGGER fail_mark ON outbox; DROP FUNCTION fail_mark()`)
				require.NoError(t, err)
				n, err := r.poll(ctx, conn)
				require.NoError(t, err)
				assert.Equal(t, 2, n)
				assert.Len(t, prod.snapshot(), 4, "unmarked batch is published again")
				assert.Zero(t, countUnpublished(t, ctx, pool))
			})
		})
	}
}

// relayLockHolder returns the pid holding relayLockKey, or 0.
func relayLockHolder(ctx context.Context, pool *pgxpool.Pool) int32 {
	var pid int32
	err := pool.QueryRow(ctx, `
		SELECT pid FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND objsubid = 1
		  AND classid = ($1::bigint >> 32)::oid AND objid = ($1::bigint & 4294967295)::oid`,
		relayLockKey).Scan(&pid)
	if err != nil {
		return 0
	}
	return pid
}
