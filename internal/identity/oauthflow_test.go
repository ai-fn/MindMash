package identity_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"

	. "github.com/mindmesh/mindmesh/internal/identity"
)

func TestOAuthStart(t *testing.T) {
	svc, _, _, exchanger, _ := newTestService(t)
	ctx := context.Background()

	authURL, state, err := svc.OAuthStart(ctx, "google", testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(authURL); u.Query().Get("state") != state || state == "" {
		t.Fatalf("auth URL %q must carry the returned state %q", authURL, state)
	}
	flow := exchanger.flows[state]
	if flow.Nonce == "" || flow.Verifier == "" || flow.RedirectURI != testRedirect {
		t.Fatalf("server must mint nonce and PKCE verifier: %+v", flow)
	}

	_, state2, err := svc.OAuthStart(ctx, "google", testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	if state2 == state || exchanger.flows[state2].Nonce == flow.Nonce || exchanger.flows[state2].Verifier == flow.Verifier {
		t.Fatal("every flow needs fresh secrets")
	}
}

func TestOAuthStart_RedirectAllowlist(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	for _, uri := range []string{"", "https://evil.example/cb", testRedirect + "/", "https://app/cb?x=1"} {
		if _, _, err := svc.OAuthStart(context.Background(), "google", uri); grpcCode(err) != codes.InvalidArgument {
			t.Errorf("redirect %q: got %v, want InvalidArgument", uri, grpcCode(err))
		}
	}
}

func TestOAuth_NotConfigured(t *testing.T) {
	svc, _, _, _, _ := newTestService(t, WithOAuth(nil, nil))
	if _, _, err := svc.OAuthStart(context.Background(), "google", testRedirect); grpcCode(err) != codes.FailedPrecondition {
		t.Fatalf("start: got %v, want FailedPrecondition", grpcCode(err))
	}
	if _, _, _, _, _, _, err := svc.OAuthExchange(context.Background(), "google", "c", "s", ""); grpcCode(err) != codes.FailedPrecondition {
		t.Fatalf("exchange: got %v, want FailedPrecondition", grpcCode(err))
	}
}

func TestOAuthExchange_StateRules(t *testing.T) {
	setup := func(t *testing.T) (*Service, *fakeOAuthExchanger, string) {
		t.Helper()
		svc, _, _, exchanger, _ := newTestService(t)
		exchanger.responses["c"] = OAuthUserInfo{Provider: "google", Subject: "s", Email: "state@example.com", EmailVerified: true}
		_, state, err := svc.OAuthStart(context.Background(), "google", testRedirect)
		if err != nil {
			t.Fatal(err)
		}
		return svc, exchanger, state
	}
	ctx := context.Background()

	t.Run("exchange uses the server's flow, not the client's", func(t *testing.T) {
		svc, exchanger, state := setup(t)
		if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", state, ""); err != nil {
			t.Fatal(err)
		}
		if exchanger.lastFlow != exchanger.flows[state] {
			t.Fatalf("exchange flow %+v differs from the started flow %+v", exchanger.lastFlow, exchanger.flows[state])
		}
	})
	t.Run("a state works once", func(t *testing.T) {
		svc, _, state := setup(t)
		if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", state, testRedirect); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", state, testRedirect); grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("replay: got %v, want Unauthenticated", grpcCode(err))
		}
	})
	t.Run("unknown state", func(t *testing.T) {
		svc, _, _ := setup(t)
		if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", "forged", ""); grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("got %v, want Unauthenticated", grpcCode(err))
		}
	})
	t.Run("another provider or redirect burns the state", func(t *testing.T) {
		for name, args := range map[string][2]string{"provider": {"apple", ""}, "redirect": {"google", "https://app/other"}} {
			svc, _, state := setup(t)
			if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, args[0], "c", state, args[1]); grpcCode(err) != codes.Unauthenticated {
				t.Errorf("%s mismatch: got %v, want Unauthenticated", name, grpcCode(err))
			}
			if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", state, ""); err == nil {
				t.Errorf("%s mismatch must consume the state", name)
			}
		}
	})
	t.Run("missing code or state", func(t *testing.T) {
		svc, _, state := setup(t)
		for _, tc := range [][2]string{{"", state}, {"c", ""}} {
			if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", tc[0], tc[1], ""); grpcCode(err) != codes.InvalidArgument {
				t.Errorf("code=%q state=%q: got %v, want InvalidArgument", tc[0], tc[1], grpcCode(err))
			}
		}
	})
}

func TestOAuthExchange_ProviderFailuresAreClientErrors(t *testing.T) {
	svc, _, _, exchanger, _ := newTestService(t)
	ctx := context.Background()
	_, state, err := svc.OAuthStart(ctx, "google", testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	exchanger.err = apperr.Unauthenticated("oauth code exchange failed", errors.New("oauth2: cannot fetch token: 400\nResponse: {secret}"))

	_, _, _, _, _, _, err = svc.OAuthExchange(ctx, "google", "c", state, "")
	if grpcCode(err) != codes.Unauthenticated {
		t.Fatalf("got %v, want Unauthenticated", grpcCode(err))
	}
	if st, _ := status.FromError(err); strings.Contains(st.Message(), "secret") {
		t.Fatalf("provider detail leaked to the client: %q", st.Message())
	}
}

func TestValidateRedirectURIs(t *testing.T) {
	for _, ok := range []string{"https://app.example.com/cb", "http://localhost:5173/cb", "http://127.0.0.1:3000/cb"} {
		if err := ValidateRedirectURIs([]string{ok}); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/cb", "http://app.example.com/cb", "https://*.example.com/cb", "https://app.example.com/cb#frag", "javascript:alert(1)"} {
		if err := ValidateRedirectURIs([]string{bad}); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
