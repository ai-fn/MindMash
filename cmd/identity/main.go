// Command identity runs the MindMesh Identity service: gRPC (internal) plus a
// grpc-gateway REST edge, with health, metrics, JWKS, and graceful shutdown.
//
// This main is the reference composition for every later service — a service is
// a thin wiring of internal/platform/* and its internal/<svc> domain.  Copy
// this file, swap the domain wiring, and adjust which generated handlers are
// registered.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	identityv1 "github.com/mindmesh/mindmesh/api/identity/v1"
	"github.com/mindmesh/mindmesh/internal/identity"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/cache"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		// log may not be initialised yet on early failures, so use stderr too.
		fmt.Fprintf(os.Stderr, "identity: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base, err := config.Load()
	if err != nil {
		return fmt.Errorf("identity: load config: %w", err)
	}

	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("identity: init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	// Cancel everything on SIGINT/SIGTERM; the servers below watch this context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Tracing — exporter dials lazily, so a missing collector does not block boot.
	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return fmt.Errorf("identity: load observability config: %w", err)
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("identity: init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("identity: tracer shutdown", zap.Error(err))
		}
	}()

	// Postgres.
	dbCfg, err := db.LoadConfig()
	if err != nil {
		return fmt.Errorf("identity: load db config: %w", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("identity: connect db: %w", err)
	}
	defer pool.Close()

	if migrationsEnabled(base.Env) {
		dir := envOr("IDENTITY_MIGRATIONS_DIR", "migrations/identity")
		if err := db.RunMigrations(ctx, dbCfg.DSN, dir); err != nil {
			return fmt.Errorf("identity: run migrations: %w", err)
		}
		logger.Info("identity: migrations applied", zap.String("dir", dir))
	}

	// Redis.
	cacheCfg, err := cache.LoadConfig()
	if err != nil {
		return fmt.Errorf("identity: load cache config: %w", err)
	}
	rdb, err := cache.New(ctx, cacheCfg)
	if err != nil {
		return fmt.Errorf("identity: connect redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// JWT signer (Ed25519 keys from disk).
	authCfg, err := auth.LoadConfig()
	if err != nil {
		return fmt.Errorf("identity: load auth config: %w", err)
	}
	signer, err := auth.NewSigner(authCfg)
	if err != nil {
		return fmt.Errorf("identity: build signer: %w", err)
	}

	// Domain wiring.
	repo := identity.NewPostgresRepository(pool)
	sessions := identity.NewRedisSessionStore(rdb)
	exchanger := identity.NewOIDCExchanger(oauthProvidersFromEnv())
	email := identity.NewLogEmailSender(logger)
	svc := identity.NewService(repo, sessions, signer, exchanger, email, logger, authCfg)
	handler := identity.NewHandler(svc)

	// gRPC server.  The user-facing JWT check lives at the REST edge below;
	// gRPC is an internal/trusted surface (the gateway calls InternalAuthService,
	// and other services may call it over the cluster network).
	grpcSrv := grpcx.New(grpcx.DefaultConfig(base.GRPCAddr), grpcx.Options{
		Register: func(gs *grpc.Server) {
			identityv1.RegisterAuthServiceServer(gs, handler)
			identityv1.RegisterProfileServiceServer(gs, handler)
			identityv1.RegisterInternalAuthServiceServer(gs, handler)
		},
	})

	// REST gateway, registered in-process (no extra network hop).  The internal
	// service is intentionally NOT exposed over REST.
	gwMux := runtime.NewServeMux(runtime.WithErrorHandler(localizedErrorHandler))
	if err := identityv1.RegisterAuthServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("identity: register auth gateway: %w", err)
	}
	if err := identityv1.RegisterProfileServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("identity: register profile gateway: %w", err)
	}

	jwksHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(svc.JWKS()); err != nil {
			logger.Error("identity: encode jwks", zap.Error(err))
		}
	})

	readiness := func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	}

	httpSrv := httpx.New(httpx.DefaultConfig(base.HTTPAddr), httpx.Options{
		// injectIdentity resolves the JWT (populating IdentityContext, incl. Locale) first, then
		// i18n.HTTPMiddleware resolves the request locale (claim → Accept-Language → English)
		// into the context for the handlers and the localized error handler.
		Handler:        injectIdentity(signer)(i18n.HTTPMiddleware(gwMux)),
		MetricsHandler: observability.InitMetrics(),
		Readiness:      readiness,
		JWKSHandler:    jwksHandler,
	})

	logger.Info("identity: starting",
		zap.String("grpc_addr", base.GRPCAddr),
		zap.String("http_addr", base.HTTPAddr),
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return grpcSrv.Start(gctx) })
	g.Go(func() error { return httpSrv.Start(gctx) })
	if err := g.Wait(); err != nil {
		return fmt.Errorf("identity: server: %w", err)
	}
	logger.Info("identity: stopped")
	return nil
}

// injectIdentity adds the authenticated caller's identity to the request context
// when a valid access token is present, leaving the request otherwise untouched.
// Identity's REST surface mixes public endpoints (login/register/refresh/oauth)
// with protected ones (me/profiles); rather than maintain a path allow-list here,
// authentication is optional at the edge and each protected handler enforces its
// own requirement via auth.IdentityFromContext.
// localizedErrorHandler is the grpc-gateway error handler for the REST edge. When the returned
// error is an *AppError tagged with a localizable MsgID, it re-renders the status message in the
// request's resolved locale (carried in ctx by i18n.HTTPMiddleware) before delegating to the
// default handler. Errors without a MsgID pass through unchanged.
func localizedErrorHandler(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	var ae *platformerrors.AppError
	if errors.As(err, &ae) && ae.MsgID != "" {
		localized := ae.Localize(i18n.PrinterFromContext(ctx))
		err = status.New(ae.Code, localized).Err()
	}
	runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, r, err)
}

func injectIdentity(v auth.Verifier) func(http.Handler) http.Handler {
	const prefix = "Bearer "
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if strings.HasPrefix(authz, prefix) {
				if token := strings.TrimSpace(authz[len(prefix):]); token != "" {
					if claims, err := v.Verify(token); err == nil && claims.Kind == "access" {
						r = r.WithContext(auth.WithIdentity(r.Context(), auth.IdentityContext{
							UserID:    claims.Subject,
							ProfileID: claims.ProfileID,
							Locale:    claims.Locale,
							Roles:     claims.Roles,
						}))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// oauthProvidersFromEnv reads per-provider OIDC settings from the environment.
// A provider is enabled only when both its issuer URL and client ID are set, so
// the service can run with no social login configured.
func oauthProvidersFromEnv() map[string]identity.ProviderConfig {
	providers := make(map[string]identity.ProviderConfig)
	for _, name := range []string{"google", "apple"} { // OIDC issuers only: GitHub has no discovery or id_token
		up := strings.ToUpper(name)
		issuer, hasIssuer := os.LookupEnv("OAUTH_" + up + "_ISSUER")
		clientID, hasClient := os.LookupEnv("OAUTH_" + up + "_CLIENT_ID")
		if !hasIssuer || !hasClient || issuer == "" || clientID == "" {
			continue
		}
		secret, _ := os.LookupEnv("OAUTH_" + up + "_CLIENT_SECRET")
		providers[name] = identity.ProviderConfig{
			IssuerURL:    issuer,
			ClientID:     clientID,
			ClientSecret: secret,
		}
	}
	return providers
}

// migrationsEnabled reports whether migrations should run at boot.  Defaults to
// on in dev for a frictionless local loop; RUN_MIGRATIONS overrides explicitly.
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
