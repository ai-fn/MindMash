package db

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Unit tests for LoadConfig ----

func TestLoadConfig_Defaults(t *testing.T) {
	// Wipe any inherited env values so defaults apply, restoring them on cleanup.
	for _, k := range []string{"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_MAX_CONN_LIFETIME", "DB_MAX_CONN_IDLE_TIME"} {
		k := k
		prev, existed := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("setup: unset %s: %v", k, err)
		}
		t.Cleanup(func() {
			if existed {
				if err := os.Setenv(k, prev); err != nil {
					t.Errorf("cleanup: restore %s: %v", k, err)
				}
			} else {
				if err := os.Unsetenv(k); err != nil {
					t.Errorf("cleanup: unset %s: %v", k, err)
				}
			}
		})
	}
	t.Setenv("DB_DSN", "postgres://user:pass@localhost:5432/testdb")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "postgres://user:pass@localhost:5432/testdb", cfg.DSN)
	assert.Equal(t, int32(10), cfg.MaxConns)
	assert.Equal(t, int32(2), cfg.MinConns)
	assert.Equal(t, 30*time.Minute, cfg.MaxConnLifetime)
	assert.Equal(t, 5*time.Minute, cfg.MaxConnIdleTime)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://user:pass@db:5432/mydb")
	t.Setenv("DB_MAX_CONNS", "20")
	t.Setenv("DB_MIN_CONNS", "5")
	t.Setenv("DB_MAX_CONN_LIFETIME", "1h")
	t.Setenv("DB_MAX_CONN_IDLE_TIME", "10m")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "postgres://user:pass@db:5432/mydb", cfg.DSN)
	assert.Equal(t, int32(20), cfg.MaxConns)
	assert.Equal(t, int32(5), cfg.MinConns)
	assert.Equal(t, 1*time.Hour, cfg.MaxConnLifetime)
	assert.Equal(t, 10*time.Minute, cfg.MaxConnIdleTime)
}

func TestLoadConfig_MissingDSN(t *testing.T) {
	prev, existed := os.LookupEnv("DB_DSN")
	if err := os.Unsetenv("DB_DSN"); err != nil {
		t.Fatalf("setup: unset DB_DSN: %v", err)
	}
	t.Cleanup(func() {
		if existed {
			if err := os.Setenv("DB_DSN", prev); err != nil {
				t.Errorf("cleanup: restore DB_DSN: %v", err)
			}
		} else {
			if err := os.Unsetenv("DB_DSN"); err != nil {
				t.Errorf("cleanup: unset DB_DSN: %v", err)
			}
		}
	})

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_DSN")
}

func TestLoadConfig_InvalidMaxConns(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("DB_MAX_CONNS", "notanumber")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_MAX_CONNS")
}

func TestLoadConfig_InvalidDuration(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("DB_MAX_CONN_LIFETIME", "badvalue")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_MAX_CONN_LIFETIME")
}

// ---- Unit tests for toPgx5URL ----

func TestToPgx5URL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "postgres scheme",
			input: "postgres://user:pass@localhost:5432/db",
			want:  "pgx5://user:pass@localhost:5432/db",
		},
		{
			name:  "postgresql scheme",
			input: "postgresql://user:pass@localhost:5432/db",
			want:  "pgx5://user:pass@localhost:5432/db",
		},
		{
			name:  "already pgx5",
			input: "pgx5://user:pass@localhost:5432/db",
			want:  "pgx5://user:pass@localhost:5432/db",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toPgx5URL(tc.input))
		})
	}
}
