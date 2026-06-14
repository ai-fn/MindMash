package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const sessionKeyPrefix = "sess:"

// RedisSessionStore implements SessionStore using Redis.
// Keys are "sess:{jti}", values are JSON-encoded RefreshSession with a TTL.
type RedisSessionStore struct {
	client *redis.Client
}

// NewRedisSessionStore returns a session store backed by the given Redis client.
func NewRedisSessionStore(client *redis.Client) *RedisSessionStore {
	return &RedisSessionStore{client: client}
}

func sessionKey(jti string) string {
	return sessionKeyPrefix + jti
}

// Put stores the session under "sess:{jti}" with the given TTL.
func (s *RedisSessionStore) Put(ctx context.Context, jti string, sess RefreshSession, ttl time.Duration) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("identity: session store put marshal: %w", err)
	}
	if err := s.client.Set(ctx, sessionKey(jti), data, ttl).Err(); err != nil {
		return fmt.Errorf("identity: session store put: %w", err)
	}
	return nil
}

// Get retrieves a session by JTI.  Returns (zero, false, nil) when the key
// does not exist (Redis.Nil), matching the "cache miss" semantic.
func (s *RedisSessionStore) Get(ctx context.Context, jti string) (RefreshSession, bool, error) {
	data, err := s.client.Get(ctx, sessionKey(jti)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return RefreshSession{}, false, nil
		}
		return RefreshSession{}, false, fmt.Errorf("identity: session store get: %w", err)
	}
	var sess RefreshSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return RefreshSession{}, false, fmt.Errorf("identity: session store get unmarshal: %w", err)
	}
	return sess, true, nil
}

// Delete removes the session keys for the given JTIs in a single round-trip.
func (s *RedisSessionStore) Delete(ctx context.Context, jtis ...string) error {
	if len(jtis) == 0 {
		return nil
	}
	keys := make([]string, len(jtis))
	for i, jti := range jtis {
		keys[i] = sessionKey(jti)
	}
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("identity: session store delete: %w", err)
	}
	return nil
}
