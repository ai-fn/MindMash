package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Daily-limit kinds used in the Redis counter key.
const (
	limitKindNew = "new"
	limitKindRev = "rev"
)

// limitKey returns the Redis key for a profile+deck's daily new/review
// counter. dayKey is domain.DayKey(dayStart), e.g. "20260619".
func limitKey(profileID, deckID, dayKey, kind string) string {
	return fmt.Sprintf("limit:%s:%s:%s:%s", profileID, deckID, dayKey, kind)
}

// reserveScript atomically grants up to ARGV[1] slots against the cap ARGV[2]
// for the counter at KEYS[1], incrementing it by the granted amount (never
// pushing it past the cap) and, on first creation, setting it to expire at
// ARGV[3] (the next rollover) so the key is self-cleaning. Returns the number
// of slots actually granted, which may be less than requested (down to 0) if
// the cap is already reached or nearly so. Running the check and the
// increment in one script keeps concurrent reservations from over-granting
// the daily limit, which a separate GET-then-INCR would allow.
var reserveScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local remaining = tonumber(ARGV[2]) - cur
if remaining < 0 then remaining = 0 end
local grant = tonumber(ARGV[1])
if grant > remaining then grant = remaining end
if grant > 0 then
	local newval = redis.call('INCRBY', KEYS[1], grant)
	if newval == grant then
		redis.call('EXPIREAT', KEYS[1], ARGV[3])
	end
end
return grant
`)

// reserveDailySlots atomically reserves up to want slots against cap for a
// profile+deck's daily new/review counter, returning the number actually
// granted (<= want). rdb == nil disables limiting (always grants want in
// full).
func reserveDailySlots(ctx context.Context, rdb *redis.Client, profileID, deckID, dayKey, kind string, want int64, dailyCap int32, expireAt time.Time) (int64, error) {
	if rdb == nil {
		return want, nil
	}
	if want <= 0 {
		return 0, nil
	}
	key := limitKey(profileID, deckID, dayKey, kind)
	n, err := reserveScript.Run(ctx, rdb, []string{key}, want, dailyCap, expireAt.Unix()).Int64()
	if err != nil {
		return 0, fmt.Errorf("scheduler: reserve daily slots: %w", err)
	}
	return n, nil
}

// releaseDailySlots gives back n previously reserved slots that ended up
// unused (e.g. fewer cards were actually available than were reserved).
func releaseDailySlots(ctx context.Context, rdb *redis.Client, profileID, deckID, dayKey, kind string, n int64) error {
	if rdb == nil || n <= 0 {
		return nil
	}
	key := limitKey(profileID, deckID, dayKey, kind)
	if err := rdb.DecrBy(ctx, key, n).Err(); err != nil {
		return fmt.Errorf("scheduler: release daily slots: %w", err)
	}
	return nil
}
