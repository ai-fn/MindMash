// Package httpx provides a reusable HTTP server with health, metrics,
// graceful shutdown, and standard middleware.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

// Config holds HTTP server settings.
type Config struct {
	Addr              string        // listen address, e.g. ":8080"
	ReadTimeout       time.Duration // default 15s
	ReadHeaderTimeout time.Duration // default 5s
	WriteTimeout      time.Duration // default 15s
	IdleTimeout       time.Duration // default 60s
	ShutdownTimeout   time.Duration // default 15s
	DrainDelay        time.Duration // default 5s
}

// DefaultConfig returns sane defaults for the given addr.
func DefaultConfig(addr string) Config {
	return Config{
		Addr:              addr,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   15 * time.Second,
		DrainDelay:        5 * time.Second,
	}
}

var errDraining = errors.New("httpx: draining for shutdown")

// HealthChecker reports readiness. Returns nil when ready.
type HealthChecker func(ctx context.Context) error

// Options configure a Server.
type Options struct {
	Handler        http.Handler  // the application handler (e.g. grpc-gateway mux)
	MetricsHandler http.Handler  // prometheus handler (from observability.InitMetrics()); if nil, /metrics is omitted
	Readiness      HealthChecker // optional readiness probe for /readyz; if nil, /readyz always 200
	JWKSHandler    http.Handler  // optional handler for /.well-known/jwks.json; if nil, route omitted
	MaxBodyBytes   int64         // app request body cap; default 4 MiB
}

// Server wraps an *http.Server with lifecycle management.
type Server struct {
	httpServer *http.Server
	cfg        Config
	handler    http.Handler
	draining   *atomic.Bool
}

// Handler returns the root mux so tests can call ServeHTTP directly without
// starting a listener.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// New builds a Server. It mounts /healthz, /readyz, /metrics, and
// /.well-known/jwks.json on a root mux, delegating everything else to opts.Handler.
func New(cfg Config, opts Options) *Server {
	mux := http.NewServeMux()

	// Liveness: process is up.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Readiness: delegate to the supplied checker.
	draining := new(atomic.Bool)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		check := opts.Readiness
		if draining.Load() {
			check = func(context.Context) error { return errDraining }
		}
		if check != nil {
			if err := check(r.Context()); err != nil {
				log.L().Warn("httpx: not ready", zap.Error(err))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"not ready"}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	if opts.MetricsHandler != nil {
		mux.Handle("/metrics", opts.MetricsHandler)
	}

	if opts.JWKSHandler != nil {
		mux.Handle("/.well-known/jwks.json", opts.JWKSHandler)
	}

	// Only application routes are traced; probes and scrapes would flood the trace store.
	// HTTP callers are external clients: link their trace rather than let them force sampling.
	if opts.Handler != nil {
		maxBody := opts.MaxBodyBytes
		if maxBody <= 0 {
			maxBody = defaultMaxBodyBytes
		}
		mux.Handle("/", otelhttp.NewHandler(observe(limitBody(maxBody, opts.Handler)), "http.server",
			otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method })))
	}

	hs := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	return &Server{
		httpServer: hs,
		cfg:        cfg,
		handler:    mux,
		draining:   draining,
	}
}

// Start begins serving and blocks until the context is cancelled, then
// gracefully shuts down within cfg.ShutdownTimeout. Returns nil on clean
// shutdown, or an error if ListenAndServe or Shutdown fails.
func (s *Server) Start(ctx context.Context) error {
	log.L().Info("httpx: starting server", zap.String("addr", s.cfg.Addr))

	serveErr := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("httpx: ListenAndServe: %w", err)
	case <-ctx.Done():
		// Fail readiness but keep serving, so load balancers drop this instance before connections close.
		s.draining.Store(true)
		log.L().Info("httpx: draining", zap.Duration("delay", s.cfg.DrainDelay))
		time.Sleep(s.cfg.DrainDelay)
		log.L().Info("httpx: shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("httpx: Shutdown: %w", err)
		}
		return nil
	}
}

// CacheReady reuses a successful check for ttl, sparing per-probe calls to slow dependencies; failures are never cached.
func CacheReady(ttl time.Duration, check HealthChecker) HealthChecker {
	var okUntil atomic.Int64
	return func(ctx context.Context) error {
		if time.Now().UnixNano() < okUntil.Load() {
			return nil
		}
		if err := check(ctx); err != nil {
			return err
		}
		okUntil.Store(time.Now().Add(ttl).UnixNano())
		return nil
	}
}
