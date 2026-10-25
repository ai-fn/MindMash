package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// Audience marks tokens meant for MindMesh's own API; service tokens use their own audience.
const Audience = "mindmesh"

// clockLeeway absorbs clock skew between the issuer and verifiers.
const clockLeeway = 5 * time.Second

type Config struct {
	PrivateKeyPath  string
	PublicKeyPath   string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	Issuer          string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		PrivateKeyPath:  "keys/ed25519.pem",
		PublicKeyPath:   "keys/ed25519.pub.pem",
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh",
	}

	if v, ok := os.LookupEnv("JWT_PRIVATE_KEY_PATH"); ok {
		cfg.PrivateKeyPath = v
	}
	if v, ok := os.LookupEnv("JWT_PUBLIC_KEY_PATH"); ok {
		cfg.PublicKeyPath = v
	}
	if v, ok := os.LookupEnv("JWT_ACCESS_TTL"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("auth: parse JWT_ACCESS_TTL: %w", err)
		}
		cfg.AccessTokenTTL = d
	}
	if v, ok := os.LookupEnv("JWT_REFRESH_TTL"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("auth: parse JWT_REFRESH_TTL: %w", err)
		}
		cfg.RefreshTokenTTL = d
	}
	if v, ok := os.LookupEnv("JWT_ISSUER"); ok {
		cfg.Issuer = v
	}

	return cfg, nil
}

// Claims is the JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	ProfileID string   `json:"profile_id"`
	Locale    string   `json:"locale"`
	Roles     []string `json:"roles"`
	Kind      string   `json:"kind"`
}

type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	cfg  Config
	kid  string
}

func NewSigner(cfg Config) (*Signer, error) {
	privPEM, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: read private key: %w", err)
	}
	priv, err := parsePrivateKeyPEM(privPEM)
	if err != nil {
		return nil, err
	}

	pubPEM, err := os.ReadFile(cfg.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: read public key: %w", err)
	}
	pub, err := parsePublicKeyPEM(pubPEM)
	if err != nil {
		return nil, err
	}

	return NewSignerFromKeys(priv, pub, cfg), nil
}

func NewSignerFromKeys(priv ed25519.PrivateKey, pub ed25519.PublicKey, cfg Config) *Signer {
	return &Signer{
		priv: priv,
		pub:  pub,
		cfg:  cfg,
		kid:  ComputeKID(pub),
	}
}

// SignAccess signs an access token. Returns (tokenString, jti, error).
func (s *Signer) SignAccess(userID, profileID, locale string, roles []string) (string, string, error) {
	jti := ids.NewULID()
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.cfg.Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTokenTTL)),
		},
		ProfileID: profileID,
		Locale:    locale,
		Roles:     roles,
		Kind:      "access",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.kid

	tokenStr, err := token.SignedString(s.priv)
	if err != nil {
		return "", "", fmt.Errorf("auth: sign access token: %w", err)
	}
	return tokenStr, jti, nil
}

// SignRefresh signs a refresh token. Returns (tokenString, jti, error).
func (s *Signer) SignRefresh(userID, profileID string) (string, string, error) {
	jti := ids.NewULID()
	tok, err := s.SignRefreshWithID(userID, profileID, jti)
	return tok, jti, err
}

// SignRefreshWithID signs a refresh token using a caller-supplied jti, so the
// caller can persist the token's identity (e.g. a refresh_tokens row) before
// signing, keeping signing out of the database transaction.
func (s *Signer) SignRefreshWithID(userID, profileID, jti string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.cfg.Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.RefreshTokenTTL)),
		},
		ProfileID: profileID,
		Kind:      "refresh",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.kid

	tokenStr, err := token.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("auth: sign refresh token: %w", err)
	}
	return tokenStr, nil
}

// Verify parses and validates a JWT. Returns apperr.Unauthenticated on any failure.
func (s *Signer) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return s.pub, nil
	}, userTokenOptions(s.cfg.Issuer)...)
	if err != nil {
		return nil, apperr.Unauthenticated("invalid token", err)
	}
	return claims, nil
}

// userTokenOptions is the validation every verifier applies to user tokens; an empty issuer skips the iss check.
func userTokenOptions(issuer string) []jwt.ParserOption {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(clockLeeway),
	}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	return opts
}

func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.pub
}

// KID returns the first 16 hex characters of the SHA-256 of the raw public
// key bytes, used as the JWT key ID.
func (s *Signer) KID() string {
	return s.kid
}

// JWK represents a single JSON Web Key.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Use string `json:"use"`
	Kid string `json:"kid"`
}

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

func BuildJWKS(signers ...*Signer) JWKS {
	keys := make([]JWK, 0, len(signers))
	for _, s := range signers {
		keys = append(keys, JWK{
			Kty: "OKP",
			Crv: "Ed25519",
			X:   base64.RawURLEncoding.EncodeToString(s.pub),
			Use: "sig",
			Kid: s.kid,
		})
	}
	return JWKS{Keys: keys}
}

func parsePrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("auth: no PEM block found in private key")
	}

	switch block.Type {
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parse PKCS8 private key: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("auth: private key is not Ed25519, got %T", key)
		}
		return priv, nil

	case "ED25519 PRIVATE KEY":
		if len(block.Bytes) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(block.Bytes), nil
		}
		// Try PKCS8 anyway
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parse ED25519 PRIVATE KEY block: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("auth: key is not Ed25519, got %T", key)
		}
		return priv, nil

	default:
		// Try PKCS#8 first on unknown block types, then fall back to treating
		// the DER bytes as a raw 32-byte Ed25519 seed for tooling compatibility.
		if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			if priv, ok := key.(ed25519.PrivateKey); ok {
				return priv, nil
			}
		}
		if len(block.Bytes) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(block.Bytes), nil
		}
		return nil, fmt.Errorf("auth: unsupported PEM block type %q", block.Type)
	}
}

func parsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("auth: no PEM block found in public key")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("auth: unsupported public key PEM block type %q", block.Type)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: parse PKIX public key: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("auth: public key is not Ed25519, got %T", key)
	}
	return pub, nil
}

// ComputeKID returns the first 16 hex characters of the SHA-256 of the raw
// public key bytes, used as the JWT key ID.
func ComputeKID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}
