package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// errTokenReuse is a private sentinel returned from within the Refresh
// transaction to signal reuse detection without carrying the full
// Unauthenticated error through WithTx (which would roll back the tx before
// we can revoke the family on the pool connection).
var errTokenReuse = errors.New("token reuse detected")

const (
	statusActive = "active"

	loginLimit       = 10
	loginWindow      = 15 * time.Minute
	resetLimit       = 3
	resetWindow      = time.Hour
	rateLimitKeyBase = "rl:"
)

// Service is the domain entry-point for all identity operations.  It composes
// the repository, session store, JWT signer, OAuth exchanger, and email sender
// so that each handler stays thin.
type Service struct {
	repo       Repository
	sessions   SessionStore
	signer     *auth.Signer
	exchanger  OAuthExchanger
	email      EmailSender
	log        *zap.Logger
	accessTTL  time.Duration
	refreshTTL time.Duration

	limiter RateLimiter // nil disables rate limiting

	// hashSlots bounds concurrent argon2 runs (64 MiB each) so a login flood cannot exhaust memory.
	hashSlots chan struct{}

	// dummyHash is verified for unknown accounts so login time does not reveal whether an email exists.
	dummyOnce sync.Once
	dummyHash string
	dummyErr  error
}

// Option customises a Service.
type Option func(*Service)

// WithRateLimiter enables per-account rate limits on login and password reset requests.
func WithRateLimiter(l RateLimiter) Option { return func(s *Service) { s.limiter = l } }

// WithHashConcurrency caps concurrent password hashes (default GOMAXPROCS).
func WithHashConcurrency(n int) Option {
	return func(s *Service) { s.hashSlots = make(chan struct{}, max(n, 1)) }
}

