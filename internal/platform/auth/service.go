package auth

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
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
	keys     map[string][]ed25519.PublicKey // by service name
}

// NewServiceVerifier trusts the public keys listed as "name=path,name=path";
// "name=old|new" trusts both while a caller's key rotates.
func NewServiceVerifier(audience, trusted string) (*ServiceVerifier, error) {
	keys := make(map[string][]ed25519.PublicKey)
	for entry := range strings.SplitSeq(trusted, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, paths, ok := strings.Cut(entry, "=")
		if !ok || name == "" || paths == "" {
			return nil, fmt.Errorf("auth: trusted service %q: want name=path", entry)
		}
		if _, dup := keys[name]; dup {
			return nil, fmt.Errorf("auth: trusted service %q listed twice; use name=old|new to rotate", name)
		}
		for path := range strings.SplitSeq(paths, "|") {
			if path == "" {
				return nil, fmt.Errorf("auth: trusted service %q: empty key path", entry)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("auth: read %s service public key: %w", name, err)
			}
			pub, err := parsePublicKeyPEM(data)
			if err != nil {
				return nil, err
			}
			keys[name] = append(keys[name], pub)
		}
	}
	return NewServiceVerifierFromKeys(audience, keys), nil
}

func NewServiceVerifierFromKeys(audience string, keys map[string][]ed25519.PublicKey) *ServiceVerifier {
	return &ServiceVerifier{audience: audience, keys: keys}
}

// Verify returns the calling service's name.
func (v *ServiceVerifier) Verify(tokenString string) (string, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		// The issuer is unverified here, but only that service's key can then validate the signature.
		iss, _ := t.Claims.GetIssuer()
		keys, ok := v.keys[iss]
		if !ok {
			return nil, fmt.Errorf("auth: untrusted service %q", iss)
		}
		set := jwt.VerificationKeySet{Keys: make([]jwt.VerificationKey, len(keys))}
		for i, k := range keys {
			set.Keys[i] = k
		}
		return set, nil
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

var serviceAuthRejections = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "mindmesh_service_auth_rejections_total",
	Help: "Rejected calls to service-token methods by method, claimed caller and reason.",
}, []string{"method", "caller", "reason"})

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
		reject := func(caller, reason string, err, cause error) error {
			serviceAuthRejections.WithLabelValues(info.FullMethod, caller, reason).Inc()
			if suppressed, ok := rejectionLogs.allow(info.FullMethod + "|" + caller + "|" + reason); ok {
				log.L().Warn("auth: service call rejected", zap.String("method", info.FullMethod),
					zap.String("caller", caller), zap.String("reason", reason), zap.Error(err),
					zap.NamedError("cause", cause), zap.Int("suppressed", suppressed))
			}
			return err
		}
		if services == nil {
			return nil, reject(unknownCaller, "no_service_verifier", status.Error(codes.Unauthenticated, "missing or invalid token"), nil)
		}
		token, ok := incomingBearer(ctx)
		if !ok {
			return nil, reject(unknownCaller, "missing_token", status.Error(codes.Unauthenticated, "missing or invalid token"), nil)
		}
		caller, err := services.Verify(token)
		if err != nil {
			return nil, reject(services.claimedCaller(token), "invalid_token", status.Error(codes.Unauthenticated, "invalid token"), err)
		}
		if !slices.Contains(allowed, caller) {
			return nil, reject(caller, "forbidden", status.Error(codes.PermissionDenied, "service not allowed to call this method"), nil)
		}
		r, ok := req.(interface{ GetUserId() string })
		if !ok || !validate.IsUUID(r.GetUserId()) {
			return nil, reject(caller, "invalid_user_id", status.Error(codes.InvalidArgument, "user_id must be a canonical UUID"), nil)
		}
		ctx = WithIdentity(ctx, IdentityContext{UserID: r.GetUserId(), Roles: []string{"service:" + caller}})
		return handler(ctx, req)
	}
}

const unknownCaller = "unknown"

// rejectionLogs bounds Warn lines to one per method/caller/reason per interval; the counter keeps the full rate.
var rejectionLogs = &logLimiter{every: 10 * time.Second, last: map[string]time.Time{}, dropped: map[string]int{}}

type logLimiter struct {
	every   time.Duration
	mu      sync.Mutex
	last    map[string]time.Time // keys are bounded like the metric labels
	dropped map[string]int
}

// allow reports whether key may log now and how many lines were dropped since it last did.
func (l *logLimiter) allow(key string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.last[key]) < l.every {
		l.dropped[key]++
		return 0, false
	}
	n := l.dropped[key]
	l.last[key], l.dropped[key] = time.Now(), 0
	return n, true
}

// claimedCaller is the token's unverified issuer if it names a trusted service, so labels stay bounded.
func (v *ServiceVerifier) claimedCaller(token string) string {
	claims := &Claims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return unknownCaller
	}
	if _, ok := v.keys[claims.Issuer]; !ok {
		return unknownCaller
	}
	return claims.Issuer
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
