package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/testutil"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(log.Replace(zap.New(core)))
	return logs
}

func TestServer_PanicBecomes500AndIsCounted(t *testing.T) {
	logs := observeLogs(t)
	srv := New(DefaultConfig(":0"), Options{Handler: Route("/v1/chain-boom", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))})
	c := httpRequests.WithLabelValues("GET", "/v1/chain-boom", "500")
	before := testutil.ToFloat64(c)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain-boom", nil))

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal") {
		t.Fatalf("got %d %q, want 500 with an internal error body", rec.Code, rec.Body.String())
	}
	if d := testutil.ToFloat64(c) - before; d != 1 {
		t.Fatalf("panics must count as 500s, delta = %v", d)
	}
	if logs.FilterMessage("httpx: panic recovered").Len() != 1 {
		t.Fatalf("want one panic log, got %v", logs.All())
	}
}

func TestServer_PanicAfterHeadersAbortsTheResponse(t *testing.T) {
	observeLogs(t)
	srv := New(DefaultConfig(":0"), Options{Handler: Route("/v1/chain-midstream", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // chunked: only an abort, not a short body, makes the read fail
		_, _ = w.Write([]byte("partial"))
		http.NewResponseController(w).Flush() //nolint:errcheck
		panic("boom mid-stream")
	}))})
	c := httpRequests.WithLabelValues("GET", "/v1/chain-midstream", "500")
	before := testutil.ToFloat64(c)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("a response that panicked after its headers must be cut off, not end cleanly")
	}
	if d := testutil.ToFloat64(c) - before; d != 1 {
		t.Fatalf("a mid-stream panic must count as a 500, delta = %v", d)
	}
}

func TestServer_LogsEachRequestWithRoute(t *testing.T) {
	logs := observeLogs(t)
	mux := http.NewServeMux()
	mux.Handle("/v1/chain-logged", Route("/v1/chain-logged", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	srv := New(DefaultConfig(":0"), Options{Handler: mux})
	srv.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/chain-logged", nil))

	entries := logs.FilterMessage("httpx: request").All()
	if len(entries) != 1 {
		t.Fatalf("want one request log, got %v", logs.All())
	}
	f := entries[0].ContextMap()
	if f["route"] != "/v1/chain-logged" || f["status"] != int64(http.StatusTeapot) || f["method"] != "GET" {
		t.Fatalf("request log fields = %v", f)
	}
	if _, ok := f["path"]; ok {
		t.Fatalf("routed requests must not log the raw path (ids): %v", f)
	}

	srv.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/"+strings.Repeat("x", 1000), nil))
	last := logs.FilterMessage("httpx: request").All()
	if p, _ := last[len(last)-1].ContextMap()["path"].(string); p == "" || len(p) > maxLoggedPath {
		t.Fatalf("unrouted path must be logged truncated to %d, got %d bytes", maxLoggedPath, len(p))
	}
}

func TestServer_LimitsRequestBodies(t *testing.T) {
	var readErr error
	srv := New(DefaultConfig(":0"), Options{MaxBodyBytes: 8, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	})})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("0123456789")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversized body: got %d, want 413", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/x", io.NopCloser(strings.NewReader("0123456789")))
	req.ContentLength = -1 // chunked: only the reader can enforce the cap
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	var tooLarge *http.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("undeclared oversized body: read err = %v, want MaxBytesError", readErr)
	}
}

func TestServer_ChunkedOverflowThroughGatewayIs413(t *testing.T) {
	gw := runtime.NewServeMux(GatewayRoutes())
	if err := gw.HandlePath(http.MethodPost, "/v1/chain-upload", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if _, err := io.ReadAll(r.Body); err != nil {
			// What generated gateway handlers do with any decode error.
			runtime.HTTPError(r.Context(), gw, &runtime.JSONPb{}, w, r, status.Errorf(codes.InvalidArgument, "%v", err))
		}
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(DefaultConfig(":0"), Options{MaxBodyBytes: 8, Handler: gw})
	req := httptest.NewRequest(http.MethodPost, "/v1/chain-upload", io.NopCloser(strings.NewReader("0123456789")))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked overflow: got %d, want 413 like the declared-length case", rec.Code)
	}
}

func TestServer_DropsSlowHeaders(t *testing.T) {
	cfg := DefaultConfig(freeAddr(t))
	cfg.ReadHeaderTimeout = 100 * time.Millisecond
	cfg.DrainDelay = 0
	srv := New(cfg, Options{Handler: http.NotFoundHandler()})
	ctx, cancel := contextWithCancel(t)
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		var err error
		if conn, err = net.Dial("tcp", cfg.Addr); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never listened: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")) // never finishes the headers
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	_, _ = io.ReadAll(conn) // returns once the server closes the connection
	if time.Since(start) > time.Second {
		t.Fatalf("slow-header connection kept open for %v", time.Since(start))
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(context.Background())
}

func TestServer_AbortHandlerIsNotLoggedAsPanic(t *testing.T) {
	logs := observeLogs(t)
	srv := New(DefaultConfig(":0"), Options{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if resp, err := ts.Client().Get(ts.URL + "/v1/x"); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("an aborted handler must not produce a response, got %d", resp.StatusCode)
	}
	if n := logs.FilterMessage("httpx: panic recovered").Len(); n != 0 {
		t.Fatalf("ErrAbortHandler logged as a panic %d times", n)
	}
	entries := logs.FilterMessage("httpx: request").All()
	if len(entries) != 1 || entries[0].ContextMap()["status"] != int64(statusNoResponse) {
		t.Fatalf("an abort before any response must not be recorded as 200: %v", entries)
	}
}

func TestServer_MidStreamPanicMarksTheSpan(t *testing.T) {
	observeLogs(t)
	sr := testhelpers.RecordSpans(t)
	srv := New(DefaultConfig(":0"), Options{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("boom mid-stream")
	})})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if resp, err := ts.Client().Get(ts.URL + "/v1/x"); err == nil {
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	ts.Close()
	spans := sr.Ended()
	if len(spans) != 1 || spans[0].Status().Code != otelcodes.Error {
		t.Fatalf("want the aborted request's span marked as an error, got %d spans", len(spans))
	}
}