// NewService constructs a Service.  TTLs are pulled from the auth.Config so
// token lifetimes stay consistent between the signer and the DB rows.
func NewService(
	repo Repository,
	sessions SessionStore,
	signer *auth.Signer,
	exchanger OAuthExchanger,
	email EmailSender,
	log *zap.Logger,
	authCfg auth.Config,
	opts ...Option,
) *Service {
	s := &Service{
		repo:       repo,
		sessions:   sessions,
		signer:     signer,
		exchanger:  exchanger,
		email:      email,
		log:        log,
		accessTTL:  authCfg.AccessTokenTTL,
		refreshTTL: authCfg.RefreshTokenTTL,
		hashSlots:  make(chan struct{}, runtime.GOMAXPROCS(0)),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ─── Registration ────────────────────────────────────────────────────────────

// Register creates a new user, a default profile, and dispatches a verification
// email.  All three DB inserts run inside a single transaction so a crash
// mid-way cannot leave a user with no profile.  The email send stays outside
// the transaction — IO against an external gateway must not hold a DB
// connection open, and a send failure is not fatal (log and continue so that a
// misconfigured email gateway cannot block sign-ups).
func (s *Service) Register(ctx context.Context, email, password, locale string) (string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return "", err
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	locale = normalizeLocale(locale)

	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return "", fmt.Errorf("identity: register hash password: %w", err)
	}

	now := time.Now()
	user := &User{
		ID:            ids.NewUUID(),
		Email:         email,
		EmailVerified: false,
		PasswordHash:  &hash,
		Locale:        locale,
		Status:        statusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var verificationToken string
	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		if err := txRepo.CreateUser(ctx, user); err != nil {
			return err // AlreadyExists from the repo propagates as-is
		}

		profile := &Profile{
			ID:        ids.NewUUID(),
			UserID:    user.ID,
			Name:      "Default",
			IsDefault: true,
			CreatedAt: now,
		}
		if err := txRepo.CreateProfile(ctx, profile); err != nil {
			return fmt.Errorf("identity: register create profile: %w", err)
		}

		token, err := generateVerificationToken()
		if err != nil {
			return fmt.Errorf("identity: register generate token: %w", err)
		}
		verificationToken = token

		ev := &EmailVerification{
			Token:     token,
			UserID:    user.ID,
			Purpose:   "verify_email",
			ExpiresAt: now.Add(24 * time.Hour),
			CreatedAt: now,
		}
		if err := txRepo.CreateEmailVerification(ctx, ev); err != nil {
			return fmt.Errorf("identity: register create verification: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return "", txErr
	}

	if err := s.email.SendVerification(ctx, email, verificationToken, locale); err != nil {
		// A send failure is not fatal; log it and continue so that a broken
		// email gateway cannot prevent users from completing registration.
		s.log.Error("identity: register send verification email", zap.Error(err), zap.String("user_id", user.ID))
	}

	return user.ID, nil
}

// ─── Login ───────────────────────────────────────────────────────────────────

// Login authenticates a user by email/password and issues a token set.
func (s *Service) Login(ctx context.Context, email, password string) (accessToken, refreshToken string, expiresIn int64, userID, profileID string, err error) {
	invalid := platformerrors.Unauthenticated("invalid credentials").WithMsgID(i18n.MsgInvalidCredentials)

	email, normErr := normalizeEmail(email)
	if normErr != nil || len(password) > maxPasswordBytes {
		return "", "", 0, "", "", invalid
	}
	if err := s.allow(ctx, "login", email, loginLimit, loginWindow); err != nil {
		return "", "", 0, "", "", err
	}

	user, lookupErr := s.repo.GetUserByEmail(ctx, email)
	if lookupErr != nil && !platformerrors.IsNotFound(lookupErr) {
		return "", "", 0, "", "", fmt.Errorf("identity: login lookup user: %w", lookupErr)
	}

	// Unknown and OAuth-only accounts still pay for a hash so timing matches a wrong password.
	hash, hashErr := s.dummyPasswordHash()
	if hashErr != nil {
		return "", "", 0, "", "", hashErr
	}
	known := lookupErr == nil && user.PasswordHash != nil
	if known {
		hash = *user.PasswordHash
	}
	ok, verifyErr := s.verifyPassword(ctx, password, hash)
	if verifyErr != nil {
		return "", "", 0, "", "", fmt.Errorf("identity: login verify password: %w", verifyErr)
	}
	if !known || !ok {
		return "", "", 0, "", "", invalid
	}
	if err := requireActive(user); err != nil {
		return "", "", 0, "", "", err
	}
	// Successful sign-ins must not eat the budget meant for guessing, or an attacker could lock the owner out.
	s.resetLimit(ctx, "login", email)

	profile, err := s.pickDefaultProfile(ctx, user.ID)
	if err != nil {
		return "", "", 0, "", "", err
	}

	access, refresh, expiresIn, err := s.issueTokens(ctx, user, profile)
	if err != nil {
		return "", "", 0, "", "", err
	}
	return access, refresh, expiresIn, user.ID, profile.ID, nil
}

// ─── OAuth ───────────────────────────────────────────────────────────────────

// OAuthExchange performs a code exchange with the given provider and either
// creates a new account or links/finds an existing one.
func (s *Service) OAuthExchange(ctx context.Context, provider, code, redirectURI string) (accessToken, refreshToken string, expiresIn int64, userID, profileID string, isNew bool, err error) {
	info, err := s.exchanger.Exchange(ctx, provider, code, redirectURI)
	if err != nil {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange: %w", err)
	}
	if info.Subject == "" {
		return "", "", 0, "", "", false, platformerrors.Unauthenticated("oauth provider returned no subject")
	}
	info.Email = strings.TrimSpace(info.Email)

	// Try the fast path: identity already exists.
	oauthID, lookupErr := s.repo.GetOAuthIdentity(ctx, info.Provider, info.Subject)
	if lookupErr == nil {
		// Known provider+uid → load the user.
		user, err := s.repo.GetUserByID(ctx, oauthID.UserID)
		if err != nil {
			return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange load user: %w", err)
		}
		if err := requireActive(user); err != nil {
			return "", "", 0, "", "", false, err
		}
		profile, err := s.pickDefaultProfile(ctx, user.ID)
		if err != nil {
			return "", "", 0, "", "", false, err
		}
		access, refresh, expiresIn, err := s.issueTokens(ctx, user, profile)
		if err != nil {
			return "", "", 0, "", "", false, err
		}
		return access, refresh, expiresIn, user.ID, profile.ID, false, nil
	}

	if !platformerrors.IsNotFound(lookupErr) {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange lookup identity: %w", lookupErr)
	}
	// An empty email would match any other account stored without one.
	if info.Email == "" {
		return "", "", 0, "", "", false, platformerrors.FailedPrecondition("oauth provider returned no email")
	}
	// Linking or creating from an unproven address would let its holder squat or take over the account.
	if !info.EmailVerified {
		return "", "", 0, "", "", false, platformerrors.FailedPrecondition("oauth email is not verified").
			WithMsgID(i18n.MsgOAuthEmailUnverified)
	}

	// New OAuth identity: try to link by email first.
	now := time.Now()
	existingUser, emailErr := s.repo.GetUserByEmail(ctx, info.Email)
	if emailErr != nil && !platformerrors.IsNotFound(emailErr) {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange lookup email: %w", emailErr)
	}
	if emailErr == nil {
		if !existingUser.EmailVerified {
			return "", "", 0, "", "", false, platformerrors.AlreadyExists(
				"an account with this email exists; verify its email before signing in with this provider").
				WithMsgID(i18n.MsgOAuthLinkUnverified)
		}
		if err := requireActive(existingUser); err != nil {
			return "", "", 0, "", "", false, err
		}
		// Link the new OAuth identity to the existing account atomically so a
		// crash between lookup and insert cannot leave a dangling partial link.
		newIdentity := &OAuthIdentity{
			ID:          ids.NewUUID(),
			UserID:      existingUser.ID,
			Provider:    info.Provider,
			ProviderUID: info.Subject,
			CreatedAt:   now,
		}
		if txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
			return txRepo.CreateOAuthIdentity(ctx, newIdentity)
		}); txErr != nil {
			return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange link identity: %w", txErr)
		}
		profile, err := s.pickDefaultProfile(ctx, existingUser.ID)
		if err != nil {
			return "", "", 0, "", "", false, err
		}
		access, refresh, expiresIn, err := s.issueTokens(ctx, existingUser, profile)
		if err != nil {
			return "", "", 0, "", "", false, err
		}
		return access, refresh, expiresIn, existingUser.ID, profile.ID, false, nil
	}

	// Brand-new user: create user, profile, and identity atomically so a crash
	// mid-way cannot leave a user row with no profile or identity.
	locale := "en"
	newUser := &User{
		ID:            ids.NewUUID(),
		Email:         info.Email,
		EmailVerified: info.EmailVerified,
		PasswordHash:  nil,
		Locale:        locale,
		Status:        statusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	newProfile := &Profile{
		ID:        ids.NewUUID(),
		UserID:    newUser.ID,
		Name:      "Default",
		IsDefault: true,
		CreatedAt: now,
	}
	newIdentity := &OAuthIdentity{
		ID:          ids.NewUUID(),
		UserID:      newUser.ID,
		Provider:    info.Provider,
		ProviderUID: info.Subject,
		CreatedAt:   now,
	}
	if txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		if err := txRepo.CreateUser(ctx, newUser); err != nil {
			return fmt.Errorf("identity: oauth exchange create user: %w", err)
		}
		if err := txRepo.CreateProfile(ctx, newProfile); err != nil {
			return fmt.Errorf("identity: oauth exchange create profile: %w", err)
		}
		if err := txRepo.CreateOAuthIdentity(ctx, newIdentity); err != nil {
			return fmt.Errorf("identity: oauth exchange create identity: %w", err)
		}
		return nil
	}); txErr != nil {
		return "", "", 0, "", "", false, txErr
	}

	access, refresh, expiresIn, err := s.issueTokens(ctx, newUser, newProfile)
	if err != nil {
		return "", "", 0, "", "", false, err
	}
	return access, refresh, expiresIn, newUser.ID, newProfile.ID, true, nil
}

