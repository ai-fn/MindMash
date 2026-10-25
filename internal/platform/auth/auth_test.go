package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

// ---- helpers ----------------------------------------------------------------

func newEphemeralSigner(t *testing.T, cfg Config) *Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return NewSignerFromKeys(priv, pub, cfg)
}

func defaultCfg() Config {
	return Config{
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh-test",
	}
}

// mockVerifier allows tests to control Verify outcomes without real crypto.
type mockVerifier struct {
	fn func(tokenString string) (*Claims, error)
}

func (m *mockVerifier) Verify(tokenString string) (*Claims, error) {
	return m.fn(tokenString)
}

// ---- password ---------------------------------------------------------------

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash prefix: got %q, want prefix $argon2id$", hash)
	}

	ok, err := VerifyPassword("secret", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected VerifyPassword to return true for correct password")
	}

	ok, err = VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if ok {
		t.Error("expected VerifyPassword to return false for wrong password")
	}
}

func TestArgon2UniqueHashes(t *testing.T) {
	h1, err := HashPassword("same")
	if err != nil {
		t.Fatalf("first HashPassword: %v", err)
	}
	h2, err := HashPassword("same")
	if err != nil {
		t.Fatalf("second HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Error("expected two hashes of the same password to differ (unique salts)")
	}
}

// ---- JWT signing ------------------------------------------------------------

func TestSignAndVerifyAccess(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())

	tokenStr, jti, err := s.SignAccess("u1", "p1", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	if tokenStr == "" {
		t.Error("expected non-empty token string")
	}
	if jti == "" {
		t.Error("expected non-empty jti")
	}

	claims, err := s.Verify(tokenStr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "u1" {
		t.Errorf("Subject: got %q, want %q", claims.Subject, "u1")
	}
	if claims.ProfileID != "p1" {
		t.Errorf("ProfileID: got %q, want %q", claims.ProfileID, "p1")
	}
	if claims.Kind != "access" {
		t.Errorf("Kind: got %q, want %q", claims.Kind, "access")
	}
}

func TestSignAndVerifyRefresh(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())

	tokenStr, _, err := s.SignRefresh("u1", "p1")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	claims, err := s.Verify(tokenStr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Kind != "refresh" {
		t.Errorf("Kind: got %q, want %q", claims.Kind, "refresh")
	}
}

func TestSignRefreshWithID(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())

	tests := []struct {
		name      string
		userID    string
		profileID string
		jti       string
	}{
		{
			name:      "supplied jti is preserved in claims",
			userID:    "u1",
			profileID: "p1",
			jti:       "01JZABCDEF0000000000000000",
		},
		{
			name:      "empty profileID allowed",
			userID:    "u2",
			profileID: "",
			jti:       "01JZABCDEF0000000000000001",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenStr, err := s.SignRefreshWithID(tc.userID, tc.profileID, tc.jti)
			if err != nil {
				t.Fatalf("SignRefreshWithID: %v", err)
			}
			if tokenStr == "" {
				t.Fatal("expected non-empty token string")
			}

			claims, err := s.Verify(tokenStr)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if claims.ID != tc.jti {
				t.Errorf("claims.ID: got %q, want %q", claims.ID, tc.jti)
			}
			if claims.Kind != "refresh" {
				t.Errorf("Kind: got %q, want %q", claims.Kind, "refresh")
			}
			if claims.Subject != tc.userID {
				t.Errorf("Subject: got %q, want %q", claims.Subject, tc.userID)
			}
		})
	}
}

