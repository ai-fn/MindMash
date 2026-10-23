// Command search runs the MindMesh Search service: a gRPC (plus grpc-gateway
// REST) query endpoint over an OpenSearch card index, together with an indexer
// consumer goroutine that keeps the index in sync with Collection change
// events. One binary, two roles for the MVP; they can be split later.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	searchv1 "github.com/mindmesh/mindmesh/api/search/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
	"github.com/mindmesh/mindmesh/internal/search"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "search: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// --backfill reindexes the given users (fills per-language buckets on old cards) and exits.
	backfillTenants := flag.String("backfill", "", "comma-separated user ids to reindex, then exit")
	flag.Parse()

	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("search: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("search: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("search: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("search: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("search: tracer shutdown", zap.Error(err))
		}
	}()

	// OpenSearch repo + index/alias bootstrap (template, current index, alias, and
	// a one-time reindex from a pre-alias cards-v1 index if present).
	repo, err := search.NewRepo(search.LoadRepoConfig())
	if err != nil {
		return fmt.Errorf("search: init opensearch: %w", err)
	}
	if err := repo.EnsureIndex(ctx); err != nil {
		return fmt.Errorf("search: ensure index: %w", err)
	}

	// The indexer authenticates to Collection with its own service key, never Identity's.
	serviceTokens, err := auth.NewServiceSigner("search", envOr("SERVICE_SIGNING_KEY_PATH", "keys/search-service.pem"))
	if err != nil {
		return fmt.Errorf("search: init service signer: %w", err)
	}
	verifier, err := userVerifier(ctx, logger)
	if err != nil {
		return err
	}

	// Collection internal client (GetSearchDocs enrichment).
	collAddr := envOr("COLLECTION_GRPC_ADDR", "localhost:9090")
	collConn, err := grpcx.Dial(ctx, collAddr)
	if err != nil {
		return fmt.Errorf("search: dial collection: %w", err)
	}
	defer func() { _ = collConn.Close() }()
	collClient := collectionv1.NewCollectionInternalServiceClient(collConn)

	// Kafka consumer for the indexer. A distinct group keeps Search's offsets
	// independent of any other consumer.
	eventsCfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("search: load events config: %w", err)
	}
	eventsCfg.GroupID = cmp.Or(eventsCfg.GroupID, "search-indexer")
	consumer, err := events.NewConsumer(eventsCfg, logger)
	if err != nil {
		return fmt.Errorf("search: init kafka consumer: %w", err)
	}
	defer func() { _ = consumer.Close() }()

	indexer := search.NewIndexer(repo, collClient, serviceTokens, logger)

	// Backfill mode: reindex the named tenants and exit (no servers, no consumer).
	if *backfillTenants != "" {
		return runBackfill(ctx, indexer, logger, *backfillTenants)
	}

	handler := search.NewHandler(repo)

	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			auth.GRPCAuthInterceptor(verifier),
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(verifier)},
		Reflection:         base.Env == "dev",
		Register: func(gs *grpc.Server) {
			searchv1.RegisterSearchServiceServer(gs, handler)
		},
	})

	gwMux := runtime.NewServeMux(httpx.GatewayRoutes())
	if err := searchv1.RegisterSearchServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("search: register search gateway: %w", err)
	}

	readiness := func(ctx context.Context) error {
		if err := auth.Ready(verifier); err != nil {
			return err
		}
		if err := repo.Ping(ctx); err != nil {
			return fmt.Errorf("opensearch: %w", err)
		}
		return nil
	}

	httpSrv := httpx.New(httpx.DefaultConfig(base.HTTPAddr), httpx.Options{
		Handler:        auth.HTTPAuthMiddleware(verifier)(gwMux),
		MetricsHandler: observability.InitMetrics(),
		Readiness:      readiness,
	})

	logger.Info("search: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
		zap.Strings("topics", search.IndexTopics),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	g.Go(func() error { return consumer.Subscribe(gctx, search.IndexTopics, indexer.Handle) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("search: server: %w", err)
	}
	logger.Info("search: stopped")
	return nil
}

// runBackfill reindexes each comma-separated user id in spec, then returns.
func runBackfill(ctx context.Context, indexer *search.Indexer, logger *zap.Logger, spec string) error {
	for _, t := range strings.Split(spec, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		userID := t
		n, err := indexer.Backfill(ctx, userID)
		if err != nil {
			return fmt.Errorf("search: backfill %s: %w", userID, err)
		}
		logger.Info("search: backfilled tenant", zap.String("user_id", userID), zap.Int("cards", n))
	}
	return nil
}

// userVerifier verifies query-path JWTs via JWKS; only the dev fallback loads Identity's local key.
func userVerifier(ctx context.Context, logger *zap.Logger) (auth.Verifier, error) {
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		rv, err := auth.NewRemoteVerifier(ctx, jwksURL, envOr("JWKS_ISSUER", ""))
		if err != nil {
			return nil, fmt.Errorf("search: init jwks verifier: %w", err)
		}
		logger.Info("search: using remote JWKS verifier", zap.String("url", jwksURL))
		return rv, nil
	}
	authCfg, err := auth.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("search: load auth config: %w", err)
	}
	signer, err := auth.NewSigner(authCfg)
	if err != nil {
		return nil, fmt.Errorf("search: init local key verifier: %w", err)
	}
	logger.Info("search: using local key verifier (dev fallback)")
	return signer, nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