// ─── Token rotation ──────────────────────────────────────────────────────────

// Refresh rotates the refresh token.  The old token is revoked atomically
// inside a transaction; if the token is already revoked the entire family is
// revoked (reuse/theft detection).
//
// Reuse detection requires special care: we must NOT revoke the family inside
// the WithTx closure, because returning an error from fn causes WithTx to roll
// back the transaction, silently un-doing the revocation.  Instead we return
// the private errTokenReuse sentinel to exit the tx cleanly (with rollback),
// capture the family ID in an outer variable, and call RevokeFamily on the
// pool (its own autocommit statement) after WithTx returns.
//
// Session-store updates are best-effort: Redis downtime must not break the
// refresh path since Postgres is the source of truth.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (accessToken, newRefreshToken string, expiresIn int64, err error) {
	claims, err := s.signer.Verify(refreshToken)
	if err != nil {
		return "", "", 0, platformerrors.Unauthenticated("invalid token")
	}
	if claims.Kind != "refresh" {
		return "", "", 0, platformerrors.Unauthenticated("invalid token")
	}
	jti := claims.ID

	var (
		newAccessToken   string
		newRefreshString string
		newExpiresIn     int64
		newJTI           string
		// reuseFamilyID is set inside the tx when reuse is detected; the actual
		// RevokeFamily call happens after WithTx returns so it is not rolled back.
		reuseFamilyID string
		// rotatedFamilyID is captured inside the tx for the post-commit session
		// mirror — it is the family that the new token belongs to.
		rotatedFamilyID string
		// txUserID / txProfileID are captured inside the tx and consumed after
		// commit so that GetUserByID and signing happen outside the transaction.
		txUserID    string
		txProfileID string
	)

	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		rt, err := txRepo.GetRefreshTokenForUpdate(ctx, jti)
		if err != nil {
			// Only a NotFound (unknown / already-deleted token) becomes
			// Unauthenticated.  Real infra errors propagate so they are
			// observable as Internal/Unavailable, not silently swallowed.
			if status.Code(err) == codes.NotFound {
				return platformerrors.Unauthenticated("invalid token")
			}
			return fmt.Errorf("identity: refresh get token: %w", err)
		}

		if rt.Revoked {
			// Token reuse detected.  Capture the family ID and return the
			// sentinel so WithTx rolls back without attempting any further
			// writes; we revoke the family outside the tx below.
			reuseFamilyID = rt.FamilyID
			return errTokenReuse
		}

		if time.Now().After(rt.ExpiresAt) {
			return platformerrors.Unauthenticated("token expired")
		}

		// Capture family, user, and profile for post-commit work.
		rotatedFamilyID = rt.FamilyID
		txUserID = rt.UserID
		if rt.ProfileID != nil {
			txProfileID = *rt.ProfileID
		}

		if err := txRepo.RevokeRefreshToken(ctx, jti); err != nil {
			return fmt.Errorf("identity: refresh revoke old token: %w", err)
		}

		// Generate the new JTI here so we can INSERT the row inside the tx
		// while signing happens outside (after commit).
		newJTI = ids.NewULID()

		now := time.Now()
		newRT := &RefreshToken{
			JTI:       newJTI,
			UserID:    rt.UserID,
			ProfileID: rt.ProfileID,
			FamilyID:  rt.FamilyID, // preserve the family for reuse tracking
			ExpiresAt: now.Add(s.refreshTTL),
			Revoked:   false,
			CreatedAt: now,
		}
		if err := txRepo.CreateRefreshToken(ctx, newRT); err != nil {
			return fmt.Errorf("identity: refresh create new token: %w", err)
		}

		return nil
	})

	if errors.Is(txErr, errTokenReuse) {
		// Revoke the whole family on the pool (autocommit) so it is durable.
		jtis, rErr := s.repo.RevokeFamily(ctx, reuseFamilyID)
		if rErr != nil {
			s.log.Error("identity: refresh revoke family on reuse", zap.Error(rErr))
		}
		// Best-effort: evict all family sessions from the store.
		if len(jtis) > 0 {
			if sErr := s.sessions.Delete(ctx, jtis...); sErr != nil {
				s.log.Warn("identity: refresh delete reuse sessions", zap.Error(sErr))
			}
		}
		return "", "", 0, platformerrors.Unauthenticated("token reuse detected")
	}
	if txErr != nil {
		return "", "", 0, txErr
	}

	// Tx committed: now sign outside the transaction so we never hold a DB
	// lock while doing crypto.  newJTI was generated inside the tx and matches
	// the row already INSERTed, so signing here produces a token whose JTI
	// equals the persisted row's JTI.
	userID := txUserID

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", "", 0, fmt.Errorf("identity: refresh load user: %w", err)
	}
	if err := requireActive(user); err != nil {
		s.revokeFamilyBestEffort(ctx, rotatedFamilyID)
		return "", "", 0, err
	}

	newAccessToken, _, err = s.signer.SignAccess(userID, txProfileID, user.Locale, DefaultRoles())
	if err != nil {
		return "", "", 0, fmt.Errorf("identity: refresh sign access: %w", err)
	}
	newExpiresIn = int64(s.accessTTL.Seconds())

	newRefreshString, err = s.signer.SignRefreshWithID(userID, txProfileID, newJTI)
	if err != nil {
		return "", "", 0, fmt.Errorf("identity: refresh sign refresh: %w", err)
	}

	// Best-effort session-store update; Redis downtime cannot fail the request.
	if sErr := s.sessions.Delete(ctx, jti); sErr != nil {
		s.log.Warn("identity: refresh delete old session", zap.Error(sErr), zap.String("jti", jti))
	}
	newSess := RefreshSession{
		UserID:    userID,
		ProfileID: txProfileID,
		FamilyID:  rotatedFamilyID,
		ExpiresAt: time.Now().Add(s.refreshTTL),
	}
	if sErr := s.sessions.Put(ctx, newJTI, newSess, s.refreshTTL); sErr != nil {
		s.log.Warn("identity: refresh put new session", zap.Error(sErr), zap.String("jti", newJTI))
	}

	return newAccessToken, newRefreshString, newExpiresIn, nil
}