func TestVerifyExpired(t *testing.T) {
	cfg := defaultCfg()
	cfg.AccessTokenTTL = -time.Minute // past the clock leeway
	s := newEphemeralSigner(t, cfg)

	tokenStr, _, err := s.SignAccess("u1", "p1", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	_, err = s.Verify(tokenStr)
	if err == nil {
		t.Error("expected Verify to return an error for an expired token")
	}
}

func TestBuildJWKS(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	jwks := BuildJWKS(s)

	if len(jwks.Keys) != 1 {
		t.Fatalf("JWKS Keys length: got %d, want 1", len(jwks.Keys))
	}
	k := jwks.Keys[0]
	if k.Kty != "OKP" {
		t.Errorf("Kty: got %q, want OKP", k.Kty)
	}
	if k.Crv != "Ed25519" {
		t.Errorf("Crv: got %q, want Ed25519", k.Crv)
	}
	if k.X == "" {
		t.Error("expected non-empty X")
	}
	if k.Use != "sig" {
		t.Errorf("Use: got %q, want sig", k.Use)
	}
}

// ---- gRPC interceptor -------------------------------------------------------

func TestGRPCAuthInterceptor(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	interceptor := GRPCAuthInterceptor(s)

	validToken, _, err := s.SignAccess("u1", "p1", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	dummyHandler := func(ctx context.Context, req any) (any, error) {
		_, ok := IdentityFromContext(ctx)
		if !ok {
			t.Error("IdentityFromContext: identity not found in context")
		}
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.MyService/DoSomething"}

	tests := []struct {
		name       string
		authHeader string
		wantErr    bool
	}{
		{
			name:       "valid Bearer token",
			authHeader: "Bearer " + validToken,
			wantErr:    false,
		},
		{
			name:       "missing authorization header",
			authHeader: "",
			wantErr:    true,
		},
		{
			name:       "malformed Bearer",
			authHeader: "Token " + validToken,
			wantErr:    true,
		},
		{
			name:       "invalid garbage token",
			authHeader: "Bearer not.a.valid.jwt.at.all",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.New(nil)
			if tc.authHeader != "" {
				md.Set("authorization", tc.authHeader)
			}
			ctx := metadata.NewIncomingContext(context.Background(), md)

			_, err := interceptor(ctx, nil, info, dummyHandler)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGRPCAuthInterceptorSkipsHealth(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	interceptor := GRPCAuthInterceptor(s)

	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	ctx := context.Background()

	_, err := interceptor(ctx, nil, info, handler)
	if err != nil {
		t.Errorf("unexpected error for health method: %v", err)
	}
	if !called {
		t.Error("expected handler to be called for health check method")
	}
}

// ---- HTTP middleware ---------------------------------------------------------

func TestHTTPAuthMiddleware(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	middleware := HTTPAuthMiddleware(s)

	validToken, _, err := s.SignAccess("u1", "p1", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	tests := []struct {
		name         string
		path         string
		authHeader   string
		wantStatus   int
		wantIdentity bool
	}{
		{
			name:         "public path no token",
			path:         "/healthz",
			authHeader:   "",
			wantStatus:   http.StatusOK,
			wantIdentity: false,
		},
		{
			name:         "protected path valid token",
			path:         "/api/resource",
			authHeader:   "Bearer " + validToken,
			wantStatus:   http.StatusOK,
			wantIdentity: true,
		},
		{
			name:         "protected path no token",
			path:         "/api/resource",
			authHeader:   "",
			wantStatus:   http.StatusUnauthorized,
			wantIdentity: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			identityFound := false

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, ok := IdentityFromContext(r.Context())
				identityFound = ok
				w.WriteHeader(http.StatusOK)
			})

			handler := middleware(inner)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantIdentity && !identityFound {
				t.Error("expected identity in context, not found")
			}
			// All 401 responses must carry JSON Content-Type.
			if tc.wantStatus == http.StatusUnauthorized {
				if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type on 401: got %q, want %q", ct, "application/json")
				}
			}
		})
	}
}

// TestHTTPAuthMiddlewareRejectsRefreshToken proves that a valid but refresh-kind
// token is rejected by HTTPAuthMiddleware with 401 application/json.
func TestHTTPAuthMiddlewareRejectsRefreshToken(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	middleware := HTTPAuthMiddleware(s)

	refreshToken, _, err := s.SignRefresh("u1", "p1")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called for a refresh token")
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	req.Header.Set("Authorization", "Bearer "+refreshToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}
}

// TestGRPCAuthInterceptorRejectsRefreshToken proves that a valid but refresh-kind
// token is rejected by GRPCAuthInterceptor with codes.Unauthenticated.
func TestGRPCAuthInterceptorRejectsRefreshToken(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	interceptor := GRPCAuthInterceptor(s)

	refreshToken, _, err := s.SignRefresh("u1", "p1")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	handler := func(ctx context.Context, req any) (any, error) {
		t.Error("handler must not be called for a refresh token")
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.MyService/DoSomething"}
	md := metadata.New(map[string]string{"authorization": "Bearer " + refreshToken})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err = interceptor(ctx, nil, info, handler)
	if err == nil {
		t.Fatal("expected error for refresh token, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("gRPC code: got %v, want %v", st.Code(), codes.Unauthenticated)
	}
}

// TestGRPCAuthInterceptorInvalidToken uses a mock verifier to confirm that
// Verify errors propagate as Unauthenticated without panicking.
func TestGRPCAuthInterceptorInvalidTokenViaMock(t *testing.T) {
	mv := &mockVerifier{
		fn: func(tokenString string) (*Claims, error) {
			return nil, apperr.Unauthenticated("invalid token", nil)
		},
	}
	interceptor := GRPCAuthInterceptor(mv)

	info := &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.MyService/DoSomething"}
	md := metadata.New(map[string]string{"authorization": "Bearer garbage.token.value"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, req any) (any, error) {
		t.Error("handler must not be called for an invalid token")
		return nil, nil
	}

	_, err := interceptor(ctx, nil, info, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("gRPC code: got %v, want %v", st.Code(), codes.Unauthenticated)
	}
}

func TestVerifyEnforcesTokenShape(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	good, _, err := s.SignAccess("u1", "p1", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := s.Verify(good); err != nil || len(c.Audience) != 1 || c.Audience[0] != Audience {
		t.Fatalf("want a token carrying aud=%q, got %+v, %v", Audience, c, err)
	}

	sign := func(mutate func(*Claims)) string {
		now := time.Now()
		c := Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject: "u1", Issuer: s.cfg.Issuer, Audience: jwt.ClaimStrings{Audience},
				IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
			Kind: "access",
		}
		mutate(&c)
		tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
		tok.Header["kid"] = s.kid
		str, err := tok.SignedString(s.priv)
		if err != nil {
			t.Fatal(err)
		}
		return str
	}
	if _, err := s.Verify(sign(func(*Claims) {})); err != nil {
		t.Fatalf("control token should verify: %v", err)
	}
	for name, tok := range map[string]string{
		"no audience":      sign(func(c *Claims) { c.Audience = nil }),
		"other audience":   sign(func(c *Claims) { c.Audience = jwt.ClaimStrings{"collection-internal"} }),
		"no expiry":        sign(func(c *Claims) { c.ExpiresAt = nil }),
		"issued in future": sign(func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }),
	} {
		if _, err := s.Verify(tok); err == nil {
			t.Errorf("%s: want rejection", name)
		}
	}

	hs, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject: "u1", Issuer: s.cfg.Issuer, Audience: jwt.ClaimStrings{Audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}, Kind: "access"}).SignedString([]byte(s.pub))
	if _, err := s.Verify(hs); err == nil {
		t.Error("HS256 signed with the public key bytes: want rejection")
	}
}
