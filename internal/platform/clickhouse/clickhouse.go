// Package clickhouse provides a thin clickhouse-go/v2 native client plus a
// startup migration helper. ClickHouse is MindMesh's analytics mirror: the
// authoritative review logs live in Postgres, while ClickHouse holds the
// columnar copy used for sub-second aggregations.
package clickhouse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/mindmesh/mindmesh/internal/platform/config"
)

// Config holds ClickHouse connection settings.
type Config struct {
	Addr     string
	Database string
	Username string
	Password string
}

// LoadConfig reads ClickHouse configuration from the environment, falling back
// to the local docker-compose defaults.
func LoadConfig() Config {
	return Config{
		Addr:     config.Str("CLICKHOUSE_ADDR", "localhost:9000"),
		Database: config.Str("CLICKHOUSE_DB", "mindmesh"),
		Username: config.Str("CLICKHOUSE_USER", "mindmesh"),
		Password: config.Str("CLICKHOUSE_PASSWORD", "mindmesh"),
	}
}

// New opens a native ClickHouse connection and pings it to verify reachability.
func New(ctx context.Context, cfg Config) (driver.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}
	return conn, nil
}

// RunMigrations applies every `*.up.sql` file in migrationsDir (lexically
// ordered) against the connection. Statements are separated by `;`. All DDL is
// expected to be idempotent (`IF NOT EXISTS`), so re-running on an already
// migrated database is a no-op — ClickHouse has no migration-version table and
// the analytics schema is small and additive.
func RunMigrations(ctx context.Context, conn driver.Conn, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("clickhouse: read migrations dir %q: %w", migrationsDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	// os.ReadDir returns entries sorted by name, so `names` is already ordered.
	for _, name := range names {
		raw, err := os.ReadFile(migrationsDir + "/" + name)
		if err != nil {
			return fmt.Errorf("clickhouse: read migration %q: %w", name, err)
		}
		for _, stmt := range splitStatements(string(raw)) {
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("clickhouse: apply %q: %w", name, err)
			}
		}
	}
	return nil
}

// splitStatements strips `--` line comments first (which may themselves contain
// semicolons), then splits the remaining SQL on `;` boundaries, dropping blank
// statements. The analytics DDL contains no semicolons inside string literals,
// so a simple split is safe once comments are gone.
func splitStatements(script string) []string {
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		code = append(code, line)
	}

	var out []string
	for _, part := range strings.Split(strings.Join(code, "\n"), ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
