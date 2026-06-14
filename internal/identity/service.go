package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
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
) *Service {
	return &Service{
		repo:       repo,
		sessions:   sessions,
		signer:     signer,
		exchanger:  exchanger,
		email:      email,
		log:        log,
		accessTTL:  authCfg.AccessTokenTTL,
		refreshTTL: authCfg.RefreshTokenTTL,
	}
}

// ─── Registration ────────────────────────────────────────────────────────────

// Register creates a new user, a default profile, and dispatches a verification
// email.  A send failure does not abort registration — we log it and continue
// so that a misconfigured email gateway cannot block sign-ups.
func (s *Service) Register(ctx context.Context, email, password, locale string) (string, error) {
	if email == "" || password == "" {
		return "", platformerrors.InvalidArgument("email and password are required")
	}
	if locale == "" {
		locale = "en"
	}

	hash, err := auth.HashPassword(password)
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
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return "", err // AlreadyExists from the repo propagates as-is
	}

	profile := &Profile{
		ID:        ids.NewUUID(),
		UserID:    user.ID,
		Name:      "Default",
		IsDefault: true,
		CreatedAt: now,
	}
	if err := s.repo.CreateProfile(ctx, profile); err != nil {
		return "", fmt.Errorf("identity: register create profile: %w", err)
	}

	token, err := generateVerificationToken()
	if err != nil {
		return "", fmt.Errorf("identity: register generate token: %w", err)
	}
	ev := &EmailVerification{
		Token:     token,
		UserID:    user.ID,
		Purpose:   "verify_email",
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	if err := s.repo.CreateEmailVerification(ctx, ev); err != nil {
		return "", fmt.Errorf("identity: register create verification: %w", err)
	}

	if err := s.email.SendVerification(ctx, email, token); err != nil {
		// A send failure is not fatal; log it and continue so that a broken
		// email gateway cannot prevent users from completing registration.
		s.log.Error("identity: register send verification email", zap.Error(err), zap.String("user_id", user.ID))
	}

	return user.ID, nil
}

// ─── Login ───────────────────────────────────────────────────────────────────

