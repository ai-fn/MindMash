// Command scheduler runs the MindMesh Scheduler service: gRPC (StudyService)
// plus a grpc-gateway REST edge, with health, metrics, JWKS JWT auth,
// migrations, an outbox relay goroutine, and a
// client connection to Collection's internal RenderCards RPC.
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

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	schedulerv1 "github.com/mindmesh/mindmesh/api/scheduler/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/scheduler"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("scheduler: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("scheduler: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal kills instead of waiting out the drain

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("scheduler: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("scheduler: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("scheduler: tracer shutdown", zap.Error(err))
		}
	}()

	// Postgres (shared with Collection: same DB, `cards`/`decks`/`deck_configs`
	// plus Scheduler-owned `review_logs`).
	dbCfg, err := db.LoadConfig()
	if err != nil {
		return fmt.Errorf("scheduler: load db config: %w", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("scheduler: connect db: %w", err)
	}
	defer pool.Close()

	if migrationsEnabled(base.Env) {
		dir := envOr("MIGRATIONS_DIR", "migrations")
		if err := db.Migrate(ctx, dbCfg.DSN, dir, "platform", "scheduler"); err != nil {
			return fmt.Errorf("scheduler: run migrations: %w", err)
		}
		logger.Info("scheduler: migrations applied", zap.String("dir", dir))
	}

	// Auth verifier: remote JWKS (JWKS_URL set) or local key file fallback.
	var verifier auth.Verifier
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		issuer := envOr("JWKS_ISSUER", "")
		rv, err := auth.NewRemoteVerifier(ctx, jwksURL, issuer)
		if err != nil {
			return fmt.Errorf("scheduler: init jwks verifier: %w", err)
		}
		verifier = rv
		logger.Info("scheduler: using remote JWKS verifier", zap.String("url", jwksURL))
	} else {
		authCfg, err := auth.LoadConfig()
		if err != nil {
			return fmt.Errorf("scheduler: load auth config: %w", err)
		}
		signer, err := auth.NewSigner(authCfg)
		if err != nil {
			return fmt.Errorf("scheduler: init local key signer: %w", err)
		}
		verifier = signer
		logger.Info("scheduler: using local key verifier (dev fallback)")
	}

	// Kafka producer for the outbox relay.
	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("scheduler: load events config: %w", err)
	}
	producer, err := events.NewProducer(eventsCfg)
	if err != nil {
		return fmt.Errorf("scheduler: init kafka producer: %w", err)
	}
	defer func() { _ = producer.Close() }()

	relay := events.NewRelay(pool, producer, logger, events.RelayConfig{Store: "scheduler"})

	// Collection internal client (RenderCards), forwarding the caller's JWT.
	collAddr := envOr("COLLECTION_GRPC_ADDR", "localhost:9090")
	collConn, err := grpcx.Dial(ctx, collAddr,
		grpc.WithChainUnaryInterceptor(grpcx.ForwardAuthInterceptor()),
		// Every internal method is a read, so all are safe to retry.
		grpcx.RetryOn(collectionv1.CollectionInternalService_ServiceDesc.ServiceName),
	)
	if err != nil {
		return fmt.Errorf("scheduler: dial collection: %w", err)
	}
	defer func() { _ = collConn.Close() }()
	collClient := collectionv1.NewCollectionInternalServiceClient(collConn)

	// Domain wiring.
	repo := scheduler.NewPostgresRepository(pool)
	svc := scheduler.NewService(repo, collClient, logger)
	handler := scheduler.NewHandler(svc)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         base.Env == "dev",
		Register: func(gs *grpc.Server) {
			schedulerv1.RegisterStudyServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := schedulerv1.RegisterStudyServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("scheduler: register study gateway: %w", err)
	}

	readiness := func(ctx context.Context) error {
		if err := auth.Ready(verifier); err != nil {
			return err
		}
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		return nil
	}

	httpSrv := httpx.New(httpx.DefaultConfig(base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness:      readiness,
	})

	logger.Info("scheduler: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	g.Go(func() error { return relay.Run(gctx) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("scheduler: server: %w", err)
	}
	logger.Info("scheduler: stopped")
	return nil
}

// migrationsEnabled reports whether migrations should run at boot. Defaults to
// on in dev; RUN_MIGRATIONS overrides explicitly.
func migrationsEnabled(env string) bool {
	if v, ok := os.LookupEnv("RUN_MIGRATIONS"); ok {
		return v == "true" || v == "1"
	}
	return env == "dev"
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
