package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter counts attempts per key in a fixed window.
type RateLimiter interface {
	// Allow records an attempt and reports whether it is within limit.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
	// Reset clears key's counter.
	Reset(ctx context.Context, key string) error
}

// RedisRateLimiter is a fixed-window counter; the window starts at the first attempt.
type RedisRateLimiter struct {
	client redis.Cmdable
}

func NewRedisRateLimiter(client redis.Cmdable) *RedisRateLimiter {
	return &RedisRateLimiter{client: client}
}

func (l *RedisRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	pipe := l.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("identity: rate limit %q: %w", key, err)
	}
	return incr.Val() <= int64(limit), nil
}

func (l *RedisRateLimiter) Reset(ctx context.Context, key string) error {
	if err := l.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("identity: rate limit reset %q: %w", key, err)
	}
	return nil
}
