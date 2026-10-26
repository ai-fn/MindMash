// Package identity contains the domain layer for the Identity service:
// user registration/authentication, OAuth, profile management, and token lifecycle.
package identity

import (
	"context"
	"time"
)

// DefaultRoles is hard-coded until RBAC is a requirement; a fresh slice so callers can't mutate it.
func DefaultRoles() []string { return []string{"user"} }

// ─── Domain entities ────────────────────────────────────────────────────────

// TokenSet is what a sign-in or a refresh hands the client.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // access-token lifetime, seconds
	UserID       string
	ProfileID    string
}

// User is the root aggregate for an authenticated principal.
type User struct {
	ID            string
	Email         string
	EmailVerified bool
	PasswordHash  *string // nil for OAuth-only accounts
	Locale        string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// OAuthIdentity links a User to an external OAuth provider.
type OAuthIdentity struct {
	ID          string
	UserID      string
	Provider    string
	ProviderUID string
	CreatedAt   time.Time
}

// Profile is a named persona that belongs to a User.  One profile per user is
// marked is_default; the others are secondary.
type Profile struct {
	ID        string
	UserID    string
	Name      string
	IsDefault bool
	CreatedAt time.Time
}

// RefreshToken is a single-use token stored in Postgres that enables token
// rotation with family-level revocation on reuse detection.
type RefreshToken struct {
	JTI        string
	UserID     string
	ProfileID  *string // nil when no profile is bound at issuance
	FamilyID   string
	ExpiresAt  time.Time
	Revoked    bool
	RotatedAgo *time.Duration // time since rotation on the database clock; nil unless rotated
	CreatedAt  time.Time
}

// Email token purposes.
const (
	purposeVerifyEmail   = "verify_email"
	purposePasswordReset = "password_reset"
)

// EmailVerification is a one-time token sent via email for address verification
// or password-reset flows. Only the token's hash is stored.
type EmailVerification struct {
	TokenHash  string
	UserID     string
	Purpose    string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

// ─── Repository ─────────────────────────────────────────────────────────────

// Repository returns apperr.NotFound for a missing row and AlreadyExists on a unique violation.
type Repository interface {
	// User operations
	CreateUser(ctx context.Context, u *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	// GetUserForUpdate and GetUserForShare lock the user row until the transaction ends.
	// Anything that revokes or mints sessions takes them (update / share) so a revoke cannot miss a concurrent mint.
	GetUserForUpdate(ctx context.Context, id string) (*User, error)
	GetUserForShare(ctx context.Context, id string) (*User, error)
	// GetUserByEmail returns apperr.NotFound when no user matches.
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	SetEmailVerified(ctx context.Context, userID string) error
	UpdatePassword(ctx context.Context, userID, hash string) error
	ClearPassword(ctx context.Context, userID string) error

	// OAuth operations
	CreateOAuthIdentity(ctx context.Context, o *OAuthIdentity) error
	GetOAuthIdentity(ctx context.Context, provider, uid string) (*OAuthIdentity, error)

	// Profile operations
	CreateProfile(ctx context.Context, p *Profile) error
	// ListProfiles returns profiles for userID ordered by created_at ascending.
	ListProfiles(ctx context.Context, userID string) ([]Profile, error)
	GetProfile(ctx context.Context, id string) (*Profile, error)

	// RefreshToken operations
	CreateRefreshToken(ctx context.Context, rt *RefreshToken) error
	// GetRefreshTokenForUpdate fetches and locks the row (SELECT … FOR UPDATE)
	// for use inside a transaction during token rotation.
	GetRefreshTokenForUpdate(ctx context.Context, jti string) (*RefreshToken, error)
	// RotateRefreshToken revokes the token as superseded.
	RotateRefreshToken(ctx context.Context, jti string) error
	RevokeFamily(ctx context.Context, familyID string) error
	// RevokeUserTokens revokes every live refresh token of the user.
	RevokeUserTokens(ctx context.Context, userID string) error

	// EmailVerification operations
	CreateEmailVerification(ctx context.Context, ev *EmailVerification) error
	// ConsumeEmailVerification atomically marks the token consumed and returns its user.
	// Fails when the hash is unknown, already consumed, expired, or issued for another purpose.
	ConsumeEmailVerification(ctx context.Context, tokenHash, purpose string) (userID string, err error)
	// EmailVerificationUser returns the user a live token belongs to without consuming it,
	// so callers can lock the user row before the token row.
	EmailVerificationUser(ctx context.Context, tokenHash, purpose string) (userID string, err error)
	// InvalidateEmailVerifications consumes every outstanding token of the user.
	InvalidateEmailVerifications(ctx context.Context, userID string) error

	// WithTx scopes fn's Repository to the transaction; don't use the outer one inside fn.
	WithTx(ctx context.Context, fn func(Repository) error) error
}

// ─── OAuth exchanger ─────────────────────────────────────────────────────────

// OAuthUserInfo is the normalised identity returned by the OAuth provider after
// a successful code exchange.
type OAuthUserInfo struct {
	Provider      string
	Subject       string // provider's stable user identifier (sub claim)
	Email         string
	EmailVerified bool
}

// OAuthFlow is what the server remembered about an authorization request.
type OAuthFlow struct {
	RedirectURI string `json:"redirect_uri"`
	Nonce       string `json:"nonce"`    // must come back in the ID token
	Verifier    string `json:"verifier"` // PKCE code verifier
}

// OAuthExchanger builds provider authorization URLs and turns authorization codes into OAuthUserInfo.
// Implementations handle OIDC discovery, token exchange and ID-token verification.
type OAuthExchanger interface {
	AuthURL(ctx context.Context, provider string, flow OAuthFlow, state string) (string, error)
	Exchange(ctx context.Context, provider, code string, flow OAuthFlow) (OAuthUserInfo, error)
}

// ─── Email sender ────────────────────────────────────────────────────────────

// EmailSender renders in the recipient's locale; the dev stub just logs the token.
type EmailSender interface {
	SendVerification(ctx context.Context, email, token, locale string) error
	SendPasswordReset(ctx context.Context, email, token, locale string) error
}
