package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// fakeIdP is a minimal OIDC provider whose token endpoint enforces PKCE and issues ID tokens with a chosen nonce.
type fakeIdP struct {
	*httptest.Server
	key *rsa.PrivateKey
	mu  sync.Mutex
	// challenge is the PKCE challenge the token endpoint must see proven.
	challenge  string
	issueNonce string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.URL,
			"authorization_endpoint":                idp.URL + "/auth",
			"token_endpoint":                        idp.URL + "/token",
			"jwks_uri":                              idp.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		jwk := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.mu.Lock()
		challenge, nonce := idp.challenge, idp.issueNonce
		idp.mu.Unlock()
		if oauth2.S256ChallengeFromVerifier(r.PostForm.Get("code_verifier")) != challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now()
		raw, err := jwt.Signed(signer).Claims(map[string]any{
			"iss": idp.URL, "aud": "client-id", "sub": "idp-user-1", "nonce": nonce,
			"email": "pat@example.com", "email_verified": "true",
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		}).Serialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

func (i *fakeIdP) set(challenge, nonce string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.challenge, i.issueNonce = challenge, nonce
}

func (i *fakeIdP) setNonce(nonce string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.issueNonce = nonce
}

func TestOIDCExchanger_PKCEAndNonce(t *testing.T) {
	idp := newFakeIdP(t)
	ex := NewOIDCExchanger(map[string]ProviderConfig{"idp": {IssuerURL: idp.URL, ClientID: "client-id", ClientSecret: "secret"}})
	ctx := context.Background()
	flow := OAuthFlow{RedirectURI: "https://app/cb", Nonce: "nonce-1", Verifier: oauth2.GenerateVerifier()}

	authURL, err := ex.AuthURL(ctx, "idp", flow, "state-1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	idp.set(oauth2.S256ChallengeFromVerifier(flow.Verifier), "")
	for k, want := range map[string]string{
		"state": "state-1", "nonce": "nonce-1", "client_id": "client-id", "redirect_uri": "https://app/cb",
		"code_challenge": oauth2.S256ChallengeFromVerifier(flow.Verifier), "code_challenge_method": "S256", "response_type": "code",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("auth URL %s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope %q must include openid", q.Get("scope"))
	}

	t.Run("matching nonce and verifier", func(t *testing.T) {
		idp.setNonce("nonce-1")
		info, err := ex.Exchange(ctx, "idp", "code", flow)
		if err != nil {
			t.Fatal(err)
		}
		if info.Subject != "idp-user-1" || info.Email != "pat@example.com" || !info.EmailVerified {
			t.Fatalf("unexpected info %+v", info)
		}
	})
	t.Run("ID token for another flow's nonce is rejected", func(t *testing.T) {
		idp.setNonce("someone-elses")
		if _, err := ex.Exchange(ctx, "idp", "code", flow); !platformerrors.IsUnauthenticated(err) {
			t.Fatalf("nonce mismatch: got %v, want Unauthenticated", err)
		}
	})
	t.Run("missing nonce in the ID token is rejected", func(t *testing.T) {
		idp.setNonce("")
		if _, err := ex.Exchange(ctx, "idp", "code", flow); err == nil {
			t.Fatal("an ID token without the flow's nonce must fail")
		}
	})
	t.Run("wrong PKCE verifier is rejected by the provider", func(t *testing.T) {
		idp.setNonce("nonce-1")
		stolen := flow
		stolen.Verifier = oauth2.GenerateVerifier()
		_, err := ex.Exchange(ctx, "idp", "code", stolen)
		if !platformerrors.IsUnauthenticated(err) {
			t.Fatalf("wrong verifier: got %v, want Unauthenticated (not a 5xx)", err)
		}
		var ae *platformerrors.AppError
		if errors.As(err, &ae) && strings.Contains(ae.Message, "invalid_grant") {
			t.Fatalf("provider response leaked into the client-visible message: %q", ae.Message)
		}
	})
	t.Run("unknown provider", func(t *testing.T) {
		if _, err := ex.AuthURL(ctx, "nope", flow, "s"); !platformerrors.IsInvalidArgument(err) {
			t.Fatalf("got %v, want InvalidArgument", err)
		}
	})
	t.Run("an empty flow nonce never matches", func(t *testing.T) {
		idp.setNonce("")
		empty := flow
		empty.Nonce = ""
		if _, err := ex.Exchange(ctx, "idp", "code", empty); err == nil {
			t.Fatal("fail closed when the flow carries no nonce")
		}
	})
}
