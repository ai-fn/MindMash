// Command sync runs the MindMesh Sync service: gRPC + grpc-gateway REST for
// the USN delta pull (GetChanges), plus an SSE route for the multi-tab
// realtime notifier. Sync reads Collection's and Scheduler's colocated
// Postgres tables directly (see internal/syncer's package doc) and owns one
// small migrated table of its own, sync_applied_ops, for /sync/apply op_id
// idempotency.
package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	syncv1 "github.com/mindmesh/mindmesh/api/sync/v1"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/syncer"
)

func main() { app.Main("sync", setup) }

func setup(ctx context.Context, a *app.App) error {
	pool, dbCfg, err := a.Postgres(ctx)
	if err != nil {
		return err
	}
	if err := a.Migrate(ctx, dbCfg.DSN, "platform", "sync"); err != nil {
		return err
	}
	// Backs the SSE notifier's pub/sub, so a nudge published by whichever replica
	// consumes the Kafka event reaches subscribers on every replica.
	rdb, err := a.Redis(ctx)
	if err != nil {
		return err
	}
	verifier, err := a.Verifier(ctx)
	if err != nil {
		return err
	}
	consumer, err := a.Consumer("sync-notifier")
	if err != nil {
		return err
	}

	// Collection client for /sync/apply's conflict-resolved mutation calls, forwarding the caller's JWT.
	collConn, err := grpcx.Dial(ctx, config.Str("COLLECTION_GRPC_ADDR", "localhost:9090"),
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
		return fmt.Errorf("dial collection: %w", err)
	}
	a.OnClose(func() { _ = collConn.Close() })
	collClient := syncer.NewGRPCCollectionClient(collConn)

	repo := syncer.NewPostgresRepository(pool)
	svc := syncer.NewService(repo, collClient, rdb, a.Log)
	handler := syncer.NewHandler(svc)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         a.Base.Dev(),
		Register: func(gs *grpc.Server) {
			syncv1.RegisterSyncServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := syncv1.RegisterSyncServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register gateway: %w", err)
	}

	// Combine the gateway mux with the plain SSE route under one handler so
	// auth.HTTPAuthMiddleware wraps both identically.
	rootMux := http.NewServeMux()
	rootMux.Handle("/v1/sync/stream", httpx.Route("/v1/sync/stream", handler.StreamHandler(a.Log)))
	rootMux.Handle("/", gwMux)

	httpSrv := httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(rootMux),
		MetricsHandler: observability.InitMetrics(),
		// Only the owned store: without Redis, pull/apply still work and new streams get 503 from the hub.
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

	a.Log.Info("sync: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	// Ends with ctx and closes every SSE stream, so open streams don't hold up the HTTP drain.
	a.Go(svc.RunNotifyHub)
	a.Go(func(ctx context.Context) error { return syncer.RunNotifier(ctx, consumer, svc, a.Log) })
	a.Go(func(ctx context.Context) error { return syncer.RunAppliedOpsPurge(ctx, pool, a.Log) })
	return nil
}
