package identity

import (
	"context"
	"fmt"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
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

// Exchange performs a full OIDC authorization-code exchange and returns the
// normalised user info from the verified ID token.
func (e *OIDCExchanger) Exchange(ctx context.Context, provider, code, redirectURI string) (OAuthUserInfo, error) {
	cfg, ok := e.configs[provider]
	if !ok {
		return OAuthUserInfo{}, platformerrors.InvalidArgument(fmt.Sprintf("unknown OAuth provider %q", provider))
	}

	oidcProvider, callCfg, err := e.getOrCreateProvider(ctx, provider, cfg, redirectURI)
	if err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange init provider: %w", err)
	}

	token, err := callCfg.Exchange(ctx, code)
	if err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange: id_token missing from response")
	}

	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange verify id_token: %w", err)
	}

	var claims struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange extract claims: %w", err)
	}

	return OAuthUserInfo{
		Provider:      provider,
		Subject:       claims.Sub,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}, nil
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
