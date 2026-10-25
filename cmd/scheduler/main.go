// Command scheduler runs the MindMesh Scheduler service: gRPC (StudyService)
// plus a grpc-gateway REST edge, with health, metrics, JWKS JWT auth,
// migrations, an outbox relay goroutine, and a
// client connection to Collection's internal RenderCards RPC.
package main

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	schedulerv1 "github.com/mindmesh/mindmesh/api/scheduler/v1"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/scheduler"
)

func main() { app.Main("scheduler", setup) }

func setup(ctx context.Context, a *app.App) error {
	// Shared with Collection: same DB, `cards`/`decks`/`deck_configs` plus Scheduler-owned `review_logs`.
	pool, dbCfg, err := a.Postgres(ctx)
	if err != nil {
		return err
	}
	if err := a.Migrate(ctx, dbCfg.DSN, "platform", "scheduler"); err != nil {
		return err
	}
	verifier, err := a.Verifier(ctx)
	if err != nil {
		return err
	}
	if err := a.Relay(pool, "scheduler"); err != nil {
		return err
	}

	// Collection internal client (RenderCards), forwarding the caller's JWT.
	collConn, err := grpcx.Dial(ctx, config.Str("COLLECTION_GRPC_ADDR", "localhost:9090"),
		grpc.WithChainUnaryInterceptor(grpcx.ForwardAuthInterceptor()),
		// Every internal method is a read, so all are safe to retry.
		grpcx.RetryOn(collectionv1.CollectionInternalService_ServiceDesc.ServiceName),
	)
	if err != nil {
		return fmt.Errorf("dial collection: %w", err)
	}
	a.OnClose(func() { _ = collConn.Close() })
	collClient := collectionv1.NewCollectionInternalServiceClient(collConn)

	repo := scheduler.NewPostgresRepository(pool)
	svc := scheduler.NewService(repo, collClient, a.Log)
	handler := scheduler.NewHandler(svc)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         a.Base.Dev(),
		Register: func(gs *grpc.Server) {
			schedulerv1.RegisterStudyServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := schedulerv1.RegisterStudyServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register study gateway: %w", err)
	}

	httpSrv := httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness: func(ctx context.Context) error {
			if err := auth.Ready(verifier); err != nil {
				return err
			}
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("postgres: %w", err)
			}
			return nil
		},
	})

	a.Log.Info("scheduler: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	return nil
}