// Login authenticates a user by email/password and issues a token set.
func (s *Service) Login(ctx context.Context, email, password string) (accessToken, refreshToken string, expiresIn int64, userID, profileID string, err error) {
	// Return a generic "invalid credentials" error for all failure cases to
	// prevent user-enumeration attacks.
	user, lookupErr := s.repo.GetUserByEmail(ctx, email)
	if lookupErr != nil {
		return "", "", 0, "", "", platformerrors.Unauthenticated("invalid credentials")
	}

	if user.PasswordHash == nil {
		// OAuth-only account; no password set.
		return "", "", 0, "", "", platformerrors.Unauthenticated("invalid credentials")
	}

	ok, verifyErr := auth.VerifyPassword(password, *user.PasswordHash)
	if verifyErr != nil {
		return "", "", 0, "", "", fmt.Errorf("identity: login verify password: %w", verifyErr)
	}
	if !ok {
		return "", "", 0, "", "", platformerrors.Unauthenticated("invalid credentials")
	}

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

	// Try the fast path: identity already exists.
	oauthID, lookupErr := s.repo.GetOAuthIdentity(ctx, info.Provider, info.Subject)
	if lookupErr == nil {
		// Known provider+uid → load the user.
		user, err := s.repo.GetUserByID(ctx, oauthID.UserID)
		if err != nil {
			return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange load user: %w", err)
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

	// New OAuth identity: try to link by email first.
	now := time.Now()
	existingUser, emailErr := s.repo.GetUserByEmail(ctx, info.Email)
	if emailErr == nil {
		// Link the new OAuth identity to the existing account.
		newIdentity := &OAuthIdentity{
			ID:          ids.NewUUID(),
			UserID:      existingUser.ID,
			Provider:    info.Provider,
			ProviderUID: info.Subject,
			CreatedAt:   now,
		}
		if err := s.repo.CreateOAuthIdentity(ctx, newIdentity); err != nil {
			return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange link identity: %w", err)
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

	// Brand-new user.
	locale := "en"
	newUser := &User{
		ID:            ids.NewUUID(),
		Email:         info.Email,
		EmailVerified: info.EmailVerified,
		PasswordHash:  nil,
		Locale:        locale,
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.repo.CreateUser(ctx, newUser); err != nil {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange create user: %w", err)
	}
	newProfile := &Profile{
		ID:        ids.NewUUID(),
		UserID:    newUser.ID,
		Name:      "Default",
		IsDefault: true,
		CreatedAt: now,
	}
	if err := s.repo.CreateProfile(ctx, newProfile); err != nil {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange create profile: %w", err)
	}
	newIdentity := &OAuthIdentity{
		ID:          ids.NewUUID(),
		UserID:      newUser.ID,
		Provider:    info.Provider,
		ProviderUID: info.Subject,
		CreatedAt:   now,
	}
	if err := s.repo.CreateOAuthIdentity(ctx, newIdentity); err != nil {
		return "", "", 0, "", "", false, fmt.Errorf("identity: oauth exchange create identity: %w", err)
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
// revoked (reuse/theft detection).  Session-store updates are best-effort:
// Redis downtime must not break the refresh path since Postgres is the source
// of truth.
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
		oldFamilyID      string
	)

	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		rt, err := txRepo.GetRefreshTokenForUpdate(ctx, jti)
		if err != nil {
			return platformerrors.Unauthenticated("invalid token")
		}

		if rt.Revoked {
			// Token reuse detected: revoke the whole family to protect the
			// legitimate session holder.  Return an error to abort the tx.
			if _, rErr := txRepo.RevokeFamily(ctx, rt.FamilyID); rErr != nil {
				s.log.Error("identity: refresh revoke family on reuse", zap.Error(rErr))
			}
			return platformerrors.Unauthenticated("token reuse detected")
		}

		if time.Now().After(rt.ExpiresAt) {
			return platformerrors.Unauthenticated("token expired")
		}

		oldFamilyID = rt.FamilyID

		if err := txRepo.RevokeRefreshToken(ctx, jti); err != nil {
			return fmt.Errorf("identity: refresh revoke old token: %w", err)
		}

		// Load the user for locale so the new access token is correctly localised.
		user, err := txRepo.GetUserByID(ctx, rt.UserID)
		if err != nil {
			return fmt.Errorf("identity: refresh load user: %w", err)
		}

		profileID := ""
		if rt.ProfileID != nil {
			profileID = *rt.ProfileID
		}

		newAccessToken, _, err = s.signer.SignAccess(rt.UserID, profileID, user.Locale, DefaultRoles)
		if err != nil {
			return fmt.Errorf("identity: refresh sign access: %w", err)
		}
		newExpiresIn = int64(s.accessTTL.Seconds())

		newRefreshString, newJTI, err = s.signer.SignRefresh(rt.UserID, profileID)
		if err != nil {
			return fmt.Errorf("identity: refresh sign refresh: %w", err)
		}

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
	if txErr != nil {
		return "", "", 0, txErr
	}

	// Best-effort session-store update; Redis downtime cannot fail the request.
	if sErr := s.sessions.Delete(ctx, jti); sErr != nil {
		s.log.Warn("identity: refresh delete old session", zap.Error(sErr), zap.String("jti", jti))
	}
	profileID := ""
	if claims.ProfileID != "" {
		profileID = claims.ProfileID
	}
	newSess := RefreshSession{
		UserID:    claims.Subject,
		ProfileID: profileID,
		FamilyID:  oldFamilyID,
		ExpiresAt: time.Now().Add(s.refreshTTL),
	}
	if sErr := s.sessions.Put(ctx, newJTI, newSess, s.refreshTTL); sErr != nil {
		s.log.Warn("identity: refresh put new session", zap.Error(sErr), zap.String("jti", newJTI))
	}

	return newAccessToken, newRefreshString, newExpiresIn, nil
}

// ─── Logout ──────────────────────────────────────────────────────────────────

// Logout revokes the token's family and clears session-store entries.
// The operation is best-effort and always returns success to the caller; we
// never leak token validity information via the logout response.
func (s *Service) Logout(ctx context.Context, refreshToken string) {
	claims, err := s.signer.Verify(refreshToken)
	if err != nil || claims.Kind != "refresh" {
		return
	}

	rt, err := s.repo.GetRefreshTokenForUpdate(ctx, claims.ID)
	if err != nil {
		return
	}

	jtis, err := s.repo.RevokeFamily(ctx, rt.FamilyID)
	if err != nil {
		s.log.Warn("identity: logout revoke family", zap.Error(err))
		return
	}

	if len(jtis) > 0 {
		if sErr := s.sessions.Delete(ctx, jtis...); sErr != nil {
			s.log.Warn("identity: logout delete sessions", zap.Error(sErr))
		}
	}
}

// ─── User info ───────────────────────────────────────────────────────────────

// GetMe loads a user and their profiles.
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
	if name == "" {
		return nil, platformerrors.InvalidArgument("profile name is required")
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

	token, _, err := s.signer.SignAccess(user.ID, profile.ID, user.Locale, DefaultRoles)
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

// VerifyEmail consumes a verification token and marks the user's email as verified.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	ev, err := s.repo.GetEmailVerification(ctx, token)
	if err != nil {
		return platformerrors.InvalidArgument("invalid or expired verification token")
	}
	if ev.ConsumedAt != nil {
		return platformerrors.InvalidArgument("verification token already used")
	}
	if time.Now().After(ev.ExpiresAt) {
		return platformerrors.InvalidArgument("verification token expired")
	}
	if err := s.repo.SetEmailVerified(ctx, ev.UserID); err != nil {
		return fmt.Errorf("identity: verify email set verified: %w", err)
	}
	if err := s.repo.ConsumeEmailVerification(ctx, token); err != nil {
		return fmt.Errorf("identity: verify email consume: %w", err)
	}
	return nil
}

// RequestPasswordReset creates a reset token and sends a reset email.
// The method always returns nil to avoid leaking whether the email exists.
func (s *Service) RequestPasswordReset(ctx context.Context, emailAddr string) error {
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
	if err := s.email.SendPasswordReset(ctx, emailAddr, token); err != nil {
		s.log.Error("identity: request password reset send email", zap.Error(err))
	}
	return nil
}

// ResetPassword validates a password-reset token and updates the user's password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	ev, err := s.repo.GetEmailVerification(ctx, token)
	if err != nil {
		return platformerrors.InvalidArgument("invalid or expired reset token")
	}
	if ev.Purpose != "password_reset" {
		return platformerrors.InvalidArgument("invalid token purpose")
	}
	if ev.ConsumedAt != nil {
		return platformerrors.InvalidArgument("reset token already used")
	}
	if time.Now().After(ev.ExpiresAt) {
		return platformerrors.InvalidArgument("reset token expired")
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("identity: reset password hash: %w", err)
	}
	if err := s.repo.UpdatePassword(ctx, ev.UserID, hash); err != nil {
		return fmt.Errorf("identity: reset password update: %w", err)
	}
	if err := s.repo.ConsumeEmailVerification(ctx, token); err != nil {
		return fmt.Errorf("identity: reset password consume: %w", err)
	}
	return nil
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

	accessToken, _, err = s.signer.SignAccess(user.ID, profile.ID, user.Locale, DefaultRoles)
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

// generateVerificationToken returns a 32-byte URL-safe opaque random string.
func generateVerificationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("identity: generate verification token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
