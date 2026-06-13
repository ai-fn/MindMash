package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// BaseConfig holds config fields shared by every service.
type BaseConfig struct {
	ServiceName     string        // SERVICE_NAME, default "mindmesh"
	Env             string        // ENV: "dev"|"staging"|"prod", default "dev"
	LogLevel        string        // LOG_LEVEL: "debug"|"info"|"warn"|"error", default "info"
	GRPCAddr        string        // GRPC_ADDR, default ":50051"
	HTTPAddr        string        // HTTP_ADDR, default ":8080"
	MetricsAddr     string        // METRICS_ADDR, default ":9090"
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT_SECONDS, default 15s
}

// Load reads BaseConfig from environment, returning an error for invalid values.
func Load() (*BaseConfig, error) {
	shutdownTimeout, err := envDuration("SHUTDOWN_TIMEOUT_SECONDS", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("config: invalid SHUTDOWN_TIMEOUT_SECONDS: %w", err)
	}

	return &BaseConfig{
		ServiceName:     envStr("SERVICE_NAME", "mindmesh"),
		Env:             envStr("ENV", "dev"),
		LogLevel:        envStr("LOG_LEVEL", "info"),
		GRPCAddr:        envStr("GRPC_ADDR", ":50051"),
		HTTPAddr:        envStr("HTTP_ADDR", ":8080"),
		MetricsAddr:     envStr("METRICS_ADDR", ":9090"),
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

// MustLoad panics on error — for use in main() where misconfiguration is fatal.
func MustLoad() *BaseConfig {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// envStr returns the value of the environment variable key, or def if not set.
// An explicitly set empty string is returned as-is (treated as "set").
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// envDuration reads an integer number of seconds from key and converts to
// time.Duration. Returns def when the variable is not set, and an error when
// the value cannot be parsed as a non-negative integer.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	secs, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as integer seconds: %w", v, err)
	}
	if secs < 0 {
		return 0, fmt.Errorf("SHUTDOWN_TIMEOUT_SECONDS must be non-negative, got %d", secs)
	}
	return time.Duration(secs) * time.Second, nil
}
