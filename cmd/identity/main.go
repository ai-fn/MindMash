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
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	identityv1 "github.com/mindmesh/mindmesh/api/identity/v1"
	"github.com/mindmesh/mindmesh/internal/identity"
	"github.com/mindmesh/mindmesh/internal/platform/app"
	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/grpcx"
	"github.com/mindmesh/mindmesh/internal/platform/httpx"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

func main() { app.Main("identity", setup) }

func setup(ctx context.Context, a *app.App) error {
	pool, dbCfg, err := a.Postgres(ctx)
	if err != nil {
		return err
	}
	if err := a.Migrate(ctx, dbCfg.DSN, "identity"); err != nil {
		return err
	}
	rdb, err := a.Redis(ctx)
	if err != nil {
		return err
	}

	// JWT signer (Ed25519 keys from disk).
	authCfg, err := auth.LoadConfig()
	if err != nil {
		return err
	}
	signer, err := auth.NewSigner(authCfg)
	if err != nil {
		return fmt.Errorf("build signer: %w", err)
	}

	repo := identity.NewPostgresRepository(pool)
	exchanger := identity.NewOIDCExchanger(oauthProvidersFromEnv())
	logSecrets, err := config.Bool("LOG_EMAIL_SECRETS", false)
	if err != nil {
		return err
	}
	email := identity.NewLogEmailSender(a.Log, logSecrets)
	redirectURIs := splitList(config.Str("OAUTH_REDIRECT_URIS", ""))
	if err := identity.ValidateRedirectURIs(redirectURIs); err != nil {
		return err
	}
	if len(redirectURIs) == 0 {
		a.Log.Warn("identity: OAUTH_REDIRECT_URIS is empty; social sign-in is disabled")
	}
	svc := identity.NewService(repo, signer, exchanger, email, a.Log, authCfg,
		identity.WithRateLimiter(identity.NewRedisRateLimiter(rdb)),
		identity.WithOAuth(identity.NewRedisOAuthStateStore(rdb), redirectURIs))
	if err := svc.WarmUp(); err != nil {
		return fmt.Errorf("warm up: %w", err)
	}
	// Registered after the pool, so it runs before the pool closes.
	a.OnClose(func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		svc.Drain(drainCtx)
	})
	handler := identity.NewHandler(svc)

	public := identity.PublicMethods()
	grpcSrv := grpcx.New(grpcx.DefaultConfig(a.Base.GRPCAddr), grpcx.Options{
		UnaryInterceptors:  []grpc.UnaryServerInterceptor{auth.GRPCAuthInterceptor(signer, public...)},
		StreamInterceptors: []grpc.StreamServerInterceptor{auth.GRPCAuthStreamInterceptor(signer, public...)},
		Reflection:         a.Base.Dev(),
		Register: func(gs *grpc.Server) {
			identityv1.RegisterAuthServiceServer(gs, handler)
			identityv1.RegisterProfileServiceServer(gs, handler)
		},
	})

	// REST gateway, registered in-process (no extra network hop), so it bypasses the gRPC interceptors.
	gwMux := runtime.NewServeMux(httpx.GatewayErrors(localizedErrorHandler), httpx.GatewayRoutes())
	if err := identityv1.RegisterAuthServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register auth gateway: %w", err)
	}
	if err := identityv1.RegisterProfileServiceHandlerServer(ctx, gwMux, handler); err != nil {
		return fmt.Errorf("register profile gateway: %w", err)
	}

	jwksHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(svc.JWKS()); err != nil {
			a.Log.Error("identity: encode jwks", zap.Error(err))
		}
	})

	httpSrv := httpx.New(httpx.DefaultConfig(a.Base.HTTPAddr), httpx.Options{
		// injectIdentity resolves the JWT (populating IdentityContext, incl. Locale) first, then
		// i18n.HTTPMiddleware resolves the request locale (claim → Accept-Language → English)
		// into the context for the handlers and the localized error handler.
		Handler:        injectIdentity(signer)(i18n.HTTPMiddleware(gwMux)),
		MetricsHandler: observability.InitMetrics(),
		// Only the owned store: Redis (rate limits, OAuth state) failing must not pull every pod out.
		Readiness: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("postgres: %w", err)
			}
			return nil
		},
		JWKSHandler: jwksHandler,
	})

	a.Log.Info("identity: starting",
		zap.String("grpc_addr", a.Base.GRPCAddr),
		zap.String("http_addr", a.Base.HTTPAddr),
	)
	a.Go(grpcSrv.Start)
	a.Go(httpSrv.Start)
	a.Go(func(ctx context.Context) error { return identity.RunPurge(ctx, repo, a.Log) })
	return nil
}

// localizedErrorHandler is the grpc-gateway error handler for the REST edge. When the returned
// error is an *AppError tagged with a localizable MsgID, it re-renders the status message in the
// request's resolved locale (carried in ctx by i18n.HTTPMiddleware) before delegating to the
// default handler. Errors without a MsgID pass through unchanged.
func localizedErrorHandler(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.AppError
	if errors.As(err, &ae) && ae.MsgID != "" {
		localized := ae.Localize(i18n.PrinterFromContext(ctx))
		err = status.New(ae.Code, localized).Err()
	}
	runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, r, err)
}

// injectIdentity makes REST auth optional; protected handlers enforce it via auth.IdentityFromContext.
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
		issuer := config.Str("OAUTH_"+up+"_ISSUER", "")
		clientID := config.Str("OAUTH_"+up+"_CLIENT_ID", "")
		if issuer == "" || clientID == "" {
			continue
		}
		secret := config.Str("OAUTH_"+up+"_CLIENT_SECRET", "")
		providers[name] = identity.ProviderConfig{
			IssuerURL:    issuer,
			ClientID:     clientID,
			ClientSecret: secret,
		}
	}
	return providers
}

func splitList(v string) []string {
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
