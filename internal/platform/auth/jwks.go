package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/mindmesh/mindmesh/internal/platform/errors"
)

const minRefreshInterval = 30 * time.Second

// RemoteVerifier fetches a JWKS from a URL, caches the parsed Ed25519 public
// keys in memory, and implements the Verifier interface.  It is safe for
// concurrent use.
type RemoteVerifier struct {
	jwksURL string
	issuer  string
	client  *http.Client

	fetchMu sync.Mutex // serializes concurrent HTTP fetches; always acquired before mu, never while mu is held

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey // kid → public key
	lastRefresh time.Time
}

// NewRemoteVerifier creates a RemoteVerifier and performs the initial JWKS
// fetch.  Returns an error if jwksURL is empty or the initial fetch fails.
// When issuer is non-empty, Verify enforces the "iss" claim; pass "" to skip
// issuer validation.
func NewRemoteVerifier(ctx context.Context, jwksURL, issuer string) (*RemoteVerifier, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: JWKS_URL is not set")
	}
	rv := &RemoteVerifier{
		jwksURL: jwksURL,
		issuer:  issuer,
		client:  &http.Client{Timeout: 10 * time.Second},
		keys:    make(map[string]ed25519.PublicKey),
	}
	if err := rv.refresh(ctx); err != nil {
		return nil, fmt.Errorf("auth: initial JWKS fetch: %w", err)
	}
	return rv, nil
}

// Verify implements Verifier.  It parses and validates the JWT, refreshing
// the cached key-set at most once per minRefreshInterval when an unknown kid
// is encountered.
func (rv *RemoteVerifier) Verify(tokenString string) (*Claims, error) {
	pub, err := rv.keyForToken(tokenString)
	if err != nil {
		return nil, errors.Unauthenticated("invalid token", err)
	}

	parseOpts := []jwt.ParserOption{jwt.WithIssuedAt()}
	if rv.issuer != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(rv.issuer))
	}

	claims := &Claims{}
	_, err = jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return pub, nil
	}, parseOpts...)
	if err != nil {
		return nil, errors.Unauthenticated("invalid token", err)
	}
	if claims.Kind != "access" {
		return nil, errors.Unauthenticated("invalid token kind", nil)
	}
	return claims, nil
}

// keyForToken extracts the kid from the token header, looks it up in the
// cache, and refreshes once if it is unknown (subject to rate-limit).
func (rv *RemoteVerifier) keyForToken(tokenString string) (ed25519.PublicKey, error) {
	kid, err := extractKID(tokenString)
	if err != nil {
		return nil, err
	}

	// Fast path: cache hit.
	rv.mu.RLock()
	pub, ok := rv.keys[kid]
	rv.mu.RUnlock()
	if ok {
		return pub, nil
	}

	// Unknown kid — acquire fetchMu to serialize concurrent refresh attempts.
	// While we waited for fetchMu, another goroutine may have already fetched
	// and loaded the key, so we re-check the cache before doing any I/O.
	rv.fetchMu.Lock()
	defer rv.fetchMu.Unlock()

	// Double-check: another goroutine may have refreshed while we waited.
	rv.mu.RLock()
	pub, ok = rv.keys[kid]
	rv.mu.RUnlock()
	if ok {
		return pub, nil
	}

	// Rate-limit check and stamp under write-lock (held for microseconds).
	rv.mu.Lock()
	if time.Since(rv.lastRefresh) < minRefreshInterval {
		rv.mu.Unlock()
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}
	// Stamp lastRefresh at the start of the attempt so that failed refreshes
	// are also rate-limited (Issue #2).
	rv.lastRefresh = time.Now()
	rv.mu.Unlock()

	// HTTP fetch — no lock held; fetchMu already prevents concurrent fetches.
	newKeys, fetchErr := rv.fetchKeysRaw(context.Background())
	if fetchErr != nil {
		return nil, fmt.Errorf("auth: unknown kid %q (refresh failed: %w)", kid, fetchErr)
	}

	// Swap the key map under write-lock (held for microseconds).
	rv.mu.Lock()
	rv.keys = newKeys
	rv.mu.Unlock()

	pub, ok = newKeys[kid]
	if !ok {
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}
	return pub, nil
}

// refresh fetches the JWKS endpoint and updates the in-memory key cache.
// It is used only for the initial fetch at construction time.
// Callers must NOT hold rv.mu or rv.fetchMu.
func (rv *RemoteVerifier) refresh(ctx context.Context) error {
	rv.fetchMu.Lock()
	defer rv.fetchMu.Unlock()

	newKeys, err := rv.fetchKeysRaw(ctx)
	if err != nil {
		return err
	}
	rv.mu.Lock()
	rv.keys = newKeys
	rv.lastRefresh = time.Now()
	rv.mu.Unlock()
	return nil
}

// fetchKeysRaw performs the HTTP fetch of the JWKS and parses the result into
// a key map.  The caller is responsible for holding rv.fetchMu to serialize
// concurrent HTTP callers.  No read/write lock on rv.mu is held during the
// fetch.
func (rv *RemoteVerifier) fetchKeysRaw(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rv.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: build JWKS request: %w", err)
	}

	resp, err := rv.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: JWKS endpoint returned status %d", resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("auth: decode JWKS: %w", err)
	}

	newKeys := make(map[string]ed25519.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			// Skip individual bad keys rather than aborting the entire key-set.
			continue
		}
		if len(raw) != ed25519.PublicKeySize {
			// Skip keys with wrong length rather than aborting the entire key-set.
			continue
		}
		pub := ed25519.PublicKey(raw)
		// Use the kid served in the JWKS, falling back to ComputeKID if absent.
		kid := k.Kid
		if kid == "" {
			kid = ComputeKID(pub)
		}
		newKeys[kid] = pub
	}

	if len(newKeys) == 0 {
		return nil, fmt.Errorf("auth: JWKS contains no usable Ed25519 keys")
	}
	return newKeys, nil
}

// extractKID parses only the JWT header (no signature verification) to read
// the "kid" field.
func extractKID(tokenString string) (string, error) {
	p := jwt.NewParser()
	// ParseUnverified does not validate the signature or claims.
	token, _, err := p.ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return "", fmt.Errorf("auth: parse token header: %w", err)
	}
	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return "", fmt.Errorf("auth: token missing kid header")
	}
	return kid, nil
}
