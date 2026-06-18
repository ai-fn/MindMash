package collection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const renderCacheTTL = time.Hour

type renderCacheEntry struct {
	Front string `json:"front"`
	Back  string `json:"back"`
}

// renderCacheKey returns the Redis key for a rendered card.
// USN is part of the key so stale renders are implicitly retired when the card
// is updated — the old key is simply never requested again.
func renderCacheKey(cardID string, usn int64) string {
	return fmt.Sprintf("render:%s:%d", cardID, usn)
}

// getRenderCache fetches a cached render result for the given card+USN.
// Returns ("", "", false) on any miss or error (best-effort).
func getRenderCache(ctx context.Context, rdb *redis.Client, cardID string, usn int64) (front, back string, ok bool) {
	if rdb == nil {
		return "", "", false
	}
	val, err := rdb.Get(ctx, renderCacheKey(cardID, usn)).Result()
	if err != nil {
		return "", "", false
	}
	var entry renderCacheEntry
	if json.Unmarshal([]byte(val), &entry) != nil {
		return "", "", false
	}
	return entry.Front, entry.Back, true
}

// setRenderCache stores rendered front+back HTML in Redis. Errors are silently
// dropped because the cache is best-effort — a miss simply triggers a re-render.
func setRenderCache(ctx context.Context, rdb *redis.Client, cardID string, usn int64, front, back string) {
	if rdb == nil {
		return
	}
	b, err := json.Marshal(renderCacheEntry{Front: front, Back: back})
	if err != nil {
		return
	}
	_ = rdb.Set(ctx, renderCacheKey(cardID, usn), b, renderCacheTTL).Err()
}
