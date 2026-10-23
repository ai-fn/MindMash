// Command collection runs the MindMesh Collection service: gRPC (internal) plus
// a grpc-gateway REST edge, with health, metrics, JWKS JWT auth, migrations,
// and an outbox relay goroutine.
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
	"github.com/mindmesh/mindmesh/internal/collection"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "collection: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("collection: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("collection: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal kills instead of waiting out the drain

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("collection: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("collection: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("collection: tracer shutdown", zap.Error(err))
		}
	}()

	// Postgres.
	dbCfg, err := db.LoadConfig()
	if err != nil {
		return fmt.Errorf("collection: load db config: %w", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("collection: connect db: %w", err)
	}
	defer pool.Close()

	if migrationsEnabled(base.Env) {
		dir := envOr("COLLECTION_MIGRATIONS_DIR", "migrations/collection")
		if err := db.RunMigrations(ctx, dbCfg.DSN, dir); err != nil {
			return fmt.Errorf("collection: run migrations: %w", err)
		}
		logger.Info("collection: migrations applied", zap.String("dir", dir))
	}

	// Auth verifier: remote JWKS (JWKS_URL set) or local key file fallback.
	var verifier auth.Verifier
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		issuer := envOr("JWKS_ISSUER", "")
		rv, err := auth.NewRemoteVerifier(ctx, jwksURL, issuer)
		if err != nil {
			return fmt.Errorf("collection: init jwks verifier: %w", err)
		}
		verifier = rv
		logger.Info("collection: using remote JWKS verifier", zap.String("url", jwksURL))
	} else {
		authCfg, err := auth.LoadConfig()
		if err != nil {
			return fmt.Errorf("collection: load auth config: %w", err)
		}
		signer, err := auth.NewSigner(authCfg)
		if err != nil {
			return fmt.Errorf("collection: init local key signer: %w", err)
		}
		verifier = signer
		logger.Info("collection: using local key verifier (dev fallback)")
	}

	// Kafka producer for the outbox relay.
	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("collection: load events config: %w", err)
	}
	producer, err := events.NewProducer(eventsCfg)
	if err != nil {
		return fmt.Errorf("collection: init kafka producer: %w", err)
	}
	defer func() { _ = producer.Close() }()

	relay := events.NewRelay(pool, producer, logger, events.RelayConfig{Store: "collection"})

	// Domain wiring.
	repo := collection.NewPostgresRepository(pool)
	svc := collection.NewService(repo, logger)
	handler := collection.NewHandler(svc)
	internalHandler := collection.NewInternalHandler(svc)

	services, err := auth.NewServiceVerifier(auth.CollectionInternalAudience,
		envOr("SERVICE_TRUSTED_KEYS", "search=keys/search-service.pub.pem"))
	if err != nil {
		return fmt.Errorf("collection: load trusted service keys: %w", err)
	}
	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCServiceOrUserAuthInterceptor(verifier, services, collection.InternalServicePolicy()),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         base.Env == "dev",
		Register: func(gs *grpc.Server) {
			collectionv1.RegisterNoteTypeServiceServer(gs, handler)
			collectionv1.RegisterNoteServiceServer(gs, handler)
			collectionv1.RegisterCardServiceServer(gs, handler)
			collectionv1.RegisterDeckServiceServer(gs, handler)
			collectionv1.RegisterFilteredDeckServiceServer(gs, handler)
			collectionv1.RegisterCollectionInternalServiceServer(gs, internalHandler)
		},
	})

	// REST gateway (public services only; CollectionInternalService is gRPC-only).
	gwMux := runtime.NewServeMux(httpx.GatewayRoutes())
	if err := collectionv1.RegisterNoteTypeServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("collection: register note-type gateway: %w", err)
	}
	if err := collectionv1.RegisterNoteServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("collection: register note gateway: %w", err)
	}
	if err := collectionv1.RegisterCardServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("collection: register card gateway: %w", err)
	}
	if err := collectionv1.RegisterDeckServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("collection: register deck gateway: %w", err)
	}
	if err := collectionv1.RegisterFilteredDeckServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("collection: register filtered-deck gateway: %w", err)
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

	logger.Info("collection: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	g.Go(func() error { return relay.Run(gctx) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("collection: server: %w", err)
	}
	logger.Info("collection: stopped")
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
