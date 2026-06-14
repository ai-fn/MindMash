package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	srv := New(DefaultConfig(":0"), Options{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("body: got %q, want it to contain %q", body, "ok")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}
}

func TestReadyz_Ready(t *testing.T) {
	srv := New(DefaultConfig(":0"), Options{Readiness: nil})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ready") {
		t.Errorf("body: got %q, want it to contain %q", body, "ready")
	}
}

func TestJWKSRoute(t *testing.T) {
	called := false
	jwksHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	srv := New(DefaultConfig(":0"), Options{JWKSHandler: jwksHandler})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if !called {
		t.Error("expected JWKS handler to be called")
	}
}

func TestReadyz_NotReady(t *testing.T) {
	srv := New(DefaultConfig(":0"), Options{
		Readiness: func(ctx context.Context) error {
			return errors.New("db not ready")
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not ready") {
		t.Errorf("body: got %q, want it to contain %q", body, "not ready")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}
}

func TestMetricsRoute(t *testing.T) {
	called := false
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	srv := New(DefaultConfig(":0"), Options{MetricsHandler: metricsHandler})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if !called {
		t.Error("expected metrics handler to be called")
	}
}

func TestCatchAllHandler(t *testing.T) {
	called := false
	appHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	srv := New(DefaultConfig(":0"), Options{Handler: appHandler})
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if !called {
		t.Error("expected catch-all handler to be called for unknown path")
	}
}
