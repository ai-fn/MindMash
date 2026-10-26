package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

const (
	minRefreshInterval  = 30 * time.Second // floor between unknown-kid refreshes
	jwksRefreshInterval = 5 * time.Minute  // background refresh, so rotated or revoked keys propagate
	maxJWKSBackoff      = 30 * time.Second
	maxJWKSBytes        = 1 << 20
)

var jwksRefreshFailures = promauto.NewCounter(prometheus.CounterOpts{
	Name: "mindmesh_jwks_refresh_failures_total",
	Help: "Failed JWKS fetches by remote verifiers.",
})

// RemoteVerifier keeps its cached JWKS keys through an Identity outage, so revoking a key needs
// Identity reachable.
type RemoteVerifier struct {
	jwksURL string
	issuer  string
	client  *http.Client
	life    context.Context // process lifetime: on-demand fetches have no request ctx but must stop on shutdown

	fetchMu sync.Mutex // serializes concurrent HTTP fetches; always acquired before mu, never while mu is held

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey // kid → public key
	lastRefresh time.Time
}

// NewRemoteVerifier survives a failed first fetch (Identity may be starting) by rejecting tokens
// and retrying with backoff. A non-empty issuer is enforced on "iss".
func NewRemoteVerifier(ctx context.Context, jwksURL, issuer string) (*RemoteVerifier, error) {
	return newRemoteVerifier(ctx, jwksURL, issuer, jwksRefreshInterval)
}

func newRemoteVerifier(ctx context.Context, jwksURL, issuer string, interval time.Duration) (*RemoteVerifier, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: JWKS_URL is not set")
	}
	rv := &RemoteVerifier{
		jwksURL: jwksURL,
		issuer:  issuer,
		client:  &http.Client{Timeout: 10 * time.Second},
		life:    ctx,
		keys:    make(map[string]ed25519.PublicKey),
	}
	failed := rv.refresh(ctx) != nil
	if failed {
		jwksRefreshFailures.Inc()
	}
	go rv.refreshLoop(ctx, interval, failed)
	return rv, nil
}

func (rv *RemoteVerifier) refreshLoop(ctx context.Context, interval time.Duration, failing bool) {
	backoff := min(time.Second, interval)
	for {
		wait := interval
		if failing {
			wait = backoff
			backoff = min(backoff*2, maxJWKSBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(wait)):
		}
		failing = rv.refresh(ctx) != nil
		if failing {
			jwksRefreshFailures.Inc()
		} else {
			backoff = min(time.Second, interval)
		}
	}
}

// jitter spreads d by ±10% so replicas and restarted services don't hit Identity in lockstep.
func jitter(d time.Duration) time.Duration {
	return d - d/10 + rand.N(d/5+1)
}

// Verify implements Verifier. An unknown kid triggers at most one refresh per minRefreshInterval.
func (rv *RemoteVerifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("auth: token missing kid header")
		}
		return rv.keyFor(kid)
	}, userTokenOptions(rv.issuer)...)
	if err != nil {
		return nil, apperr.Unauthenticated("invalid token", err)
	}
	if claims.Kind != "access" {
		return nil, apperr.Unauthenticated("invalid token kind", nil)
	}
	return claims, nil
}

// keyFor returns the cached key for kid, refreshing once if it is unknown (rate-limited).
func (rv *RemoteVerifier) keyFor(kid string) (ed25519.PublicKey, error) {
	rv.mu.RLock()
	pub, ok := rv.keys[kid]
	limited := time.Since(rv.lastRefresh) < minRefreshInterval
	rv.mu.RUnlock()
	if ok {
		return pub, nil
	}
	// Fail fast outside the lock: queueing behind a slow fetch would stall every unknown-kid request.
	if limited {
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}

	rv.fetchMu.Lock()
	defer rv.fetchMu.Unlock()

	// Re-check under the lock: another goroutine may have refreshed while we waited.
	rv.mu.Lock()
	if pub, ok = rv.keys[kid]; ok {
		rv.mu.Unlock()
		return pub, nil
	}
	if time.Since(rv.lastRefresh) < minRefreshInterval {
		rv.mu.Unlock()
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}
	rv.lastRefresh = time.Now() // stamped before fetching so failures are rate-limited too
	rv.mu.Unlock()

	newKeys, err := rv.fetchKeysRaw(rv.life)
	if err != nil {
		jwksRefreshFailures.Inc()
		return nil, fmt.Errorf("auth: unknown kid %q (refresh failed: %w)", kid, err)
	}
	rv.mu.Lock()
	rv.keys = newKeys
	rv.mu.Unlock()

	pub, ok = newKeys[kid]
	if !ok {
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}
	return pub, nil
}

// refresh fetches the JWKS and replaces the cache; on failure the previous keys stay.
func (rv *RemoteVerifier) refresh(ctx context.Context) error {
	rv.fetchMu.Lock()
	defer rv.fetchMu.Unlock()

	// Stamped first, so unknown-kid requests arriving during a slow fetch fail fast instead of queueing.
	rv.mu.Lock()
	rv.lastRefresh = time.Now()
	rv.mu.Unlock()

	newKeys, err := rv.fetchKeysRaw(ctx)
	if err != nil {
		return err
	}
	rv.mu.Lock()
	rv.keys = newKeys
	rv.mu.Unlock()
	return nil
}

// fetchKeysRaw needs rv.fetchMu held by the caller; it takes no rv.mu lock during the HTTP call.
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("auth: decode JWKS: %w", err)
	}

	newKeys := make(map[string]ed25519.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		// A malformed Ed25519 entry fails the fetch: swapping in a set without it would drop its valid kid.
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("auth: malformed Ed25519 key %q in JWKS", k.Kid)
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

// Ready fails until the first JWKS fetch succeeds; later failures keep the cached keys, so it stays ready.
func (rv *RemoteVerifier) Ready() error {
	rv.mu.RLock()
	defer rv.mu.RUnlock()
	if len(rv.keys) == 0 {
		return fmt.Errorf("auth: JWKS not loaded from %s", rv.jwksURL)
	}
	return nil
}

// Ready reports whether v can verify tokens yet; verifiers without remote keys always can.
func Ready(v Verifier) error {
	if r, ok := v.(interface{ Ready() error }); ok {
		return r.Ready()
	}
	return nil
}
