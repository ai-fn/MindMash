//go:build integration

package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startPostgres starts a throwaway Postgres container for the duration of t
// and returns the pool (for cleanup) and the connection DSN.
func startPostgres(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()

	pgc, err := postgres.Run(ctx,
		"postgres:18-alpine",
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

	cfg := Config{
		DSN:             dsn,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
	pool, err := New(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool, dsn
}

func TestNewPool_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool, _ := startPostgres(t, ctx)

	require.NoError(t, pool.Ping(ctx))
}

func TestWithTx_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool, _ := startPostgres(t, ctx)

	// Create a test table.
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tx_test (id SERIAL PRIMARY KEY, val TEXT)`)
	require.NoError(t, err)

	t.Run("commit on success", func(t *testing.T) {
		err := WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO tx_test (val) VALUES ($1)`, "hello")
			return err
		})
		require.NoError(t, err)

		var count int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM tx_test WHERE val = $1`, "hello").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("rollback on fn error", func(t *testing.T) {
		fnErr := fmt.Errorf("intentional error")
		err := WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO tx_test (val) VALUES ($1)`, "rollback-me")
			if err != nil {
				return err
			}
			return fnErr
		})
		require.ErrorIs(t, err, fnErr)

		var count int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM tx_test WHERE val = $1`, "rollback-me").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("rollback and release on panic", func(t *testing.T) {
		require.Panics(t, func() {
			_ = WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO tx_test (val) VALUES ($1)`, "panic-me"); err != nil {
					return err
				}
				panic("boom")
			})
		})
		assert.Zero(t, pool.Stat().AcquiredConns(), "the panicking tx kept its connection")

		var count int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM tx_test WHERE val = $1`, "panic-me").Scan(&count))
		assert.Equal(t, 0, count)
	})

	t.Run("honours tx options", func(t *testing.T) {
		err := WithTx(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO tx_test (val) VALUES ($1)`, "read-only")
			return err
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})
}

func TestMigrate_StoresKeepSeparateHistories(t *testing.T) {
	ctx := context.Background()
	pool, dsn := startPostgres(t, ctx)

	root := t.TempDir()
	for _, store := range []string{"a", "b"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, store), 0o755))
		for v := 1; v <= 2; v++ {
			up := fmt.Sprintf("CREATE TABLE %s_%d (id int);", store, v)
			require.NoError(t, os.WriteFile(filepath.Join(root, store, fmt.Sprintf("%06d_t.up.sql", v)), []byte(up), 0o644))
			down := fmt.Sprintf("DROP TABLE %s_%d;", store, v)
			require.NoError(t, os.WriteFile(filepath.Join(root, store, fmt.Sprintf("%06d_t.down.sql", v)), []byte(down), 0o644))
		}
	}

	require.NoError(t, Migrate(ctx, dsn, root, "a"))
	require.NoError(t, Migrate(ctx, dsn, root, "a", "b"))

	for _, table := range []string{"a_1", "a_2", "b_1", "b_2"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		assert.True(t, exists, "table %s missing: stores share a version history", table)
	}
}

// Guards ADR 0001: every distributed table shares user_usn's colocation group, whichever store migrates first.
func TestMigrate_RepoStoresColocateOnCitus(t *testing.T) {
	ctx := context.Background()
	pgc, err := postgres.Run(ctx, "citusdata/citus:13.0",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS citus`)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, dsn, "../../../migrations", "platform", "media", "identity", "collection", "scheduler", "sync"))

	var tables, groups int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT colocationid) FROM pg_dist_partition`).Scan(&tables, &groups))
	assert.Greater(t, tables, 1)
	assert.Equal(t, 1, groups, "distributed tables span several colocation groups")
}
