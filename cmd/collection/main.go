// Command collection runs the MindMesh Collection service: gRPC plus a grpc-gateway REST edge, and
// its outbox relay.
package main

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/collection"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() { app.Main("collection", setup) }

func setup(ctx context.Context, a *app.App) error {
	pool, dbCfg, err := a.Postgres(ctx)
	if err != nil {
		return err
	}
	if err := a.Migrate(ctx, dbCfg.DSN, "platform", "collection"); err != nil {
		return err
	}
	verifier, err := a.Verifier(ctx)
	if err != nil {
		return err
	}

	if err := a.Relay(pool, "collection"); err != nil {
		return err
	}

	repo := collection.NewPostgresRepository(pool)
	svc := collection.NewService(repo, a.Log)
	handler := collection.NewHandler(svc)
	internalHandler := collection.NewInternalHandler(svc)

	services, err := auth.NewServiceVerifier(auth.CollectionInternalAudience,
		config.Str("SERVICE_TRUSTED_KEYS", "search=keys/search-service.pub.pem"))
	if err != nil {
		return fmt.Errorf("load trusted service keys: %w", err)
	}
	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCServiceOrUserAuthInterceptor(verifier, services, collection.InternalServicePolicy()),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         a.Base.Dev(),
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
	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := collectionv1.RegisterNoteTypeServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register note-type gateway: %w", err)
	}
	if err := collectionv1.RegisterNoteServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register note gateway: %w", err)
	}
	if err := collectionv1.RegisterCardServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register card gateway: %w", err)
	}
	if err := collectionv1.RegisterDeckServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register deck gateway: %w", err)
	}
	if err := collectionv1.RegisterFilteredDeckServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register filtered-deck gateway: %w", err)
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

	a.Log.Info("collection: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	return nil
}
