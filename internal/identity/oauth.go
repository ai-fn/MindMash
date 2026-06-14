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

	cp, err := e.getOrCreateProvider(ctx, provider, cfg, redirectURI)
	if err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange init provider: %w", err)
	}

	token, err := cp.oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return OAuthUserInfo{}, fmt.Errorf("identity: oauth exchange: id_token missing from response")
	}

	verifier := cp.oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
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

func (e *OIDCExchanger) getOrCreateProvider(ctx context.Context, name string, cfg ProviderConfig, redirectURI string) (*cachedProvider, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if cp, ok := e.providers[name]; ok {
		// Update the redirect URI per-call (it may differ across environments).
		cp.oauth2Cfg.RedirectURL = redirectURI
		return cp, nil
	}

	oidcProvider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("identity: oidc discovery for %q: %w", name, err)
	}

	cp := &cachedProvider{
		oidcProvider: oidcProvider,
		oauth2Cfg: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     oidcProvider.Endpoint(),
			RedirectURL:  redirectURI,
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
	}
	e.providers[name] = cp
	return cp, nil
}
