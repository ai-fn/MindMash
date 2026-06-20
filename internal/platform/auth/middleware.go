package auth

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// IdentityContext holds the authenticated caller's identity.
type IdentityContext struct {
	UserID    string
	ProfileID string
	Locale    string
	Roles     []string
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, id IdentityContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IdentityFromContext retrieves the IdentityContext stored by WithIdentity.
func IdentityFromContext(ctx context.Context) (IdentityContext, bool) {
	id, ok := ctx.Value(ctxKey{}).(IdentityContext)
	return id, ok
}

// RequireIdentity retrieves the IdentityContext stored by WithIdentity, or
// returns a typed Unauthenticated error if the auth interceptor/middleware
// did not inject one. Every service's gRPC handler calls this at the top of
// each RPC instead of duplicating the IdentityFromContext-plus-error-mapping
// pattern locally.
func RequireIdentity(ctx context.Context) (IdentityContext, error) {
	id, ok := IdentityFromContext(ctx)
	if !ok {
		return IdentityContext{}, platformerrors.Unauthenticated("missing identity context")
	}
	return id, nil
}

// Verifier is the subset of *Signer used by middleware.
type Verifier interface {
	Verify(tokenString string) (*Claims, error)
}

// GRPCAuthInterceptor returns a gRPC unary interceptor that validates Bearer JWTs.
func GRPCAuthInterceptor(v Verifier) grpc.UnaryServerInterceptor {
	publicMethods := []string{
		"grpc.health",
		"grpc.reflection",
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		for _, skip := range publicMethods {
			if strings.Contains(info.FullMethod, skip) {
				return handler(ctx, req)
			}
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
		}

		vals := md.Get("authorization")
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
		}

		token, found := parseBearerToken(vals[0])
		if !found {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
		}

		claims, err := v.Verify(token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Only access tokens are valid API credentials; a refresh token presented
		// here indicates a confused or malicious client and must be rejected.
		if claims.Kind != "access" {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		ctx = WithIdentity(ctx, IdentityContext{
			UserID:    claims.Subject,
			ProfileID: claims.ProfileID,
			Locale:    claims.Locale,
			Roles:     claims.Roles,
		})
		return handler(ctx, req)
	}
}

// HTTPAuthMiddleware returns an HTTP middleware that validates Bearer JWTs.
func HTTPAuthMiddleware(v Verifier) func(http.Handler) http.Handler {
	publicPaths := map[string]struct{}{
		"/healthz":               {},
		"/readyz":                {},
		"/.well-known/jwks.json": {},
		"/metrics":               {},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, isPublic := publicPaths[r.URL.Path]; isPublic {
				next.ServeHTTP(w, r)
				return
			}

			token, found := parseBearerToken(r.Header.Get("Authorization"))
			if !found {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}

			claims, err := v.Verify(token)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}

			// Only access tokens are valid API credentials; a refresh token presented
			// here indicates a confused or malicious client and must be rejected.
			if claims.Kind != "access" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}

			r = r.WithContext(WithIdentity(r.Context(), IdentityContext{
				UserID:    claims.Subject,
				ProfileID: claims.ProfileID,
				Locale:    claims.Locale,
				Roles:     claims.Roles,
			}))
			next.ServeHTTP(w, r)
		})
	}
}

// parseBearerToken extracts the token from a "Bearer <token>" header value.
func parseBearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
