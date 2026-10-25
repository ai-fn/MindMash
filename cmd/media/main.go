// Command media runs the MindMesh Media service: a gRPC (plus grpc-gateway REST)
// upload/dedupe/delivery API over S3/MinIO and Postgres metadata, an outbox
// relay, and an image-processing worker consuming media.uploaded. One binary; MODE
// (api | worker | all, default all) picks the roles so the worker can run as its own Deployment.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	mediav1 "github.com/mindmesh/mindmesh/api/media/v1"
	"github.com/mindmesh/mindmesh/internal/media"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/blob"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() { app.Main("media", setup) }

func setup(ctx context.Context, a *app.App) error {
	m, err := parseMode(config.Str("MODE", ""))
	if err != nil {
		return err
	}
	pool, dbCfg, err := a.Postgres(ctx)
	if err != nil {
		return err
	}
	if m.api {
		if err := a.Migrate(ctx, dbCfg.DSN, "platform", "media"); err != nil {
			return err
		}
	}

	// Object store (S3/MinIO). The API ensures the bucket exists; the worker only needs it reachable.
	blobCfg, err := blob.LoadConfig(a.Base.Dev())
	if err != nil {
		return err
	}
	blobClient, err := blob.NewClient(ctx, blobCfg)
	if err != nil {
		return fmt.Errorf("init blob client: %w", err)
	}
	if m.api {
		if err := blobClient.EnsureBucket(ctx, blobCfg.DefaultBucket); err != nil {
			return fmt.Errorf("ensure bucket %s: %w", blobCfg.DefaultBucket, err)
		}
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
	// Probes and /metrics only, unless the API mounts its routes below.
	httpOpts := httpx.Options{MetricsHandler: observability.InitMetrics(), Readiness: readiness}

	if m.api {
		if verifier, err = a.Verifier(ctx); err != nil {
			return err
		}
		if err := a.Relay(pool, "media"); err != nil {
			return err
		}

		svc := media.NewService(repo, blobClient, blobCfg.DefaultBucket, a.Log)
		handler := media.NewHandler(svc)
		internalHandler := media.NewInternalHandler(svc)

		grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
			UnaryInterceptors: []grpc.UnaryServerInterceptor{
				auth.GRPCAuthInterceptor(verifier),
			},
			StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
			Reflection:         a.Base.Dev(),
			Register: func(gs *grpc.Server) {
				mediav1.RegisterMediaServiceServer(gs, handler)
				mediav1.RegisterMediaInternalServiceServer(gs, internalHandler)
			},
		})

		// REST gateway (external MediaService only; the internal service is gRPC-only).
		gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
		if err := mediav1.RegisterMediaServiceHandlerServer(ctx, gwMux, handler); err != nil {
			return fmt.Errorf("register media gateway: %w", err)
		}
		// GET /v1/media/{id} is a custom 302 redirect, not a generated RPC.
		if err := media.RegisterDownloadRoute(gwMux, svc); err != nil {
			return fmt.Errorf("register download route: %w", err)
		}
		httpOpts.Handler = auth.HTTPAuthMiddleware(verifier)(gwMux)

		a.Go(grpcSrv.Start)
		a.Go(svc.RunPendingGC)
	}

	if m.worker {
		consumer, err := a.Consumer("media-processor")
		if err != nil {
			return err
		}
		worker := media.NewWorker(repo, blobClient, blobCfg.DefaultBucket, a.Log)
		a.Go(func(ctx context.Context) error {
			return consumer.Subscribe(ctx, []string{media.Topic}, worker.Handle)
		})
	}

	a.Go(httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpOpts).Start)

	fields := []zap.Field{
		zap.Bool("api", m.api), zap.Bool("worker", m.worker), zap.String("http_addr", a.Base.HTTPAddr),
		zap.String("topic", media.Topic), zap.String("bucket", blobCfg.DefaultBucket),
	}
	if m.api {
		fields = append(fields, zap.String("grpc_addr", a.Base.GRPCAddr))
	}
	a.Log.Info("media: starting", fields...)
	return nil
}