// ─── Logout ──────────────────────────────────────────────────────────────────

// Logout revokes the token's family and clears session-store entries.
// The FOR UPDATE lock and the family revocation run inside a single transaction
// so they are atomic — the lock is meaningless across separate statements.
// The operation is best-effort and always returns success to the caller; we
// never leak token validity information via the logout response.
func (s *Service) Logout(ctx context.Context, refreshToken string) {
	claims, err := s.signer.Verify(refreshToken)
	if err != nil || claims.Kind != "refresh" {
		return
	}

	var jtis []string
	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		rt, err := txRepo.GetRefreshTokenForUpdate(ctx, claims.ID)
		if err != nil {
			// Token not found or already gone — nothing to revoke; treat as success.
			return nil
		}
		rjtis, err := txRepo.RevokeFamily(ctx, rt.FamilyID)
		if err != nil {
			return fmt.Errorf("identity: logout revoke family: %w", err)
		}
		jtis = rjtis
		return nil
	})
	if txErr != nil {
		s.log.Warn("identity: logout revoke family tx", zap.Error(txErr))
		return
	}

	if len(jtis) > 0 {
		if sErr := s.sessions.Delete(ctx, jtis...); sErr != nil {
			s.log.Warn("identity: logout delete sessions", zap.Error(sErr))
		}
	}
}

