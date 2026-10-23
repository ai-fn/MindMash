package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// OAuthState is the server-side record of one authorization request, keyed by its state.
type OAuthState struct {
	Provider string `json:"provider"`
	OAuthFlow
}

type OAuthStateStore interface {
	Put(ctx context.Context, state string, st OAuthState, ttl time.Duration) error
	// Take returns the record and deletes it, so a state works once.
	Take(ctx context.Context, state string) (OAuthState, bool, error)
}

type RedisOAuthStateStore struct {
	client redis.Cmdable
}

func NewRedisOAuthStateStore(client redis.Cmdable) *RedisOAuthStateStore {
	return &RedisOAuthStateStore{client: client}
}

func oauthStateKey(state string) string { return "oauth:state:" + HashToken(state) }

func (s *RedisOAuthStateStore) Put(ctx context.Context, state string, st OAuthState, ttl time.Duration) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("identity: oauth state marshal: %w", err)
	}
	if err := s.client.Set(ctx, oauthStateKey(state), data, ttl).Err(); err != nil {
		return fmt.Errorf("identity: oauth state put: %w", err)
	}
	return nil
}

func (s *RedisOAuthStateStore) Take(ctx context.Context, state string) (OAuthState, bool, error) {
	data, err := s.client.GetDel(ctx, oauthStateKey(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return OAuthState{}, false, nil
	}
	if err != nil {
		return OAuthState{}, false, fmt.Errorf("identity: oauth state take: %w", err)
	}
	var st OAuthState
	if err := json.Unmarshal(data, &st); err != nil {
		return OAuthState{}, false, fmt.Errorf("identity: oauth state decode: %w", err)
	}
	return st, true, nil
}
