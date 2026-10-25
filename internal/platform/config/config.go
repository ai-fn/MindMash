package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"
)

// BaseConfig holds config fields shared by every service.
type BaseConfig struct {
	ServiceName     string        // SERVICE_NAME, default "mindmesh"
	Env             string        // ENV: "dev"|"staging"|"prod", required
	LogLevel        string        // LOG_LEVEL: "debug"|"info"|"warn"|"error", default "info"
	GRPCAddr        string        // GRPC_ADDR, default ":50051"
	HTTPAddr        string        // HTTP_ADDR, default ":8080"
	MetricsAddr     string        // METRICS_ADDR, default ":9090"
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT_SECONDS, default 15s
}

var envs = []string{"dev", "staging", "prod"}

// Load reads BaseConfig from environment, returning an error for invalid values.
func Load() (*BaseConfig, error) {
	// No default: dev turns on reflection and boot migrations, so a missing ENV must not mean dev.
	env, _ := os.LookupEnv("ENV")
	if !slices.Contains(envs, env) {
		return nil, fmt.Errorf("config: ENV must be one of %v, got %q", envs, env)
	}
	shutdownTimeout, err := Seconds("SHUTDOWN_TIMEOUT_SECONDS", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &BaseConfig{
		ServiceName:     Str("SERVICE_NAME", "mindmesh"),
		Env:             env,
		LogLevel:        Str("LOG_LEVEL", "info"),
		GRPCAddr:        Str("GRPC_ADDR", ":50051"),
		HTTPAddr:        Str("HTTP_ADDR", ":8080"),
		MetricsAddr:     Str("METRICS_ADDR", ":9090"),
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

// Dev reports whether the service runs in local development.
func (c *BaseConfig) Dev() bool { return c.Env == "dev" }

// Str returns the value of key, or def when it is unset. A value set to "" is returned as is,
// so a blanked required setting fails its validation instead of falling back to a localhost default.
func Str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// Int returns key parsed as an int, or def when it is unset.
func Int(key string, def int) (int, error) {
	return parse(key, def, strconv.Atoi)
}

// Int32 returns key parsed as an int32, or def when it is unset.
func Int32(key string, def int32) (int32, error) {
	return parse(key, def, func(v string) (int32, error) {
		n, err := strconv.ParseInt(v, 10, 32)
		return int32(n), err
	})
}

// Bool returns key parsed by strconv.ParseBool, or def when it is unset.
func Bool(key string, def bool) (bool, error) {
	return parse(key, def, strconv.ParseBool)
}

// Duration returns key parsed by time.ParseDuration ("1500ms"), or def when it is unset.
func Duration(key string, def time.Duration) (time.Duration, error) {
	return parse(key, def, time.ParseDuration)
}

// Seconds returns key read as a non-negative integer number of seconds, or def when it is unset.
func Seconds(key string, def time.Duration) (time.Duration, error) {
	return parse(key, def, func(v string) (time.Duration, error) {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return 0, err
		}
		if secs < 0 {
			return 0, fmt.Errorf("must be non-negative")
		}
		return time.Duration(secs) * time.Second, nil
	})
}

func parse[T any](key string, def T, fn func(string) (T, error)) (T, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	got, err := fn(v)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s=%q: %w", key, v, err)
	}
	return got, nil
}
