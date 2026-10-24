// Command sync runs the MindMesh Sync service: gRPC + grpc-gateway REST for
// the USN delta pull (GetChanges), plus an SSE route for the multi-tab
// realtime notifier. Sync reads Collection's and Scheduler's colocated
// Postgres tables directly (see internal/sync's package doc) and owns one
// small migrated table of its own, sync_applied_ops, for /sync/apply op_id
// idempotency.
package main

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	syncv1 "github.com/mindmesh/mindmesh/api/sync/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/cache"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/sync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sync: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("sync: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("sync: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal kills instead of waiting out the drain

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("sync: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("sync: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("sync: tracer shutdown", zap.Error(err))
		}
	}()

	// Postgres — same cluster as Collection/Scheduler, plus Sync's own
	// sync_applied_ops table for /sync/apply idempotency.
	dbCfg, err := db.LoadConfig()
	if err != nil {
		return fmt.Errorf("sync: load db config: %w", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("sync: connect db: %w", err)
	}
	defer pool.Close()

	if migrationsEnabled(base.Env) {
		dir := envOr("MIGRATIONS_DIR", "migrations")
		if err := db.Migrate(ctx, dbCfg.DSN, dir, "platform", "sync"); err != nil {
			return fmt.Errorf("sync: run migrations: %w", err)
		}
		logger.Info("sync: migrations applied", zap.String("dir", dir))
	}

	// Redis — backs the SSE notifier's pub/sub so a nudge published by
	// whichever replica consumes the Kafka event reaches subscribers on
	// every replica, not just the one that happened to receive it.
	cacheCfg, err := cache.LoadConfig()
	if err != nil {
		return fmt.Errorf("sync: load cache config: %w", err)
	}
	rdb, err := cache.New(ctx, cacheCfg)
	if err != nil {
		return fmt.Errorf("sync: connect redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// Auth verifier: remote JWKS (JWKS_URL set) or local key file fallback.
	var verifier auth.Verifier
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		issuer := envOr("JWKS_ISSUER", "")
		rv, err := auth.NewRemoteVerifier(ctx, jwksURL, issuer)
		if err != nil {
			return fmt.Errorf("sync: init jwks verifier: %w", err)
		}
		verifier = rv
		logger.Info("sync: using remote JWKS verifier", zap.String("url", jwksURL))
	} else {
		authCfg, err := auth.LoadConfig()
		if err != nil {
			return fmt.Errorf("sync: load auth config: %w", err)
		}
		signer, err := auth.NewSigner(authCfg)
		if err != nil {
			return fmt.Errorf("sync: init local key signer: %w", err)
		}
		verifier = signer
		logger.Info("sync: using local key verifier (dev fallback)")
	}

	// Kafka consumer driving the realtime notifier.
	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("sync: load events config: %w", err)
	}
	eventsCfg.GroupID = cmp.Or(eventsCfg.GroupID, "sync-notifier")
	consumer, err := events.NewConsumer(eventsCfg, logger)
	if err != nil {
		return fmt.Errorf("sync: init kafka consumer: %w", err)
	}
	defer func() { _ = consumer.Close() }()

	// Collection client for /sync/apply's conflict-resolved mutation calls
	// (forwards the caller's JWT, same dial pattern as Scheduler's Collection
	// client).
	collAddr := envOr("COLLECTION_GRPC_ADDR", "localhost:9090")
	collConn, err := grpcx.Dial(ctx, collAddr,
		grpc.WithChainUnaryInterceptor(grpcx.ForwardAuthInterceptor()),
		// Reads only: mutations carry an expected USN, so a replayed success would come back as a conflict.
		grpcx.RetryOn(
			collectionv1.NoteTypeService_GetNoteType_FullMethodName,
			collectionv1.NoteService_GetNote_FullMethodName,
			collectionv1.DeckService_GetDeck_FullMethodName,
			collectionv1.DeckService_GetDeckConfig_FullMethodName,
		),
	)
	if err != nil {
		return fmt.Errorf("sync: dial collection: %w", err)
	}
	defer func() { _ = collConn.Close() }()
	collClient := sync.NewGRPCCollectionClient(collConn)

	// Domain wiring.
	repo := sync.NewPostgresRepository(pool)
	svc := sync.NewService(repo, collClient, rdb, logger)
	handler := sync.NewHandler(svc)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         base.Env == "dev",
		Register: func(gs *grpc.Server) {
			syncv1.RegisterSyncServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := syncv1.RegisterSyncServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("sync: register gateway: %w", err)
	}

	// Combine the gateway mux with the plain SSE route under one handler so
	// auth.HTTPAuthMiddleware wraps both identically.
	rootMux := http.NewServeMux()
	rootMux.Handle("/v1/sync/stream", httpx.Route("/v1/sync/stream", handler.StreamHandler(logger)))
	rootMux.Handle("/", gwMux)

	// Only the owned store: without Redis, pull/apply still work and new streams get 503 from the hub.
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
		Handler:        auth.HTTPAuthMiddleware(verifier)(rootMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness:      readiness,
	})

	logger.Info("sync: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	// Ends with gctx and closes every SSE stream, so open streams don't hold up the HTTP drain.
	g.Go(func() error { return svc.RunNotifyHub(gctx) })
	g.Go(func() error { return sync.RunNotifier(gctx, consumer, svc, logger) })
	g.Go(func() error { return sync.RunAppliedOpsPurge(gctx, pool, logger) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("sync: server: %w", err)
	}
	logger.Info("sync: stopped")
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
