//go:build integration

package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
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
		err := WithTx(ctx, pool, func(tx pgx.Tx) error {
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
		err := WithTx(ctx, pool, func(tx pgx.Tx) error {
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
}
