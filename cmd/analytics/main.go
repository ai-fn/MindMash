// Command analytics runs the MindMesh Analytics service: a gRPC (plus
// grpc-gateway REST) statistics API over a ClickHouse review-events mirror,
// together with a stats-aggregator consumer goroutine that ingests Scheduler's
// review.recorded stream into ClickHouse. One binary, two roles for the MVP;
// they can be split later.
package main

import (
	"context"
	"fmt"
	_ "time/tzdata" // parseTZ must not depend on the base image shipping zoneinfo

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	analyticsv1 "github.com/mindmesh/mindmesh/api/analytics/v1"
	"github.com/mindmesh/mindmesh/internal/analytics"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/clickhouse"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() { app.Main("analytics", setup) }

func setup(ctx context.Context, a *app.App) error {
	// The DDL is idempotent (IF NOT EXISTS), so applying it on every start is safe.
	chConn, err := clickhouse.New(ctx, clickhouse.LoadConfig())
	if err != nil {
		return fmt.Errorf("init clickhouse: %w", err)
	}
	a.OnClose(func() { _ = chConn.Close() })
	if err := clickhouse.RunMigrations(ctx, chConn, config.Str("CLICKHOUSE_MIGRATIONS_DIR", "migrations/clickhouse")); err != nil {
		return fmt.Errorf("run clickhouse migrations: %w", err)
	}

	verifier, err := a.Verifier(ctx)
	if err != nil {
		return err
	}
	// A distinct group keeps Analytics's offsets independent of other consumers on the review_log topic.
	consumer, err := a.Consumer("stats-aggregator")
	if err != nil {
		return err
	}

	aggregator := analytics.NewAggregator(chConn, a.Log)
	handler := analytics.NewHandler(analytics.NewRepo(chConn))

	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         a.Base.Dev(),
		Register: func(gs *grpc.Server) {
			analyticsv1.RegisterAnalyticsServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := analyticsv1.RegisterAnalyticsServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register analytics gateway: %w", err)
	}

	httpSrv := httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness: func(ctx context.Context) error {
			if err := auth.Ready(verifier); err != nil {
				return err
			}
			if err := chConn.Ping(ctx); err != nil {
				return fmt.Errorf("clickhouse: %w", err)
			}
			return nil
		},
	})

	a.Log.Info("analytics: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
		zap.String("topic", analytics.ReviewTopic),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	a.Go(func(ctx context.Context) error {
		return consumer.SubscribeBatch(ctx, []string{analytics.ReviewTopic}, analytics.Batch, aggregator.HandleBatch)
	})
	return nil
}
