//go:build integration

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// startRedis starts a throwaway Redis container for the duration of t and
// returns a connected client.
func startRedis(t *testing.T, ctx context.Context) *redis.Client {
	t.Helper()

	rc, err := tcredis.Run(ctx, "redis:8-alpine")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := rc.Terminate(ctx); err != nil {
			t.Errorf("cleanup: terminate redis container: %v", err)
		}
	})

	addr, err := rc.Endpoint(ctx, "")
	require.NoError(t, err)

	cfg := Config{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	client, err := New(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("cleanup: close redis client: %v", err)
		}
	})

	return client
}

func TestNew_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client := startRedis(t, ctx)

	require.NoError(t, client.Ping(ctx).Err())
}

func TestSetNXAndGetDel_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client := startRedis(t, ctx)

	key := "test:setnx:key"

	// SetNX should succeed on first call.
	ok, err := SetNX(ctx, client, key, "hello", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok, "expected key to be set on first call")

	// SetNX should fail (key exists).
	ok, err = SetNX(ctx, client, key, "world", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok, "expected key not to be set when it already exists")

	// GetDel should return the original value and delete the key.
	val, err := GetDel(ctx, client, key)
	require.NoError(t, err)
	assert.Equal(t, "hello", val)

	// Second GetDel on same key should return empty string (key gone).
	val, err = GetDel(ctx, client, key)
	require.NoError(t, err)
	assert.Equal(t, "", val)
}

func TestIncr_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client := startRedis(t, ctx)

	key := "test:incr:counter"
	// Make sure the key is clean.
	require.NoError(t, client.Del(ctx, key).Err())

	for i := int64(1); i <= 3; i++ {
		n, err := Incr(ctx, client, key)
		require.NoError(t, err)
		assert.Equal(t, i, n)
	}
}
