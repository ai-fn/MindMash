// Package db provides a pgxpool-based PostgreSQL client and transaction helper.
package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	maxConns, err := envInt32("DB_MAX_CONNS", 10)
	if err != nil {
		return Config{}, fmt.Errorf("db: invalid DB_MAX_CONNS: %w", err)
	}

	minConns, err := envInt32("DB_MIN_CONNS", 2)
	if err != nil {
		return Config{}, fmt.Errorf("db: invalid DB_MIN_CONNS: %w", err)
	}

	maxConnLifetime, err := envDuration("DB_MAX_CONN_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("db: invalid DB_MAX_CONN_LIFETIME: %w", err)
	}

	maxConnIdleTime, err := envDuration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("db: invalid DB_MAX_CONN_IDLE_TIME: %w", err)
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

// WithTx runs fn inside a database transaction.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	if fnErr := fn(tx); fnErr != nil {
		_ = tx.Rollback(ctx)
		return fnErr
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}

	return nil
}

// envInt32 reads an int32 value from the environment variable key.
// Returns def when the variable is unset, and an error when it cannot be parsed.
func envInt32(key string, def int32) (int32, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as int32: %w", v, err)
	}
	return int32(n), nil
}

// envDuration reads a duration string from the environment variable key.
// Returns def when the variable is unset, and an error when it cannot be parsed.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as duration: %w", v, err)
	}
	return d, nil
}
