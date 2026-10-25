package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

// ProviderConfig holds the OIDC client credentials for a single OAuth provider.
type ProviderConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
}

// cachedProvider pairs the oidc.Provider with its oauth2.Config so they are
// initialised once per provider name and reused across requests.
type cachedProvider struct {
	oidcProvider *gooidc.Provider
	oauth2Cfg    oauth2.Config
}

// OIDCExchanger implements OAuthExchanger using OIDC discovery and standard
// authorization-code exchange.  Provider metadata is lazily cached after the
// first exchange so the service can start without a live issuer.
type OIDCExchanger struct {
	configs map[string]ProviderConfig

	mu        sync.Mutex
	providers map[string]*cachedProvider
}

// NewOIDCExchanger returns an exchanger configured for the given providers.
func NewOIDCExchanger(configs map[string]ProviderConfig) *OIDCExchanger {
	return &OIDCExchanger{
		configs:   configs,
		providers: make(map[string]*cachedProvider),
	}
}

// AuthURL returns the provider's authorization URL carrying state, the nonce and a PKCE challenge.
func (e *OIDCExchanger) AuthURL(ctx context.Context, provider string, flow OAuthFlow, state string) (string, error) {
	cfg, ok := e.configs[provider]
	if !ok {
		return "", apperr.InvalidArgument(fmt.Sprintf("unknown OAuth provider %q", provider))
	}
	_, callCfg, err := e.getOrCreateProvider(ctx, provider, cfg, flow.RedirectURI)
	if err != nil {
		return "", apperr.Unavailable("oauth provider unavailable", err)
	}
	return callCfg.AuthCodeURL(state, oauth2.S256ChallengeOption(flow.Verifier), oauth2.SetAuthURLParam("nonce", flow.Nonce)), nil
}

// Exchange performs the authorization-code exchange (with PKCE) and returns the
// user info from the verified ID token, whose nonce must match the flow's.
func (e *OIDCExchanger) Exchange(ctx context.Context, provider, code string, flow OAuthFlow) (OAuthUserInfo, error) {
	cfg, ok := e.configs[provider]
	if !ok {
		return OAuthUserInfo{}, apperr.InvalidArgument(fmt.Sprintf("unknown OAuth provider %q", provider))
	}

	oidcProvider, callCfg, err := e.getOrCreateProvider(ctx, provider, cfg, flow.RedirectURI)
	if err != nil {
		return OAuthUserInfo{}, apperr.Unavailable("oauth provider unavailable", err)
	}

	token, err := callCfg.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		var rejected *oauth2.RetrieveError
		if errors.As(err, &rejected) {
			return OAuthUserInfo{}, apperr.Unauthenticated("oauth code exchange failed", err)
		}
		return OAuthUserInfo{}, apperr.Unavailable("oauth provider unavailable", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return OAuthUserInfo{}, apperr.Unauthenticated("oauth response has no id_token")
	}

	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return OAuthUserInfo{}, apperr.Unauthenticated("invalid id_token", err)
	}

	if flow.Nonce == "" || idToken.Nonce != flow.Nonce {
		return OAuthUserInfo{}, apperr.Unauthenticated("oauth id_token nonce mismatch")
	}

	var claims struct {
		Sub           string    `json:"sub"`
		Email         string    `json:"email"`
		EmailVerified claimBool `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return OAuthUserInfo{}, apperr.Unauthenticated("invalid id_token claims", err)
	}

	return OAuthUserInfo{
		Provider:      provider,
		Subject:       claims.Sub,
		Email:         claims.Email,
		EmailVerified: bool(claims.EmailVerified),
	}, nil
}

// claimBool accepts Apple's string "true"/"false" as well as a JSON bool.
type claimBool bool

func (b *claimBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case `true`, `"true"`:
		*b = true
	case `false`, `"false"`, `null`:
		*b = false
	default:
		return fmt.Errorf("identity: email_verified: unexpected value %s", data)
	}
	return nil
}

// getOrCreateProvider returns the cached oidc.Provider and a per-call copy of
// the oauth2.Config with RedirectURL set.  The config is copied by value so
// that setting RedirectURL on the copy does not race with concurrent Exchange
// calls that hold a pointer into the same cached struct.
func (e *OIDCExchanger) getOrCreateProvider(ctx context.Context, name string, cfg ProviderConfig, redirectURI string) (*gooidc.Provider, oauth2.Config, error) {
	e.mu.Lock()
	cp, ok := e.providers[name]
	e.mu.Unlock()

	if !ok {
		oidcProvider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
		if err != nil {
			return nil, oauth2.Config{}, fmt.Errorf("identity: oidc discovery for %q: %w", name, err)
		}
		newCP := &cachedProvider{
			oidcProvider: oidcProvider,
			oauth2Cfg: oauth2.Config{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				Endpoint:     oidcProvider.Endpoint(),
				Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
			},
		}
		e.mu.Lock()
		// Re-check under the lock in case another goroutine raced us here.
		if existing, ok := e.providers[name]; ok {
			cp = existing
		} else {
			e.providers[name] = newCP
			cp = newCP
		}
		e.mu.Unlock()
	}

	// Copy the oauth2.Config by value and set RedirectURL on the copy; the
	// cached struct is never mutated after being stored, so there is no race.
	callCfg := cp.oauth2Cfg
	callCfg.RedirectURL = redirectURI
	return cp.oidcProvider, callCfg, nil
}

// ValidateRedirectURIs requires absolute https URLs (http only for loopback) without fragments or wildcards.
func ValidateRedirectURIs(uris []string) error {
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Fragment != "" || strings.Contains(raw, "*") {
			return fmt.Errorf("identity: invalid oauth redirect uri %q", raw)
		}
		loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
		if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
			return fmt.Errorf("identity: oauth redirect uri %q must be https", raw)
		}
	}
	return nil
}
