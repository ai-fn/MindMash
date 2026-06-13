package cache

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Unit tests for LoadConfig ----

func TestLoadConfig_Defaults(t *testing.T) {
	for _, k := range []string{
		"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB",
		"REDIS_DIAL_TIMEOUT", "REDIS_READ_TIMEOUT", "REDIS_WRITE_TIMEOUT",
	} {
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

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "localhost:6379", cfg.Addr)
	assert.Equal(t, "", cfg.Password)
	assert.Equal(t, 0, cfg.DB)
	assert.Equal(t, 5*time.Second, cfg.DialTimeout)
	assert.Equal(t, 3*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 3*time.Second, cfg.WriteTimeout)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis-host:6380")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DB", "2")
	t.Setenv("REDIS_DIAL_TIMEOUT", "10s")
	t.Setenv("REDIS_READ_TIMEOUT", "6s")
	t.Setenv("REDIS_WRITE_TIMEOUT", "6s")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "redis-host:6380", cfg.Addr)
	assert.Equal(t, "secret", cfg.Password)
	assert.Equal(t, 2, cfg.DB)
	assert.Equal(t, 10*time.Second, cfg.DialTimeout)
	assert.Equal(t, 6*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 6*time.Second, cfg.WriteTimeout)
}

func TestLoadConfig_InvalidDB(t *testing.T) {
	t.Setenv("REDIS_DB", "notanumber")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REDIS_DB")
}

func TestLoadConfig_InvalidDuration(t *testing.T) {
	t.Setenv("REDIS_DIAL_TIMEOUT", "badvalue")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REDIS_DIAL_TIMEOUT")
}

func TestLoadConfig_EmptyAddr(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REDIS_ADDR")
}
