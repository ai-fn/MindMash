// Package db provides a pgxpool-based PostgreSQL client and transaction helper.
package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mindmesh/mindmesh/internal/platform/config"
)

// Config holds PostgreSQL connection pool configuration.
type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// LoadConfig reads DB configuration from environment variables.
// DB_DSN is required; missing it returns an error.
func LoadConfig() (Config, error) {
	dsn, ok := os.LookupEnv("DB_DSN")
	if !ok || dsn == "" {
		return Config{}, fmt.Errorf("db: DB_DSN is required but not set")
	}

	maxConns, err := config.Int32("DB_MAX_CONNS", 10)
	if err != nil {
		return Config{}, fmt.Errorf("db: %w", err)
	}

	minConns, err := config.Int32("DB_MIN_CONNS", 2)
	if err != nil {
		return Config{}, fmt.Errorf("db: %w", err)
	}

	if maxConns < 1 || minConns < 0 || minConns > maxConns {
		return Config{}, fmt.Errorf("db: need 0 <= DB_MIN_CONNS (%d) <= DB_MAX_CONNS (%d) and DB_MAX_CONNS >= 1", minConns, maxConns)
	}

	maxConnLifetime, err := config.Duration("DB_MAX_CONN_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("db: %w", err)
	}

	maxConnIdleTime, err := config.Duration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("db: %w", err)
	}

	return Config{
		DSN:             dsn,
		MaxConns:        maxConns,
		MinConns:        minConns,
		MaxConnLifetime: maxConnLifetime,
		MaxConnIdleTime: maxConnIdleTime,
	}, nil
}

// New creates and returns a connected pgxpool.Pool using the provided config.
// It pings the database to verify the connection before returning.
func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return pool, nil
}

// WithTx runs fn in a transaction, committing only if it returns nil.
func WithTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	// Also runs on a panic, so the connection goes back to the pool; a no-op after Commit.
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}
	return nil
}
