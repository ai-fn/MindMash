package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5 driver
	"github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations applies all pending migrations from migrationsDir to the
// database pointed at by dsn. ErrNoChange (no new migrations) is treated as
// success.
func RunMigrations(_ context.Context, dsn, migrationsDir string) error {
	srcURL := "file://" + migrationsDir
	dbURL := toPgx5URL(dsn)

	// Open the file source so we can get a concrete *file.File driver.
	fSrc, err := (&file.File{}).Open(srcURL)
	if err != nil {
		return fmt.Errorf("db: open migration source %q: %w", srcURL, err)
	}

	m, err := migrate.NewWithSourceInstance("file", fSrc, dbURL)
	if err != nil {
		return fmt.Errorf("db: create migrate instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: run migrations: %w", err)
	}

	return nil
}

// toPgx5URL replaces the postgres:// (or postgresql://) scheme with pgx5://
// so that golang-migrate uses the registered pgx/v5 database driver.
func toPgx5URL(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgresql://")
	case strings.HasPrefix(dsn, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgres://")
	default:
		// DSN already uses a different scheme or is a key=value string;
		// return as-is and let migrate surface a meaningful error.
		return dsn
	}
}