// ─── User info ───────────────────────────────────────────────────────────────

// GetMe returns the authenticated user together with all of their profiles so
// the caller never needs two round-trips for the most common account page.
func (s *Service) GetMe(ctx context.Context, userID string) (*User, []Profile, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: get me load user: %w", err)
	}
	profiles, err := s.repo.ListProfiles(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: get me list profiles: %w", err)
	}
	return user, profiles, nil
}

// ─── Profile management ──────────────────────────────────────────────────────

// ListProfiles returns all profiles for the given user.
func (s *Service) ListProfiles(ctx context.Context, userID string) ([]Profile, error) {
	return s.repo.ListProfiles(ctx, userID)
}

// CreateProfile adds a non-default profile for the user.
func (s *Service) CreateProfile(ctx context.Context, userID, name string) (*Profile, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return nil, err
	}
	p := &Profile{
		ID:        ids.NewUUID(),
		UserID:    userID,
		Name:      name,
		IsDefault: false,
		CreatedAt: time.Now(),
	}
	if err := s.repo.CreateProfile(ctx, p); err != nil {
		return nil, fmt.Errorf("identity: create profile: %w", err)
	}
	return p, nil
}

// ActivateProfile mints a new access token scoped to the given profile.
//
// Note: the refresh token keeps its original profile binding.  ActivateProfile
// only re-scopes the access token, which is short-lived and re-issued on each
// refresh.  To fully re-scope a session the client must call Logout and Login
// again with the target profile.
func (s *Service) ActivateProfile(ctx context.Context, userID, profileID string) (accessToken string, expiresIn int64, err error) {
	profile, err := s.repo.GetProfile(ctx, profileID)
	if err != nil {
		return "", 0, err // NotFound propagates
	}
	if profile.UserID != userID {
		return "", 0, platformerrors.PermissionDenied("profile does not belong to this user")
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", 0, fmt.Errorf("identity: activate profile load user: %w", err)
	}
	if err := requireActive(user); err != nil {
		return "", 0, err
	}

	token, _, err := s.signer.SignAccess(user.ID, profile.ID, user.Locale, DefaultRoles())
	if err != nil {
		return "", 0, fmt.Errorf("identity: activate profile sign access: %w", err)
	}
	return token, int64(s.accessTTL.Seconds()), nil
}

