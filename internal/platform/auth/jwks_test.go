package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// serveJWKS returns an httptest.Server that serves the JWKS for the given
// signers and counts how many times the endpoint was hit.
func serveJWKS(t *testing.T, signers ...*Signer) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		jwks := BuildJWKS(signers...)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return NewSignerFromKeys(priv, pub, Config{
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh-test",
	})
}

// TestRemoteVerifier_ValidAccessToken: serve JWKS, sign a token, verify it.
func TestRemoteVerifier_ValidAccessToken(t *testing.T) {
	s := newTestSigner(t)
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	tokenStr, _, err := s.SignAccess("user1", "profile1", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	claims, err := rv.Verify(tokenStr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user1" {
		t.Errorf("Subject: got %q, want %q", claims.Subject, "user1")
	}
	if claims.ProfileID != "profile1" {
		t.Errorf("ProfileID: got %q, want %q", claims.ProfileID, "profile1")
	}
	if claims.Kind != "access" {
		t.Errorf("Kind: got %q, want %q", claims.Kind, "access")
	}
}

// TestRemoteVerifier_UnknownKIDTriggersOneRefresh: a token signed by a key
// not present at construction time causes exactly one refresh call, bringing
// the total hit count to 2 (1 initial + 1 refresh).
func TestRemoteVerifier_UnknownKIDTriggersOneRefresh(t *testing.T) {
	// First signer served at construction.
	s1 := newTestSigner(t)
	// Second signer added to the JWKS after construction.
	s2 := newTestSigner(t)

	var serveS2 atomic.Bool // controls whether s2 appears in the JWKS

	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var jwks JWKS
		if serveS2.Load() {
			jwks = BuildJWKS(s1, s2)
		} else {
			jwks = BuildJWKS(s1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	// Now expose s2 in the JWKS and set lastRefresh far enough back that the
	// rate-limit does not block the refresh.
	serveS2.Store(true)
	rv.mu.Lock()
	rv.lastRefresh = time.Now().Add(-2 * minRefreshInterval)
	rv.mu.Unlock()

	// Sign a token with s2 — kid not yet in cache.
	tokenStr, _, err := s2.SignAccess("user2", "profile2", "en", []string{"user"})
	if err != nil {
		t.Fatalf("SignAccess(s2): %v", err)
	}

	claims, err := rv.Verify(tokenStr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user2" {
		t.Errorf("Subject: got %q, want user2", claims.Subject)
	}

	// Endpoint must have been hit exactly twice: once at construction, once on refresh.
	if n := hits.Load(); n != 2 {
		t.Errorf("JWKS endpoint hit count: got %d, want 2", n)
	}
}

// TestRemoteVerifier_ExpiredToken: an expired token returns an error.
func TestRemoteVerifier_ExpiredToken(t *testing.T) {
	cfg := Config{
		AccessTokenTTL:  -1 * time.Second, // already expired
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh-test",
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s := NewSignerFromKeys(priv, pub, cfg)
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	tokenStr, _, err := s.SignAccess("u1", "p1", "en", nil)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	_, err = rv.Verify(tokenStr)
	if err == nil {
		t.Error("expected Verify to return an error for an expired token, got nil")
	}
}

// TestRemoteVerifier_WrongKindRefreshToken: Verify must reject a refresh token
// because RemoteVerifier.Verify enforces kind == "access".
func TestRemoteVerifier_WrongKindRefreshToken(t *testing.T) {
	s := newTestSigner(t)
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	tokenStr, _, err := s.SignRefresh("u1", "p1")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	_, err = rv.Verify(tokenStr)
	if err == nil {
		t.Error("expected Verify to return an error for a refresh token, got nil")
	}
}

// TestRemoteVerifier_IssuerMismatch: a token signed with a different issuer
// must be rejected when the RemoteVerifier is configured with a specific issuer.
func TestRemoteVerifier_IssuerMismatch(t *testing.T) {
	// Sign the token with "wrong-issuer" but the verifier expects "expected-issuer".
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s := NewSignerFromKeys(priv, pub, Config{
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "wrong-issuer",
	})
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "expected-issuer")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	tokenStr, _, err := s.SignAccess("u1", "p1", "en", nil)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	_, err = rv.Verify(tokenStr)
	if err == nil {
		t.Error("expected Verify to return an error for issuer mismatch, got nil")
	}
}

// TestRemoteVerifier_CacheHit: after the initial fetch, a second Verify for
// a known kid must not hit the JWKS endpoint again.
func TestRemoteVerifier_CacheHit(t *testing.T) {
	s := newTestSigner(t)
	srv, hits := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	tokenStr, _, err := s.SignAccess("u1", "p1", "en", nil)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	// First verify.
	if _, err := rv.Verify(tokenStr); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	// Second verify.
	if _, err := rv.Verify(tokenStr); err != nil {
		t.Fatalf("second Verify: %v", err)
	}

	// Only the initial fetch should have hit the endpoint.
	if n := hits.Load(); n != 1 {
		t.Errorf("JWKS endpoint hit count: got %d, want 1", n)
	}
}

// TestNewRemoteVerifier_EmptyURL: NewRemoteVerifier must return an error when
// the JWKS URL is empty.
func TestNewRemoteVerifier_EmptyURL(t *testing.T) {
	_, err := NewRemoteVerifier(context.Background(), "", "mindmesh-test")
	if err == nil {
		t.Fatal("expected error for empty JWKS URL, got nil")
	}
}

// TestNewRemoteVerifier_InitialFetchFailure: NewRemoteVerifier must return an
// error when the server returns a non-200 status on the initial fetch.
func TestNewRemoteVerifier_InitialFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := NewRemoteVerifier(context.Background(), srv.URL+"/jwks.json", "mindmesh-test")
	if err == nil {
		t.Fatal("expected error when server returns 500, got nil")
	}
}

// TestRemoteVerifier_GarbageToken: Verify must return an error for a
// syntactically malformed JWT string.
func TestRemoteVerifier_GarbageToken(t *testing.T) {
	s := newTestSigner(t)
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	_, err = rv.Verify("not-a-jwt")
	if err == nil {
		t.Fatal("expected error for malformed JWT, got nil")
	}
}

// TestRemoteVerifier_MissingKIDHeader: Verify must return an error when the
// JWT header lacks a "kid" field.
func TestRemoteVerifier_MissingKIDHeader(t *testing.T) {
	s := newTestSigner(t)
	srv, _ := serveJWKS(t, s)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}

	// Build a token without a kid header by signing directly with jwt library.
	claims := &Claims{}
	claims.Subject = "u1"
	claims.Kind = "access"
	token := newJWTWithoutKID(t, s.priv, claims)

	_, err = rv.Verify(token)
	if err == nil {
		t.Fatal("expected error for token missing kid header, got nil")
	}
}

// newJWTToken creates an unsigned *jwt.Token with the EdDSA signing method and
// the supplied claims.  The caller may mutate the header before signing.
func newJWTToken(claims *Claims) *jwt.Token {
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
}

// newJWTWithoutKID signs a token with Ed25519 but deliberately omits the kid header.
func newJWTWithoutKID(t *testing.T, priv ed25519.PrivateKey, claims *Claims) string {
	t.Helper()
	tok := newJWTToken(claims)
	// Remove kid from header if set.
	delete(tok.Header, "kid")
	str, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token without kid: %v", err)
	}
	return str
}

