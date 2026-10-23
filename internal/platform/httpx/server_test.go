package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
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

func TestServer_LinksClientTraceInsteadOfJoiningIt(t *testing.T) {
	sr := testhelpers.RecordSpans(t)
	const clientTrace = "0af7651916cd43dd8448eb211c80319c"
	var got trace.SpanContext
	srv := New(DefaultConfig(":0"), Options{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	})})
	req := httptest.NewRequest(http.MethodGet, "/v1/decks", nil)
	req.Header.Set("traceparent", "00-"+clientTrace+"-b7ad6b7169203331-01")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req)

	if !got.IsValid() || got.TraceID().String() == clientTrace {
		t.Fatalf("handler trace = %q, want a new root so clients can't force sampling", got.TraceID())
	}
	spans := sr.Ended()
	if len(spans) != 1 || len(spans[0].Links()) != 1 || spans[0].Links()[0].SpanContext.TraceID().String() != clientTrace {
		t.Fatalf("want one server span linked to the client trace, got %d spans", len(spans))
	}
}

func TestServer_AppHandlerSupportsWriteDeadlines(t *testing.T) {
	errs := make(chan error, 1)
	srv := New(DefaultConfig(":0"), Options{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		err := rc.SetWriteDeadline(time.Now().Add(time.Second))
		if err == nil {
			err = rc.Flush()
		}
		errs <- err
	})})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	resp, err := ts.Client().Get(ts.URL + "/v1/sync/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if err := <-errs; err != nil {
		t.Fatalf("SSE handlers need per-write deadlines through the wrapped writer: %v", err)
	}
}
