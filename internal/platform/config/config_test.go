package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Unset each env var for the duration of this test, restoring on cleanup.
	keys := []string{
		"SERVICE_NAME", "ENV", "LOG_LEVEL",
		"GRPC_ADDR", "HTTP_ADDR", "METRICS_ADDR",
		"SHUTDOWN_TIMEOUT_SECONDS",
	}
	for _, k := range keys {
		k := k // capture loop var
		prev, exists := os.LookupEnv(k)
		os.Unsetenv(k)
		if exists {
			t.Cleanup(func() { os.Setenv(k, prev) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ServiceName != "mindmesh" {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, "mindmesh")
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want %q", cfg.Env, "dev")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.GRPCAddr != ":50051" {
		t.Errorf("GRPCAddr = %q, want %q", cfg.GRPCAddr, ":50051")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr = %q, want %q", cfg.MetricsAddr, ":9090")
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 15*time.Second)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("SERVICE_NAME", "testsvc")
	t.Setenv("ENV", "prod")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("GRPC_ADDR", ":9000")
	t.Setenv("HTTP_ADDR", ":7000")
	t.Setenv("METRICS_ADDR", ":9200")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ServiceName != "testsvc" {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, "testsvc")
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want %q", cfg.Env, "prod")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.GRPCAddr != ":9000" {
		t.Errorf("GRPCAddr = %q, want %q", cfg.GRPCAddr, ":9000")
	}
	if cfg.HTTPAddr != ":7000" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":7000")
	}
	if cfg.MetricsAddr != ":9200" {
		t.Errorf("MetricsAddr = %q, want %q", cfg.MetricsAddr, ":9200")
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 30*time.Second)
	}
}

func TestLoadInvalidTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "notanumber")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid SHUTDOWN_TIMEOUT_SECONDS, got nil")
	}
}

func TestLoadNegativeTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "-5")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for negative SHUTDOWN_TIMEOUT_SECONDS, got nil")
	}
}
