package auth

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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

// infraPrefixes are gRPC infrastructure services served without a token; reflection is registered in dev only.
var infraPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.v1.ServerReflection/",
	"/grpc.reflection.v1alpha.ServerReflection/",
}

func publicMatcher(public []string) func(string) bool {
	set := make(map[string]struct{}, len(public))
	for _, m := range public {
		set[m] = struct{}{}
	}
	return func(method string) bool {
		if _, ok := set[method]; ok {
			return true
		}
		for _, p := range infraPrefixes {
			if strings.HasPrefix(method, p) {
				return true
			}
		}
		return false
	}
}

// authenticate injects the caller of a valid access token into ctx.
func authenticate(ctx context.Context, v Verifier) (context.Context, error) {
	token, found := incomingBearer(ctx)
	if !found {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
	}
	claims, err := v.Verify(token)
	if err != nil || claims.Kind != "access" {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return WithIdentity(ctx, IdentityContext{
		UserID:    claims.Subject,
		ProfileID: claims.ProfileID,
		Locale:    claims.Locale,
		Roles:     claims.Roles,
	}), nil
}

// GRPCAuthInterceptor requires an access token on every method except health/reflection and the
// exact full method names in public.
func GRPCAuthInterceptor(v Verifier, public ...string) grpc.UnaryServerInterceptor {
	isPublic := publicMatcher(public)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isPublic(info.FullMethod) {
			return handler(ctx, req)
		}
		ctx, err := authenticate(ctx, v)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// GRPCAuthStreamInterceptor is the streaming counterpart of GRPCAuthInterceptor.
func GRPCAuthStreamInterceptor(v Verifier, public ...string) grpc.StreamServerInterceptor {
	isPublic := publicMatcher(public)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isPublic(info.FullMethod) {
			return handler(srv, ss)
		}
		ctx, err := authenticate(ss.Context(), v)
		if err != nil {
			return err
		}
		return handler(srv, identityStream{ServerStream: ss, ctx: ctx})
	}
}

type identityStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s identityStream) Context() context.Context { return s.ctx }

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
