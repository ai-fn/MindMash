// Command search runs the MindMesh Search service: the query API over OpenSearch plus the indexer
// consumer that follows Collection's change events.
package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	searchv1 "github.com/mindmesh/mindmesh/api/search/v1"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/search"
)

func main() {
	// --backfill reindexes the given users (fills per-language buckets on old cards) and exits.
	backfill := flag.String("backfill", "", "comma-separated user ids to reindex, then exit")
	flag.Parse()
	app.Main("search", func(ctx context.Context, a *app.App) error { return setup(ctx, a, *backfill) })
}

func setup(ctx context.Context, a *app.App, backfill string) error {
	// OpenSearch repo + index/alias bootstrap (template, current index, alias, and
	// a one-time reindex from a pre-alias cards-v1 index if present).
	repo, err := search.NewRepo(search.LoadRepoConfig())
	if err != nil {
		return fmt.Errorf("init opensearch: %w", err)
	}
	if err := repo.EnsureIndex(ctx); err != nil {
		return fmt.Errorf("ensure index: %w", err)
	}

	// The indexer authenticates to Collection with its own service key, never Identity's.
	serviceTokens, err := auth.NewServiceSigner("search", config.Str("SERVICE_SIGNING_KEY_PATH", "keys/search-service.pem"))
	if err != nil {
		return fmt.Errorf("init service signer: %w", err)
	}

	// Collection internal client (GetSearchDocs enrichment).
	// Every internal method is a read, so all are safe to retry.
	collConn, err := grpcx.Dial(ctx, config.Str("COLLECTION_GRPC_ADDR", "localhost:9090"),
		grpcx.RetryOn(collectionv1.CollectionInternalService_ServiceDesc.ServiceName))
	if err != nil {
		return fmt.Errorf("dial collection: %w", err)
	}
	a.OnClose(func() { _ = collConn.Close() })
	collClient := collectionv1.NewCollectionInternalServiceClient(collConn)

	indexer := search.NewIndexer(repo, collClient, serviceTokens, a.Log)
	if backfill != "" {
		return runBackfill(ctx, indexer, a.Log, backfill)
	}

	verifier, err := a.Verifier(ctx)
	if err != nil {
		return err
	}
	// A distinct group keeps Search's offsets independent of any other consumer.
	consumer, err := a.Consumer("search-indexer")
	if err != nil {
		return err
	}

	handler := search.NewHandler(repo)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         a.Base.Dev(),
		Register: func(gs *grpc.Server) {
			searchv1.RegisterSearchServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes(), httpx.GatewayErrors(nil))
	if err := searchv1.RegisterSearchServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register search gateway: %w", err)
	}

	httpSrv := httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness: func(ctx context.Context) error {
			if err := auth.Ready(verifier); err != nil {
				return err
			}
			if err := repo.Ping(ctx); err != nil {
				return fmt.Errorf("opensearch: %w", err)
			}
			return nil
		},
	})

	a.Log.Info("search: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
		zap.Strings("topics", search.IndexTopics),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	a.Go(func(ctx context.Context) error { return consumer.Subscribe(ctx, search.IndexTopics, indexer.Handle) })
	return nil
}

// runBackfill reindexes each comma-separated user id in spec, then returns.
func runBackfill(ctx context.Context, indexer *search.Indexer, logger *zap.Logger, spec string) error {
	for _, t := range strings.Split(spec, ",") {
		userID := strings.TrimSpace(t)
		if userID == "" {
			continue
		}
		n, err := indexer.Backfill(ctx, userID)
		if err != nil {
			return fmt.Errorf("backfill %s: %w", userID, err)
		}
		logger.Info("search: backfilled tenant", zap.String("user_id", userID), zap.Int("cards", n))
	}
	return nil
}