// ─── Internal RPCs ───────────────────────────────────────────────────────────

// ValidateToken verifies an access token structurally (no DB hit).  This is
// the fast path used by the gateway JWKS cache so it must never block on I/O.
func (s *Service) ValidateToken(_ context.Context, token string) (userID, profileID, locale string, roles []string, err error) {
	claims, err := s.signer.Verify(token)
	if err != nil {
		return "", "", "", nil, platformerrors.Unauthenticated("invalid token")
	}
	if claims.Kind != "access" {
		return "", "", "", nil, platformerrors.Unauthenticated("invalid token kind")
	}
	return claims.Subject, claims.ProfileID, claims.Locale, claims.Roles, nil
}

// GetProfile loads a profile by ID.  Used by downstream services to resolve
// a profile_id carried in JWT claims.
func (s *Service) GetProfile(ctx context.Context, profileID string) (*Profile, error) {
	return s.repo.GetProfile(ctx, profileID)
}

// ─── Email verification & password reset ─────────────────────────────────────

// VerifyEmail consumes a verification token and marks the user's email as
// verified.  Both operations run inside a single transaction so concurrent
// requests cannot both pass the consumed/expiry check (TOCTOU-safe).
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	return s.repo.WithTx(ctx, func(txRepo Repository) error {
		userID, purpose, err := txRepo.ConsumeEmailVerification(ctx, token)
		if err != nil {
			return platformerrors.InvalidArgument("invalid or expired verification token").WithMsgID(i18n.MsgInvalidOrExpiredToken)
		}
		if purpose != "verify_email" {
			return platformerrors.InvalidArgument("invalid token purpose")
		}
		if err := txRepo.SetEmailVerified(ctx, userID); err != nil {
			return fmt.Errorf("identity: verify email set verified: %w", err)
		}
		return nil
	})
}

