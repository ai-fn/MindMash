package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "") // registers the restore
		os.Unsetenv(k)
	}
}

func TestLoadDefaults(t *testing.T) {
	unsetenv(t, "SERVICE_NAME", "LOG_LEVEL", "GRPC_ADDR", "HTTP_ADDR", "METRICS_ADDR", "SHUTDOWN_TIMEOUT_SECONDS")
	t.Setenv("ENV", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ServiceName != "mindmesh" {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, "mindmesh")
	}
	if !cfg.Dev() {
		t.Errorf("Dev() = false for ENV=dev")
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
	if cfg.Env != "prod" || cfg.Dev() {
		t.Errorf("Env = %q, Dev() = %v; want prod, false", cfg.Env, cfg.Dev())
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

// An unset or unknown ENV must not fall back to dev, which enables reflection and boot migrations.
func TestLoadRejectsMissingOrUnknownEnv(t *testing.T) {
	for _, env := range []string{"", "production", "Dev"} {
		t.Run(env, func(t *testing.T) {
			if env == "" {
				unsetenv(t, "ENV")
			} else {
				t.Setenv("ENV", env)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "ENV") {
				t.Fatalf("Load() error = %v, want one naming ENV", err)
			}
		})
	}
}

func TestLoadInvalidTimeout(t *testing.T) {
	t.Setenv("ENV", "dev")
	for _, v := range []string{"notanumber", "-5", ""} {
		t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", v)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() with SHUTDOWN_TIMEOUT_SECONDS=%q: want error", v)
		}
	}
}

func TestHelpers(t *testing.T) {
	const key = "MINDMESH_CONFIG_TEST"

	t.Run("Str", func(t *testing.T) {
		unsetenv(t, key)
		if got := Str(key, "def"); got != "def" {
			t.Fatalf("unset: got %q", got)
		}
		t.Setenv(key, "")
		if got := Str(key, "def"); got != "" {
			t.Fatalf("set empty: got %q, want the literal empty value", got)
		}
	})

	t.Run("typed values parse or name the key", func(t *testing.T) {
		cases := []struct {
			val  string
			call func() (any, error)
			want any
		}{
			{"7", func() (any, error) { return Int(key, 1) }, 7},
			{"7", func() (any, error) { return Int32(key, 1) }, int32(7)},
			{"true", func() (any, error) { return Bool(key, false) }, true},
			{"1500ms", func() (any, error) { return Duration(key, 0) }, 1500 * time.Millisecond},
			{"20", func() (any, error) { return Seconds(key, 0) }, 20 * time.Second},
		}
		for _, tc := range cases {
			t.Setenv(key, tc.val)
			got, err := tc.call()
			if err != nil || got != tc.want {
				t.Fatalf("%q: got %v, %v; want %v", tc.val, got, err, tc.want)
			}
			for _, bad := range []string{"", "x", "-3s"} {
				if bad == "-3s" && tc.want != 20*time.Second {
					continue
				}
				t.Setenv(key, bad)
				if _, err := tc.call(); err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("%q: error = %v, want one naming %s", bad, err, key)
				}
			}
		}
	})

	t.Run("unset typed values use the default", func(t *testing.T) {
		unsetenv(t, key)
		if n, err := Int(key, 3); err != nil || n != 3 {
			t.Fatalf("Int: %v, %v", n, err)
		}
		if d, err := Seconds(key, time.Minute); err != nil || d != time.Minute {
			t.Fatalf("Seconds: %v, %v", d, err)
		}
	})
}
