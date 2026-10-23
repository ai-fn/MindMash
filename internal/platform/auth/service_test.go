package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	searchMethod = "/collection.v1.CollectionInternalService/GetSearchDocs"
	renderMethod = "/collection.v1.CollectionInternalService/RenderCards"
	testUser     = "0190a5b2-7c3d-7e4f-8a1b-2c3d4e5f6a7b"
)

func newServiceKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// signWith builds a service-shaped token with arbitrary claims, for negative cases.
func signWith(t *testing.T, priv ed25519.PrivateKey, mutate func(*Claims)) string {
	t.Helper()
	now := time.Now()
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "search", Subject: "search", Audience: jwt.ClaimStrings{CollectionInternalAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
		Kind: serviceKind,
	}
	mutate(&c)
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServiceVerifier(t *testing.T) {
	pub, priv := newServiceKey(t)
	_, otherPriv := newServiceKey(t)
	v := NewServiceVerifierFromKeys(CollectionInternalAudience, map[string]ed25519.PublicKey{"search": pub})

	good, err := NewServiceSignerFromKey("search", priv).Sign(CollectionInternalAudience)
	if err != nil {
		t.Fatal(err)
	}
	if caller, err := v.Verify(good); err != nil || caller != "search" {
		t.Fatalf("valid token: got %q, %v", caller, err)
	}

	user := newEphemeralSigner(t, defaultCfg())
	userToken, _, err := user.SignAccess(testUser, "p", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongAud, _ := NewServiceSignerFromKey("search", priv).Sign("somewhere-else")
	forged, _ := NewServiceSignerFromKey("search", otherPriv).Sign(CollectionInternalAudience)
	untrusted, _ := NewServiceSignerFromKey("media", priv).Sign(CollectionInternalAudience)

	for name, tok := range map[string]string{
		"user access token":        userToken,
		"wrong audience":           wrongAud,
		"signed by another key":    forged,
		"untrusted service name":   untrusted,
		"expired":                  signWith(t, priv, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }),
		"no expiry":                signWith(t, priv, func(c *Claims) { c.ExpiresAt = nil }),
		"lifetime over the cap":    signWith(t, priv, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }),
		"not a service kind":       signWith(t, priv, func(c *Claims) { c.Kind = "access" }),
		"issued in the future":     signWith(t, priv, func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }),
		"subject differs from iss": signWith(t, priv, func(c *Claims) { c.Subject = testUser }),
	} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: want rejection", name)
		}
	}

	// HS256 signed with the public key bytes, the classic algorithm-confusion attempt.
	hs, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "search", Subject: "search", Audience: jwt.ClaimStrings{CollectionInternalAudience},
		IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}, Kind: serviceKind}).SignedString([]byte(pub))
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "search", Subject: "search", Audience: jwt.ClaimStrings{CollectionInternalAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}, Kind: serviceKind}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	for name, tok := range map[string]string{"HS256 with the public key": hs, "alg none": none} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: want rejection", name)
		}
	}

	if _, err := user.Verify(good); err == nil {
		t.Error("a service token must not pass as a user access token")
	}
}

func TestNewServiceVerifierLoadsKeys(t *testing.T) {
	user := newEphemeralSigner(t, defaultCfg())
	dir := t.TempDir()
	path := filepath.Join(dir, "search.pub.pem")
	if err := os.WriteFile(path, mustPublicPEM(t, user.PublicKey()), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := NewServiceVerifier(CollectionInternalAudience, " search="+path+" ,")
	if err != nil || len(v.keys) != 1 {
		t.Fatalf("want one trusted key, got %v, %v", v, err)
	}
	if _, err := NewServiceVerifier(CollectionInternalAudience, "search"); err == nil {
		t.Fatal("want an error for an entry without a path")
	}
}

type userIDReq struct{ id string }

func (r userIDReq) GetUserId() string { return r.id }

func TestGRPCServiceOrUserAuthInterceptor(t *testing.T) {
	pub, priv := newServiceKey(t)
	services := NewServiceVerifierFromKeys(CollectionInternalAudience, map[string]ed25519.PublicKey{
		"search": pub, "scheduler": pub,
	})
	user := newEphemeralSigner(t, defaultCfg())
	ic := GRPCServiceOrUserAuthInterceptor(user, services, ServicePolicy{searchMethod: {"search"}})

	searchTok, _ := NewServiceSignerFromKey("search", priv).Sign(CollectionInternalAudience)
	schedulerTok, _ := NewServiceSignerFromKey("scheduler", priv).Sign(CollectionInternalAudience)
	userTok, _, _ := user.SignAccess(testUser, "p", "", nil)

	call := func(method, token string, req any) (IdentityContext, codes.Code) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
		var got IdentityContext
		_, err := ic(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
			got, _ = IdentityFromContext(ctx)
			return nil, nil
		})
		return got, status.Code(err)
	}

	tests := []struct {
		name   string
		method string
		token  string
		req    any
		want   codes.Code
	}{
		{"policy method with an allowed service", searchMethod, searchTok, userIDReq{testUser}, codes.OK},
		{"policy method rejects a user token", searchMethod, userTok, userIDReq{testUser}, codes.Unauthenticated},
		{"policy method rejects another trusted service", searchMethod, schedulerTok, userIDReq{testUser}, codes.PermissionDenied},
		{"policy method needs a user_id", searchMethod, searchTok, userIDReq{""}, codes.InvalidArgument},
		{"policy method needs a uuid user_id", searchMethod, searchTok, userIDReq{"not-a-uuid"}, codes.InvalidArgument},
		{"policy method needs a canonical user_id", searchMethod, searchTok, userIDReq{"{" + testUser + "}"}, codes.InvalidArgument},
		{"policy method rejects a dashless user_id", searchMethod, searchTok, userIDReq{strings.ReplaceAll(testUser, "-", "")}, codes.InvalidArgument},
		{"policy method needs a request with user_id", searchMethod, searchTok, struct{}{}, codes.InvalidArgument},
		{"other methods keep user auth", renderMethod, userTok, struct{}{}, codes.OK},
		{"other methods reject service tokens", renderMethod, searchTok, struct{}{}, codes.Unauthenticated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, code := call(tc.method, tc.token, tc.req)
			if code != tc.want {
				t.Fatalf("want %v, got %v", tc.want, code)
			}
			if code == codes.OK && id.UserID != testUser {
				t.Fatalf("want identity for %s, got %+v", testUser, id)
			}
			if code == codes.OK && tc.method == searchMethod && !slices.Equal(id.Roles, []string{"service:search"}) {
				t.Fatalf("want the service caller recorded in roles, got %+v", id.Roles)
			}
		})
	}

	none := GRPCServiceOrUserAuthInterceptor(user, nil, ServicePolicy{searchMethod: {"search"}})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+searchTok))
	if _, err := none(ctx, userIDReq{testUser}, &grpc.UnaryServerInfo{FullMethod: searchMethod}, nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no trusted services: want Unauthenticated, got %v", err)
	}
}

func mustPublicPEM(t *testing.T, pub ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