// RequestPasswordReset creates a reset token and sends a reset email.
// The method always returns nil to avoid leaking whether the email exists.
func (s *Service) RequestPasswordReset(ctx context.Context, emailAddr string) error {
	emailAddr, err := normalizeEmail(emailAddr)
	if err != nil {
		return nil
	}
	// Over the limit looks like success so the response cannot be used to probe accounts.
	if err := s.allow(ctx, "reset", emailAddr, resetLimit, resetWindow); err != nil {
		return nil
	}
	user, err := s.repo.GetUserByEmail(ctx, emailAddr)
	if err != nil {
		// Do not reveal whether the address is registered.
		return nil
	}

	token, err := generateVerificationToken()
	if err != nil {
		return fmt.Errorf("identity: request password reset generate token: %w", err)
	}
	now := time.Now()
	ev := &EmailVerification{
		Token:     token,
		UserID:    user.ID,
		Purpose:   "password_reset",
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := s.repo.CreateEmailVerification(ctx, ev); err != nil {
		return fmt.Errorf("identity: request password reset create token: %w", err)
	}
	if err := s.email.SendPasswordReset(ctx, emailAddr, token, user.Locale); err != nil {
		s.log.Error("identity: request password reset send email", zap.Error(err))
	}
	return nil
}

// ResetPassword validates a password-reset token and updates the user's
// password.  Consumption and the password update run inside a single
// transaction so concurrent requests cannot both pass the consumed/expiry
// check (TOCTOU-safe).
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.hashPassword(ctx, newPassword)
	if err != nil {
		return fmt.Errorf("identity: reset password hash: %w", err)
	}

	return s.repo.WithTx(ctx, func(txRepo Repository) error {
		userID, purpose, err := txRepo.ConsumeEmailVerification(ctx, token)
		if err != nil {
			return platformerrors.InvalidArgument("invalid or expired reset token").WithMsgID(i18n.MsgInvalidOrExpiredToken)
		}
		if purpose != "password_reset" {
			return platformerrors.InvalidArgument("invalid token purpose")
		}
		if err := txRepo.UpdatePassword(ctx, userID, hash); err != nil {
			return fmt.Errorf("identity: reset password update: %w", err)
		}
		return nil
	})
}

// ─── JWKS ────────────────────────────────────────────────────────────────────

// JWKS returns the public-key set used to verify tokens issued by this service.
func (s *Service) JWKS() auth.JWKS {
	return auth.BuildJWKS(s.signer)
}

// ─── Private helpers ─────────────────────────────────────────────────────────

// issueTokens creates a new token family, persists the refresh token row, puts
// the session into the store, and returns the signed strings.
func (s *Service) issueTokens(ctx context.Context, user *User, profile *Profile) (accessToken, refreshToken string, expiresIn int64, err error) {
	familyID := ids.NewULID()

	accessToken, _, err = s.signer.SignAccess(user.ID, profile.ID, user.Locale, DefaultRoles())
	if err != nil {
		return "", "", 0, fmt.Errorf("identity: issue tokens sign access: %w", err)
	}

	refreshToken, jti, err := s.signer.SignRefresh(user.ID, profile.ID)
	if err != nil {
		return "", "", 0, fmt.Errorf("identity: issue tokens sign refresh: %w", err)
	}

	now := time.Now()
	rt := &RefreshToken{
		JTI:       jti,
		UserID:    user.ID,
		ProfileID: &profile.ID,
		FamilyID:  familyID,
		ExpiresAt: now.Add(s.refreshTTL),
		Revoked:   false,
		CreatedAt: now,
	}
	if err := s.repo.CreateRefreshToken(ctx, rt); err != nil {
		return "", "", 0, fmt.Errorf("identity: issue tokens create refresh token: %w", err)
	}

	sess := RefreshSession{
		UserID:    user.ID,
		ProfileID: profile.ID,
		FamilyID:  familyID,
		ExpiresAt: rt.ExpiresAt,
	}
	if sErr := s.sessions.Put(ctx, jti, sess, s.refreshTTL); sErr != nil {
		// Redis being unavailable must not block login — DB is source of truth.
		s.log.Warn("identity: issue tokens put session", zap.Error(sErr))
	}

	expiresIn = int64(s.accessTTL.Seconds())
	return accessToken, refreshToken, expiresIn, nil
}

