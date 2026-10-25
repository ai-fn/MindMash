// Command media runs the MindMesh Media service: a gRPC (plus grpc-gateway REST)
// upload/dedupe/delivery API over S3/MinIO and Postgres metadata, an outbox
// relay, and an image-processing worker consuming media.uploaded. One binary; MODE
// (api | worker | all, default all) picks the roles so the worker can run as its own Deployment.
package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	mediav1 "github.com/mindmesh/mindmesh/api/media/v1"
	"github.com/mindmesh/mindmesh/internal/media"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/blob"
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
		fmt.Fprintf(os.Stderr, "media: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("media: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("media: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal kills instead of waiting out the drain

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("media: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("media: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("media: tracer shutdown", zap.Error(err))
		}
	}()

	// Postgres.
	dbCfg, err := db.LoadConfig()
	if err != nil {
		return fmt.Errorf("media: load db config: %w", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("media: connect db: %w", err)
	}
	defer pool.Close()

	m, err := parseMode(os.Getenv("MODE"))
	if err != nil {
		return err
	}

	if m.api && migrationsEnabled(base.Env) {
		dir := envOr("MIGRATIONS_DIR", "migrations")
		if err := db.Migrate(ctx, dbCfg.DSN, dir, "platform", "media"); err != nil {
			return fmt.Errorf("media: run migrations: %w", err)
		}
		logger.Info("media: migrations applied", zap.String("dir", dir))
	}

	// Object store (S3/MinIO). The API ensures the bucket exists; the worker only needs it reachable.
	blobCfg, err := blob.LoadConfig()
	if err != nil {
		return fmt.Errorf("media: load blob config: %w", err)
	}
	blobClient, err := blob.NewClient(ctx, blobCfg)
	if err != nil {
		return fmt.Errorf("media: init blob client: %w", err)
	}
	if m.api {
		if err := blobClient.EnsureBucket(ctx, blobCfg.DefaultBucket); err != nil {
			return fmt.Errorf("media: ensure bucket %s: %w", blobCfg.DefaultBucket, err)
		}
	}

	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("media: load events config: %w", err)
	}
	repo := media.NewRepo(pool)

	// HEAD only, cached: a per-probe S3 call per pod adds up, and creating is startup's job.
	bucketReady := httpx.CacheReady(time.Minute, func(ctx context.Context) error {
		return blobClient.CheckBucket(ctx, blobCfg.DefaultBucket)
	})
	var verifier auth.Verifier
	readiness := func(ctx context.Context) error {
		if verifier != nil {
			if err := auth.Ready(verifier); err != nil {
				return err
			}
		}
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		return bucketReady(ctx)
	}

	// Started together once every role is wired, so a setup error leaves nothing running.
	var runs []func(context.Context) error
	// Probes and /metrics only, unless the API mounts its routes below.
	httpOpts := httpx.Options{MetricsHandler: observability.InitMetrics(), Readiness: readiness}

	if m.api {
		// Auth verifier: remote JWKS (JWKS_URL set) or local key file fallback.
		if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
			rv, err := auth.NewRemoteVerifier(ctx, jwksURL, envOr("JWKS_ISSUER", ""))
			if err != nil {
				return fmt.Errorf("media: init jwks verifier: %w", err)
			}
			verifier = rv
			logger.Info("media: using remote JWKS verifier", zap.String("url", jwksURL))
		} else {
			authCfg, err := auth.LoadConfig()
			if err != nil {
				return fmt.Errorf("media: load auth config: %w", err)
			}
			signer, err := auth.NewSigner(authCfg)
			if err != nil {
				return fmt.Errorf("media: init local key signer: %w", err)
			}
			verifier = signer
			logger.Info("media: using local key verifier (dev fallback)")
		}

		producer, err := events.NewProducer(eventsCfg)
		if err != nil {
			return fmt.Errorf("media: init kafka producer: %w", err)
		}
		defer func() { _ = producer.Close() }()
		relay := events.NewRelay(pool, producer, logger, events.RelayConfig{Store: "media"})

		svc := media.NewService(repo, blobClient, blobCfg.DefaultBucket, logger)
		handler := media.NewHandler(svc)
		internalHandler := media.NewInternalHandler(svc)

		grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
			UnaryInterceptors: []grpc.UnaryServerInterceptor{
				auth.GRPCAuthInterceptor(verifier),
			},
			StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
			Reflection:         base.Env == "dev",
			Register: func(gs *grpc.Server) {
				mediav1.RegisterMediaServiceServer(gs, handler)
				mediav1.RegisterMediaInternalServiceServer(gs, internalHandler)
			},
		})

		// REST gateway (external MediaService only; the internal service is gRPC-only).
		gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
		if err := mediav1.RegisterMediaServiceHandlerServer(ctx, gwMux, handler); err != nil {
			return fmt.Errorf("media: register media gateway: %w", err)
		}
		// GET /v1/media/{id} is a custom 302 redirect, not a generated RPC.
		if err := media.RegisterDownloadRoute(gwMux, svc); err != nil {
			return fmt.Errorf("media: register download route: %w", err)
		}
		httpOpts.Handler = auth.HTTPAuthMiddleware(verifier)(gwMux)

		runs = append(runs, grpcSrv.Start, relay.Run, svc.RunPendingGC)
	}

	if m.worker {
		workerCfg := eventsCfg
		workerCfg.GroupID = cmp.Or(workerCfg.GroupID, "media-processor")
		consumer, err := events.NewConsumer(workerCfg, logger)
		if err != nil {
			return fmt.Errorf("media: init kafka consumer: %w", err)
		}
		defer func() { _ = consumer.Close() }()
		worker := media.NewWorker(repo, blobClient, blobCfg.DefaultBucket, logger)
		runs = append(runs, func(ctx context.Context) error {
			return consumer.Subscribe(ctx, []string{media.Topic}, worker.Handle)
		})
	}

	runs = append(runs, httpx.New(httpx.DefaultConfig(base.HTTPAddr), httpOpts).Start)

	fields := []zap.Field{zap.Bool("api", m.api), zap.Bool("worker", m.worker), zap.String("http_addr", base.HTTPAddr)}
	if m.api {
		fields = append(fields, zap.String("grpc_addr", base.GRPCAddr))
	}
	logger.Info("media: starting", append(fields,
		zap.String("topic", media.Topic),
		zap.String("bucket", blobCfg.DefaultBucket),
	)...)
	g, gctx := errgroup.WithContext(ctx)
	for _, run := range runs {
		g.Go(func() error { return run(gctx) })
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("media: server: %w", err)
	}
	logger.Info("media: stopped")
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
