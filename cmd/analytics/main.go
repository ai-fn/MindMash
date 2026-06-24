// Command analytics runs the MindMesh Analytics service: a gRPC (plus
// grpc-gateway REST) statistics API over a ClickHouse review-events mirror,
// together with a stats-aggregator consumer goroutine that ingests Scheduler's
// review.recorded stream into ClickHouse. One binary, two roles for the MVP;
// they can be split later.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	analyticsv1 "github.com/mindmesh/mindmesh/api/analytics/v1"
	"github.com/mindmesh/mindmesh/internal/analytics"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/clickhouse"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "analytics: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("analytics: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("analytics: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("analytics: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("analytics: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("analytics: tracer shutdown", zap.Error(err))
		}
	}()

	// ClickHouse client + schema bootstrap. The DDL is idempotent (IF NOT
	// EXISTS), so applying it on every start is safe.
	chConn, err := clickhouse.New(ctx, clickhouse.LoadConfig())
	if err != nil {
		return fmt.Errorf("analytics: init clickhouse: %w", err)
	}
	defer func() { _ = chConn.Close() }()
	migrationsDir := envOr("CLICKHOUSE_MIGRATIONS_DIR", "migrations/clickhouse")
	if err := clickhouse.RunMigrations(ctx, chConn, migrationsDir); err != nil {
		return fmt.Errorf("analytics: run clickhouse migrations: %w", err)
	}

	// The read API verifies inbound JWTs. The local signer is the dev fallback
	// verifier; prod verifies via remote JWKS. Analytics makes no outbound RPCs,
	// so it never mints tokens.
	authCfg, err := auth.LoadConfig()
	if err != nil {
		return fmt.Errorf("analytics: load auth config: %w", err)
	}
	signer, err := auth.NewSigner(authCfg)
	if err != nil {
		return fmt.Errorf("analytics: init signer: %w", err)
	}
	var verifier auth.Verifier = signer
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		rv, err := auth.NewRemoteVerifier(ctx, jwksURL, envOr("JWKS_ISSUER", ""))
		if err != nil {
			return fmt.Errorf("analytics: init jwks verifier: %w", err)
		}
		verifier = rv
		logger.Info("analytics: using remote JWKS verifier", zap.String("url", jwksURL))
	} else {
		logger.Info("analytics: using local key verifier (dev fallback)")
	}

	// Kafka consumer for the aggregator. A distinct group keeps Analytics's
	// offsets independent of other consumers on the review_log topic.
	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("analytics: load events config: %w", err)
	}
	if os.Getenv("KAFKA_GROUP_ID") == "" {
		eventsCfg.GroupID = "stats-aggregator"
	}
	consumer, err := events.NewConsumer(eventsCfg)
	if err != nil {
		return fmt.Errorf("analytics: init kafka consumer: %w", err)
	}
	defer func() { _ = consumer.Close() }()

	aggregator := analytics.NewAggregator(chConn, logger)
	handler := analytics.NewHandler(analytics.NewRepo(chConn))

	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		Register: func(gs *grpc.Server) {
			analyticsv1.RegisterAnalyticsServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux()
	if err := analyticsv1.RegisterAnalyticsServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("analytics: register analytics gateway: %w", err)
	}

	readiness := func(ctx context.Context) error {
		if err := chConn.Ping(ctx); err != nil {
			return fmt.Errorf("clickhouse: %w", err)
		}
		return nil
	}

	httpSrv := httpx.New(httpx.DefaultConfig(base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness:      readiness,
	})

	logger.Info("analytics: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
		zap.String("topic", analytics.ReviewTopic),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	g.Go(func() error { return consumer.Subscribe(gctx, []string{analytics.ReviewTopic}, aggregator.Handle) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("analytics: server: %w", err)
	}
	logger.Info("analytics: stopped")
	return nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
