package auth

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// CollectionInternalAudience scopes service tokens to Collection's internal API.
const CollectionInternalAudience = "collection-internal"

const (
	serviceKind        = "service"
	serviceTokenTTL    = time.Minute
	maxServiceTokenTTL = 5 * time.Minute
)

// ServiceSigner mints service-to-service tokens with the caller's own key, so no peer holds Identity's user-token key.
type ServiceSigner struct {
	name string
	priv ed25519.PrivateKey
	kid  string
}

// NewServiceSigner loads the named service's Ed25519 private key.
func NewServiceSigner(name, privateKeyPath string) (*ServiceSigner, error) {
	data, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: read %s service key: %w", name, err)
	}
	priv, err := parsePrivateKeyPEM(data)
	if err != nil {
		return nil, err
	}
	return NewServiceSignerFromKey(name, priv), nil
}

func NewServiceSignerFromKey(name string, priv ed25519.PrivateKey) *ServiceSigner {
	return &ServiceSigner{name: name, priv: priv, kid: ComputeKID(priv.Public().(ed25519.PublicKey))}
}

// Sign returns a short-lived token for audience.
func (s *ServiceSigner) Sign(audience string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.name,
			Subject:   s.name,
			Audience:  jwt.ClaimStrings{audience},
			ID:        ids.NewULID(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(serviceTokenTTL)),
		},
		Kind: serviceKind,
	})
	token.Header["kid"] = s.kid
	signed, err := token.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("auth: sign %s service token: %w", s.name, err)
	}
	return signed, nil
}

// ServiceVerifier accepts service tokens for one audience from trusted callers only.
type ServiceVerifier struct {
	audience string
	keys     map[string]ed25519.PublicKey // by service name
}

// NewServiceVerifier trusts the public keys listed as "name=path,name=path".
func NewServiceVerifier(audience, trusted string) (*ServiceVerifier, error) {
	keys := make(map[string]ed25519.PublicKey)
	for entry := range strings.SplitSeq(trusted, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, path, ok := strings.Cut(entry, "=")
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("auth: trusted service %q: want name=path", entry)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("auth: read %s service public key: %w", name, err)
		}
		pub, err := parsePublicKeyPEM(data)
		if err != nil {
			return nil, err
		}
		keys[name] = pub
	}
	return NewServiceVerifierFromKeys(audience, keys), nil
}

func NewServiceVerifierFromKeys(audience string, keys map[string]ed25519.PublicKey) *ServiceVerifier {
	return &ServiceVerifier{audience: audience, keys: keys}
}

// Verify returns the calling service's name.
func (v *ServiceVerifier) Verify(tokenString string) (string, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		// The issuer is unverified here, but only that service's key can then validate the signature.
		iss, _ := t.Claims.GetIssuer()
		key, ok := v.keys[iss]
		if !ok {
			return nil, fmt.Errorf("auth: untrusted service %q", iss)
		}
		return key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(clockLeeway),
	)
	if err != nil {
		return "", errors.Unauthenticated("invalid service token", err)
	}
	if claims.Kind != serviceKind || claims.Subject != claims.Issuer || claims.IssuedAt == nil ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > maxServiceTokenTTL {
		return "", errors.Unauthenticated("invalid service token")
	}
	return claims.Issuer, nil
}

// ServicePolicy maps a full gRPC method name to the services allowed to call it.
type ServicePolicy map[string][]string

// GRPCServiceOrUserAuthInterceptor admits policy methods only with a service token from an allowed caller,
// acting for the request's user_id; every other method requires a user access token.
func GRPCServiceOrUserAuthInterceptor(users Verifier, services *ServiceVerifier, policy ServicePolicy) grpc.UnaryServerInterceptor {
	userAuth := GRPCAuthInterceptor(users)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		allowed, ok := policy[info.FullMethod]
		if !ok {
			return userAuth(ctx, req, info, handler)
		}
		token, ok := incomingBearer(ctx)
		if !ok || services == nil {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
		}
		caller, err := services.Verify(token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		if !slices.Contains(allowed, caller) {
			return nil, status.Error(codes.PermissionDenied, "service not allowed to call this method")
		}
		r, ok := req.(interface{ GetUserId() string })
		if !ok || !IsCanonicalUUID(r.GetUserId()) {
			return nil, status.Error(codes.InvalidArgument, "user_id must be a canonical UUID")
		}
		ctx = WithIdentity(ctx, IdentityContext{UserID: r.GetUserId(), Roles: []string{"service:" + caller}})
		return handler(ctx, req)
	}
}

// IsCanonicalUUID rejects the braced, urn: and dashless spellings uuid.Parse also accepts.
func IsCanonicalUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}

func incomingBearer(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", false
	}
	return parseBearerToken(vals[0])
}
