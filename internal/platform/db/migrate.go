package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5 driver
	"github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate applies each store's migrations from root/<store>, in order. Stores share one database,
// so each keeps its own schema_migrations_<store> history: overlapping version numbers would otherwise collide.
func Migrate(_ context.Context, dsn, root string, stores ...string) error {
	for _, store := range stores {
		if err := migrateStore(dsn, filepath.Join(root, store), store); err != nil {
			return fmt.Errorf("db: migrate %s: %w", store, err)
		}
	}
	return nil
}

func migrateStore(dsn, dir, store string) error {
	fSrc, err := (&file.File{}).Open("file://" + dir)
	if err != nil {
		return fmt.Errorf("open source %q: %w", dir, err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	m, err := migrate.NewWithSourceInstance("file", fSrc, toPgx5URL(dsn)+sep+"x-migrations-table=schema_migrations_"+store)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
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
