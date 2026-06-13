// Package cache provides a Redis client and common helper functions.
package cache

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds Redis connection configuration.
type Config struct {
	Addr         string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// LoadConfig reads cache configuration from environment variables.
func LoadConfig() (Config, error) {
	addr := envStr("REDIS_ADDR", "localhost:6379")
	if addr == "" {
		return Config{}, fmt.Errorf("cache: REDIS_ADDR must not be empty")
	}
	password := envStr("REDIS_PASSWORD", "")

	db, err := envInt("REDIS_DB", 0)
	if err != nil {
		return Config{}, fmt.Errorf("cache: invalid REDIS_DB: %w", err)
	}

	dialTimeout, err := envDuration("REDIS_DIAL_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("cache: invalid REDIS_DIAL_TIMEOUT: %w", err)
	}

	readTimeout, err := envDuration("REDIS_READ_TIMEOUT", 3*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("cache: invalid REDIS_READ_TIMEOUT: %w", err)
	}

	writeTimeout, err := envDuration("REDIS_WRITE_TIMEOUT", 3*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("cache: invalid REDIS_WRITE_TIMEOUT: %w", err)
	}

	return Config{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}, nil
}

// New creates and returns a connected *redis.Client using the provided config.
// It pings the server to verify the connection before returning.
func New(ctx context.Context, cfg Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}

	return client, nil
}

// SetNX sets key to value with ttl only if the key does not already exist.
// Returns true if the key was set, false if it already existed.
func SetNX(ctx context.Context, client *redis.Client, key string, value any, ttl time.Duration) (bool, error) {
	set, err := client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: setnx %q: %w", key, err)
	}
	return set, nil
}

// GetDel atomically retrieves and deletes the value stored at key.
// Returns ("", nil) when the key does not exist (redis.Nil is treated as
// empty, not an error).
func GetDel(ctx context.Context, client *redis.Client, key string) (string, error) {
	val, err := client.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: getdel %q: %w", key, err)
	}
	return val, nil
}

// Incr atomically increments the integer value stored at key by one.
func Incr(ctx context.Context, client *redis.Client, key string) (int64, error) {
	n, err := client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: incr %q: %w", key, err)
	}
	return n, nil
}

// envStr returns the value of the environment variable key, or def if not set.
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// envInt reads an int value from the environment variable key.
// Returns def when unset, and an error when unparseable.
func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as int: %w", v, err)
	}
	return n, nil
}

// envDuration reads a duration string from the environment variable key.
// Returns def when unset, and an error when unparseable.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as duration: %w", v, err)
	}
	return d, nil
}