// pickDefaultProfile returns the user's default profile, falling back to the
// first profile if none is marked default (defensive: the schema enforces at
// most one default, but a user created via direct DB insert might lack one).
func (s *Service) pickDefaultProfile(ctx context.Context, userID string) (*Profile, error) {
	profiles, err := s.repo.ListProfiles(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: pick default profile list: %w", err)
	}
	for i := range profiles {
		if profiles[i].IsDefault {
			return &profiles[i], nil
		}
	}
	if len(profiles) > 0 {
		return &profiles[0], nil
	}
	return nil, platformerrors.Internal("user has no profiles")
}

func requireActive(u *User) error {
	if u.Status != statusActive {
		return platformerrors.PermissionDenied("account is not active")
	}
	return nil
}

func (s *Service) revokeFamilyBestEffort(ctx context.Context, familyID string) {
	jtis, err := s.repo.RevokeFamily(ctx, familyID)
	if err != nil {
		s.log.Error("identity: revoke family", zap.Error(err))
		return
	}
	if len(jtis) > 0 {
		if err := s.sessions.Delete(ctx, jtis...); err != nil {
			s.log.Warn("identity: delete revoked sessions", zap.Error(err))
		}
	}
}

func limiterKey(scope, key string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(key)))
	return rateLimitKeyBase + scope + ":" + hex.EncodeToString(sum[:16])
}

// allow counts one attempt for key; a limiter outage fails open so Redis downtime cannot block sign-in.
func (s *Service) allow(ctx context.Context, scope, key string, limit int, window time.Duration) error {
	if s.limiter == nil {
		return nil
	}
	ok, err := s.limiter.Allow(ctx, limiterKey(scope, key), limit, window)
	if err != nil {
		s.log.Warn("identity: rate limiter unavailable", zap.String("scope", scope), zap.Error(err))
		return nil
	}
	if !ok {
		return platformerrors.ResourceExhausted("too many attempts").WithMsgID(i18n.MsgTooManyAttempts)
	}
	return nil
}

func (s *Service) resetLimit(ctx context.Context, scope, key string) {
	if s.limiter == nil {
		return
	}
	if err := s.limiter.Reset(ctx, limiterKey(scope, key)); err != nil {
		s.log.Warn("identity: rate limiter reset failed", zap.String("scope", scope), zap.Error(err))
	}
}

// WarmUp builds the dummy hash up front so the first unknown-email login is not slower than later ones.
func (s *Service) WarmUp() error {
	_, err := s.dummyPasswordHash()
	return err
}

func (s *Service) acquireHashSlot(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, platformerrors.ResourceExhausted("server busy", err)
	}
	select {
	case s.hashSlots <- struct{}{}:
		return func() { <-s.hashSlots }, nil
	case <-ctx.Done():
		return nil, platformerrors.ResourceExhausted("server busy", ctx.Err())
	}
}

func (s *Service) hashPassword(ctx context.Context, password string) (string, error) {
	release, err := s.acquireHashSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return auth.HashPassword(password)
}

func (s *Service) verifyPassword(ctx context.Context, password, hash string) (bool, error) {
	release, err := s.acquireHashSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return auth.VerifyPassword(password, hash)
}

func (s *Service) dummyPasswordHash() (string, error) {
	s.dummyOnce.Do(func() {
		s.dummyHash, s.dummyErr = auth.HashPassword(ids.NewULID())
	})
	return s.dummyHash, s.dummyErr
}

// generateVerificationToken returns a 32-byte URL-safe opaque random string.
func generateVerificationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("identity: generate verification token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