// TestRemoteVerifier_FailedRefreshRateLimits: after a failed refresh attempt,
// a second rapid Verify with an unknown kid must not trigger another HTTP
// request (the attempt stamps lastRefresh, rate-limiting subsequent tries).
func TestRemoteVerifier_FailedRefreshRateLimits(t *testing.T) {
	s := newTestSigner(t)

	// Serve the initial JWKS so construction succeeds.
	var failFetch atomic.Bool
	var hits atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failFetch.Load() {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		jwks := BuildJWKS(s)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rv, err := NewRemoteVerifier(context.Background(), srv.URL+"/.well-known/jwks.json", "mindmesh-test")
	if err != nil {
		t.Fatalf("NewRemoteVerifier: %v", err)
	}
	// Construction hit = 1.

	// Switch to always-failing mode and wind back lastRefresh so the rate-limit
	// doesn't block the first refresh attempt.
	failFetch.Store(true)
	rv.mu.Lock()
	rv.lastRefresh = time.Now().Add(-2 * minRefreshInterval)
	rv.mu.Unlock()

	// Use a signer whose key is NOT in the cache.
	s2 := newTestSigner(t)
	tokenStr, _, err := s2.SignAccess("u2", "p2", "en", nil)
	if err != nil {
		t.Fatalf("SignAccess(s2): %v", err)
	}

	// First Verify: unknown kid → triggers refresh attempt (which fails).
	_, err = rv.Verify(tokenStr)
	if err == nil {
		t.Fatal("expected error on first Verify with unknown kid + failing endpoint")
	}

	// Second rapid Verify: lastRefresh was stamped on the first attempt, so the
	// rate-limit should block another HTTP call.
	_, err = rv.Verify(tokenStr)
	if err == nil {
		t.Fatal("expected error on second Verify with unknown kid")
	}

	// Hits: 1 (construction) + 1 (first refresh attempt) = 2.
	// The second Verify must NOT trigger another fetch.
	if n := hits.Load(); n != 2 {
		t.Errorf("JWKS endpoint hit count: got %d, want 2 (1 construction + 1 refresh attempt)", n)
	}
}
