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
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// errTokenReuse carries reuse detection out of the Refresh tx; the family is revoked after it, as a
// revoke inside would roll back too.
var errTokenReuse = errors.New("token reuse detected")

const (
	statusActive = "active"

	refreshGrace = 10 * time.Second

	loginLimit       = 10
	loginWindow      = 15 * time.Minute
	resetLimit       = 3
	resetWindow      = time.Hour
	rateLimitKeyBase = "rl:"
)

// Service is the domain entry point for identity operations.
type Service struct {
	repo       Repository
	signer     *auth.Signer
	exchanger  OAuthExchanger
	email      EmailSender
	log        *zap.Logger
	accessTTL  time.Duration
	refreshTTL time.Duration

	limiter RateLimiter // nil disables rate limiting

	oauthStates    OAuthStateStore
	oauthRedirects []string

	// hashSlots bounds concurrent argon2 runs (64 MiB each) so a login flood cannot exhaust memory.
	hashSlots chan struct{}

	// syncEmail sends mail inline; by default it is dispatched off the request path.
	syncEmail bool
	bgMu      sync.Mutex
	bg        sync.WaitGroup
	bgActive  int
	bgClosed  bool

	// dummyHash is verified for unknown accounts so login time does not reveal whether an email exists.
	dummyOnce sync.Once
	dummyHash string
	dummyErr  error
}

// Option customises a Service.
type Option func(*Service)

// WithRateLimiter enables per-account rate limits on login and password reset requests.
func WithRateLimiter(l RateLimiter) Option { return func(s *Service) { s.limiter = l } }

// WithOAuth enables social sign-in; only exact matches of redirectURIs may start a flow.
func WithOAuth(states OAuthStateStore, redirectURIs []string) Option {
	return func(s *Service) { s.oauthStates, s.oauthRedirects = states, redirectURIs }
}

// WithSyncEmail sends email inside the request, for tests.
func WithSyncEmail() Option { return func(s *Service) { s.syncEmail = true } }

// WithHashConcurrency caps concurrent password hashes (default defaultHashSlots).
func WithHashConcurrency(n int) Option {
	return func(s *Service) { s.hashSlots = make(chan struct{}, max(n, 1)) }
}

// defaultHashSlots runs one hash per P, within half of GOMEMLIMIT (set from the pod's memory
// limit) so a CPU limit raised without memory can't let a login flood OOM the pod.
func defaultHashSlots(procs int, memLimit int64) int {
	return max(min(procs, int(memLimit/2/auth.PasswordHashBytes)), 1)
}

// NewService constructs a Service.  TTLs are pulled from the auth.Config so
// token lifetimes stay consistent between the signer and the DB rows.
func NewService(
	repo Repository,
	signer *auth.Signer,
	exchanger OAuthExchanger,
	email EmailSender,
	log *zap.Logger,
	authCfg auth.Config,
	opts ...Option,
) *Service {
	s := &Service{
		repo:       repo,
		signer:     signer,
		exchanger:  exchanger,
		email:      email,
		log:        log,
		accessTTL:  authCfg.AccessTokenTTL,
		refreshTTL: authCfg.RefreshTokenTTL,
		hashSlots:  make(chan struct{}, defaultHashSlots(runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1))),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ─── Registration ────────────────────────────────────────────────────────────

// Register sends the verification email outside the transaction and only logs a failure, so a
// broken email gateway neither holds a connection nor blocks sign-ups.
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
			TokenHash: HashToken(token),
			UserID:    user.ID,
			Purpose:   purposeVerifyEmail,
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

	// A send failure is logged, not fatal, so a broken gateway cannot block sign-ups.
	s.background(ctx, "verification email", func(ctx context.Context) error {
		return s.email.SendVerification(ctx, email, verificationToken, locale)
	})

	return user.ID, nil
}

// ─── Login ───────────────────────────────────────────────────────────────────

// Login authenticates a user by email/password and issues a token set.
func (s *Service) Login(ctx context.Context, email, password string) (TokenSet, error) {
	invalid := apperr.Unauthenticated("invalid credentials").WithMsgID(i18n.MsgInvalidCredentials)

	email, normErr := normalizeEmail(email)
	if normErr != nil || len(password) > maxPasswordBytes {
		return TokenSet{}, invalid
	}
	if err := s.allow(ctx, "login", email, loginLimit, loginWindow); err != nil {
		return TokenSet{}, err
	}

	user, lookupErr := s.repo.GetUserByEmail(ctx, email)
	if lookupErr != nil && !apperr.IsNotFound(lookupErr) {
		return TokenSet{}, fmt.Errorf("identity: login lookup user: %w", lookupErr)
	}

	// Unknown and OAuth-only accounts still pay for a hash so timing matches a wrong password.
	hash, hashErr := s.dummyPasswordHash()
	if hashErr != nil {
		return TokenSet{}, hashErr
	}
	known := lookupErr == nil && user.PasswordHash != nil
	if known {
		hash = *user.PasswordHash
	}
	ok, verifyErr := s.verifyPassword(ctx, password, hash)
	if verifyErr != nil {
		return TokenSet{}, fmt.Errorf("identity: login verify password: %w", verifyErr)
	}
	if !known || !ok {
		return TokenSet{}, invalid
	}
	if err := requireActive(user); err != nil {
		return TokenSet{}, err
	}
	// Successful sign-ins must not eat the budget meant for guessing, or an attacker could lock the owner out.
	s.resetLimit(ctx, "login", email)

	profile, err := s.pickDefaultProfile(ctx, user.ID)
	if err != nil {
		return TokenSet{}, err
	}

	return s.issueTokens(ctx, user, profile)
}

// ─── OAuth ───────────────────────────────────────────────────────────────────

const oauthStateTTL = 10 * time.Minute

// OAuthStart begins an authorization-code flow. The state, nonce and PKCE verifier stay on the server,
// so the callback can only be completed by a flow this service started.
func (s *Service) OAuthStart(ctx context.Context, provider, redirectURI string) (authURL, state string, err error) {
	if s.oauthStates == nil || len(s.oauthRedirects) == 0 {
		return "", "", apperr.FailedPrecondition("oauth is not configured")
	}
	if !slices.Contains(s.oauthRedirects, redirectURI) {
		return "", "", apperr.InvalidArgument("redirect_uri is not allowed")
	}
	state, err = generateVerificationToken()
	if err != nil {
		return "", "", err
	}
	nonce, err := generateVerificationToken()
	if err != nil {
		return "", "", err
	}
	flow := OAuthFlow{RedirectURI: redirectURI, Nonce: nonce, Verifier: oauth2.GenerateVerifier()}
	authURL, err = s.exchanger.AuthURL(ctx, provider, flow, state)
	if err != nil {
		return "", "", fmt.Errorf("identity: oauth start: %w", err)
	}
	if err := s.oauthStates.Put(ctx, state, OAuthState{Provider: provider, OAuthFlow: flow}, oauthStateTTL); err != nil {
		return "", "", err
	}
	return authURL, state, nil
}

// OAuthExchange completes a flow started by OAuthStart and either creates a new account or links/finds an existing one.
func (s *Service) OAuthExchange(ctx context.Context, provider, code, state, redirectURI string) (tokens TokenSet, isNew bool, err error) {
	if s.oauthStates == nil {
		return TokenSet{}, false, apperr.FailedPrecondition("oauth is not configured")
	}
	if state == "" || code == "" {
		return TokenSet{}, false, apperr.InvalidArgument("code and state are required")
	}
	st, found, err := s.oauthStates.Take(ctx, state)
	if err != nil {
		return TokenSet{}, false, err
	}
	if !found || st.Provider != provider || (redirectURI != "" && redirectURI != st.RedirectURI) {
		return TokenSet{}, false, apperr.Unauthenticated("invalid or expired oauth state")
	}
	info, err := s.exchanger.Exchange(ctx, provider, code, st.OAuthFlow)
	if err != nil {
		var appErr *apperr.AppError
		if errors.As(err, &appErr) {
			return TokenSet{}, false, err // already client-safe; wrapping would expose the cause in the status message
		}
		return TokenSet{}, false, fmt.Errorf("identity: oauth exchange: %w", err)
	}
	if info.Subject == "" {
		return TokenSet{}, false, apperr.Unauthenticated("oauth provider returned no subject")
	}
	info.Email = strings.TrimSpace(info.Email)

	// Try the fast path: identity already exists.
	oauthID, lookupErr := s.repo.GetOAuthIdentity(ctx, info.Provider, info.Subject)
	if lookupErr == nil {
		// Known provider+uid → load the user.
		user, err := s.repo.GetUserByID(ctx, oauthID.UserID)
		if err != nil {
			return TokenSet{}, false, fmt.Errorf("identity: oauth exchange load user: %w", err)
		}
		if err := requireActive(user); err != nil {
			return TokenSet{}, false, err
		}
		profile, err := s.pickDefaultProfile(ctx, user.ID)
		if err != nil {
			return TokenSet{}, false, err
		}
		tokens, err = s.issueTokens(ctx, user, profile)
		if err != nil {
			return TokenSet{}, false, err
		}
		return tokens, false, nil
	}

	if !apperr.IsNotFound(lookupErr) {
		return TokenSet{}, false, fmt.Errorf("identity: oauth exchange lookup identity: %w", lookupErr)
	}
	// An empty email would match any other account stored without one.
	if info.Email == "" {
		return TokenSet{}, false, apperr.FailedPrecondition("oauth provider returned no email")
	}
	// Linking or creating from an unproven address would let its holder squat or take over the account.
	if !info.EmailVerified {
		return TokenSet{}, false, apperr.FailedPrecondition("oauth email is not verified").
			WithMsgID(i18n.MsgOAuthEmailUnverified)
	}

	// New OAuth identity: try to link by email first.
	now := time.Now()
	existingUser, emailErr := s.repo.GetUserByEmail(ctx, info.Email)
	if emailErr != nil && !apperr.IsNotFound(emailErr) {
		return TokenSet{}, false, fmt.Errorf("identity: oauth exchange lookup email: %w", emailErr)
	}
	if emailErr == nil {
		// The provider proved the mailbox, so the account is now theirs: a password set before that
		// proof may belong to whoever registered the address first, so it is dropped along with its sessions.
		newIdentity := &OAuthIdentity{
			ID:          ids.NewUUID(),
			UserID:      existingUser.ID,
			Provider:    info.Provider,
			ProviderUID: info.Subject,
			CreatedAt:   now,
		}
		if txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
			cur, err := txRepo.GetUserForUpdate(ctx, existingUser.ID)
			if err != nil {
				return err
			}
			if err := requireActive(cur); err != nil {
				return err
			}
			if err := txRepo.CreateOAuthIdentity(ctx, newIdentity); err != nil {
				return err
			}
			if cur.PasswordHash != nil {
				if err := txRepo.ClearPassword(ctx, cur.ID); err != nil {
					return err
				}
				if err := txRepo.RevokeUserTokens(ctx, cur.ID); err != nil {
					return err
				}
			}
			if !cur.EmailVerified {
				if err := txRepo.SetEmailVerified(ctx, cur.ID); err != nil {
					return err
				}
			}
			return txRepo.InvalidateEmailVerifications(ctx, cur.ID)
		}); txErr != nil {
			return TokenSet{}, false, fmt.Errorf("identity: oauth exchange link identity: %w", txErr)
		}
		existingUser.PasswordHash = nil
		profile, err := s.pickDefaultProfile(ctx, existingUser.ID)
		if err != nil {
			return TokenSet{}, false, err
		}
		tokens, err = s.issueTokens(ctx, existingUser, profile)
		if err != nil {
			return TokenSet{}, false, err
		}
		return tokens, false, nil
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
		return TokenSet{}, false, txErr
	}

	tokens, err = s.issueTokens(ctx, newUser, newProfile)
	if err != nil {
		return TokenSet{}, false, err
	}
	return tokens, true, nil
}

// ─── Token rotation ──────────────────────────────────────────────────────────

// Refresh rotates the refresh token. Presenting a token that was rotated earlier revokes its whole family
// (theft detection), unless the rotation was within refreshGrace, which only mints a sibling token.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenSet, error) {
	claims, err := s.signer.Verify(refreshToken)
	if err != nil {
		return TokenSet{}, apperr.Unauthenticated("invalid token")
	}
	if claims.Kind != "refresh" {
		return TokenSet{}, apperr.Unauthenticated("invalid token")
	}
	jti := claims.ID

	var (
		newJTI string
		// Captured in the tx; the family is revoked after it returns, since an error would roll the revoke back.
		reuseFamilyID   string
		rotatedFamilyID string
		txUserID        string
		txProfileID     string
	)

	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		// Locked before the token row, matching reset and claim, so their revoke cannot miss this rotation.
		if _, err := txRepo.GetUserForShare(ctx, claims.Subject); err != nil {
			if apperr.IsNotFound(err) {
				return apperr.Unauthenticated("invalid token")
			}
			return fmt.Errorf("identity: refresh lock user: %w", err)
		}
		rt, err := txRepo.GetRefreshTokenForUpdate(ctx, jti)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return apperr.Unauthenticated("invalid token")
			}
			return fmt.Errorf("identity: refresh get token: %w", err)
		}

		if rt.UserID != claims.Subject {
			return apperr.Unauthenticated("invalid token")
		}

		// A token rotated moments ago is a concurrent refresh (two tabs), not theft: it mints a sibling.
		concurrent := rt.Revoked && rt.RotatedAgo != nil && *rt.RotatedAgo >= 0 && *rt.RotatedAgo <= refreshGrace
		if rt.Revoked && !concurrent {
			// The family is revoked after WithTx returns, since returning an error here rolls the tx back.
			reuseFamilyID = rt.FamilyID
			return errTokenReuse
		}

		if time.Now().After(rt.ExpiresAt) {
			return apperr.Unauthenticated("token expired")
		}

		rotatedFamilyID = rt.FamilyID
		txUserID = rt.UserID
		if rt.ProfileID != nil {
			txProfileID = *rt.ProfileID
		}

		if !rt.Revoked {
			if err := txRepo.RotateRefreshToken(ctx, jti); err != nil {
				return fmt.Errorf("identity: refresh revoke old token: %w", err)
			}
		}

		newJTI = ids.NewULID()

		now := time.Now()
		newRT := &RefreshToken{
			JTI:       newJTI,
			UserID:    rt.UserID,
			ProfileID: rt.ProfileID,
			FamilyID:  rt.FamilyID,
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
		s.revokeFamilyBestEffort(ctx, claims.Subject, reuseFamilyID)
		return TokenSet{}, apperr.Unauthenticated("token reuse detected")
	}
	if txErr != nil {
		return TokenSet{}, txErr
	}

	// Signed after commit so no row lock is held during crypto.
	userID := txUserID

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: refresh load user: %w", err)
	}
	if err := requireActive(user); err != nil {
		s.revokeFamilyBestEffort(ctx, userID, rotatedFamilyID)
		return TokenSet{}, err
	}

	tokens := TokenSet{UserID: userID, ProfileID: txProfileID, ExpiresIn: int64(s.accessTTL.Seconds())}
	tokens.AccessToken, _, err = s.signer.SignAccess(userID, txProfileID, user.Locale, DefaultRoles())
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: refresh sign access: %w", err)
	}
	tokens.RefreshToken, err = s.signer.SignRefreshWithID(userID, txProfileID, newJTI)
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: refresh sign refresh: %w", err)
	}
	return tokens, nil
}

// ─── Logout ──────────────────────────────────────────────────────────────────

// Logout revokes the token's family. It is best effort and never reports token validity to the caller.
func (s *Service) Logout(ctx context.Context, refreshToken string) {
	claims, err := s.signer.Verify(refreshToken)
	if err != nil || claims.Kind != "refresh" {
		return
	}

	txErr := s.repo.WithTx(ctx, func(txRepo Repository) error {
		// Exclusive, unlike Refresh: waits out any sibling mint so the family revoke sees it.
		if _, err := txRepo.GetUserForUpdate(ctx, claims.Subject); err != nil {
			return nil
		}
		rt, err := txRepo.GetRefreshTokenForUpdate(ctx, claims.ID)
		if err != nil {
			// Token not found or already gone — nothing to revoke; treat as success.
			return nil
		}
		if err := txRepo.RevokeFamily(ctx, rt.FamilyID); err != nil {
			return fmt.Errorf("identity: logout revoke family: %w", err)
		}
		return nil
	})
	if txErr != nil {
		s.log.Warn("identity: logout revoke family tx", zap.Error(txErr))
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

// MaxProfilesPerUser caps profiles per account, the default one included.
const MaxProfilesPerUser = 20

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
	err = s.repo.WithTx(ctx, func(tx Repository) error {
		// The user-row lock serialises concurrent creates so the count check holds.
		user, err := tx.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if err := requireActive(user); err != nil {
			return err
		}
		existing, err := tx.ListProfiles(ctx, userID)
		if err != nil {
			return err
		}
		if len(existing) >= MaxProfilesPerUser {
			return apperr.ResourceExhausted(fmt.Sprintf("at most %d profiles per account", MaxProfilesPerUser))
		}
		return tx.CreateProfile(ctx, p)
	})
	if err != nil {
		return nil, fmt.Errorf("identity: create profile: %w", err)
	}
	return p, nil
}

// ActivateProfile re-scopes only the access token; the refresh token keeps its profile, so a
// refresh reverts it until the client logs in again.
func (s *Service) ActivateProfile(ctx context.Context, userID, profileID string) (accessToken string, expiresIn int64, err error) {
	profile, err := s.repo.GetProfile(ctx, profileID)
	if err != nil {
		return "", 0, err // NotFound propagates
	}
	if profile.UserID != userID {
		return "", 0, apperr.PermissionDenied("profile does not belong to this user")
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

// ─── Email verification & password reset ─────────────────────────────────────

// VerifyEmail consumes a verification token and marks the user's email as verified.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	return s.repo.WithTx(ctx, func(txRepo Repository) error {
		userID, err := consumeToken(ctx, txRepo, token, purposeVerifyEmail)
		if err != nil {
			return err
		}
		if err := txRepo.SetEmailVerified(ctx, userID); err != nil {
			return fmt.Errorf("identity: verify email set verified: %w", err)
		}
		return nil
	})
}

// consumeToken locks the token's user before the token row; every flow takes user then token to avoid deadlocks.
func consumeToken(ctx context.Context, repo Repository, token, purpose string) (string, error) {
	hash := HashToken(token)
	userID, err := repo.EmailVerificationUser(ctx, hash, purpose)
	if err == nil {
		_, err = repo.GetUserForUpdate(ctx, userID)
	}
	if err == nil {
		userID, err = repo.ConsumeEmailVerification(ctx, hash, purpose)
	}
	if err != nil {
		if apperr.IsNotFound(err) {
			return "", apperr.InvalidArgument("invalid or expired token").WithMsgID(i18n.MsgInvalidOrExpiredToken)
		}
		return "", fmt.Errorf("identity: consume email token: %w", err)
	}
	return userID, nil
}

// RequestPasswordReset always returns nil; the lookup, insert and send all run off the request path
// so neither the response nor its timing reveals whether the email is registered.
func (s *Service) RequestPasswordReset(ctx context.Context, emailAddr string) error {
	emailAddr, err := normalizeEmail(emailAddr)
	if err != nil {
		return nil
	}
	if err := s.allow(ctx, "reset", emailAddr, resetLimit, resetWindow); err != nil {
		return nil
	}
	s.background(ctx, "password reset email", func(ctx context.Context) error {
		user, err := s.repo.GetUserByEmail(ctx, emailAddr)
		if err != nil {
			if apperr.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("lookup user: %w", err)
		}
		token, err := generateVerificationToken()
		if err != nil {
			return err
		}
		now := time.Now()
		ev := &EmailVerification{
			TokenHash: HashToken(token),
			UserID:    user.ID,
			Purpose:   purposePasswordReset,
			ExpiresAt: now.Add(time.Hour),
			CreatedAt: now,
		}
		if err := s.repo.CreateEmailVerification(ctx, ev); err != nil {
			return fmt.Errorf("create token: %w", err)
		}
		return s.email.SendPasswordReset(ctx, emailAddr, token, user.Locale)
	})
	return nil
}

// ResetPassword sets a new password from an emailed token and ends every session of the account.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.hashPassword(ctx, newPassword)
	if err != nil {
		return fmt.Errorf("identity: reset password hash: %w", err)
	}

	return s.repo.WithTx(ctx, func(txRepo Repository) error {
		userID, err := consumeToken(ctx, txRepo, token, purposePasswordReset)
		if err != nil {
			return err
		}
		if err := txRepo.UpdatePassword(ctx, userID, hash); err != nil {
			return fmt.Errorf("identity: reset password update: %w", err)
		}
		// Receiving the link proves the mailbox.
		if err := txRepo.SetEmailVerified(ctx, userID); err != nil {
			return fmt.Errorf("identity: reset password verify email: %w", err)
		}
		if err := txRepo.InvalidateEmailVerifications(ctx, userID); err != nil {
			return fmt.Errorf("identity: reset password invalidate tokens: %w", err)
		}
		if err := txRepo.RevokeUserTokens(ctx, userID); err != nil {
			return fmt.Errorf("identity: reset password revoke sessions: %w", err)
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

// issueTokens starts a new token family and returns the signed pair.
func (s *Service) issueTokens(ctx context.Context, user *User, profile *Profile) (TokenSet, error) {
	familyID := ids.NewULID()
	tokens := TokenSet{UserID: user.ID, ProfileID: profile.ID, ExpiresIn: int64(s.accessTTL.Seconds())}

	access, _, err := s.signer.SignAccess(user.ID, profile.ID, user.Locale, DefaultRoles())
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: issue tokens sign access: %w", err)
	}

	refresh, jti, err := s.signer.SignRefresh(user.ID, profile.ID)
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: issue tokens sign refresh: %w", err)
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
	// The share lock orders this insert against a reset or claim: either it is revoked by them, or it sees their result.
	err = s.repo.WithTx(ctx, func(txRepo Repository) error {
		cur, err := txRepo.GetUserForShare(ctx, user.ID)
		if err != nil {
			return err
		}
		if err := requireActive(cur); err != nil {
			return err
		}
		if !samePassword(cur.PasswordHash, user.PasswordHash) {
			return apperr.Unauthenticated("invalid credentials").WithMsgID(i18n.MsgInvalidCredentials)
		}
		return txRepo.CreateRefreshToken(ctx, rt)
	})
	if err != nil {
		return TokenSet{}, fmt.Errorf("identity: issue tokens create refresh token: %w", err)
	}

	tokens.AccessToken, tokens.RefreshToken = access, refresh
	return tokens, nil
}

func samePassword(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// pickDefaultProfile falls back to the first profile: the schema allows a user with no default.
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
	return nil, apperr.Internal("user has no profiles")
}

func requireActive(u *User) error {
	if u.Status != statusActive {
		return apperr.PermissionDenied("account is not active")
	}
	return nil
}

// revokeFamilyBestEffort holds the user lock so a concurrent sibling mint cannot slip past the revoke.
func (s *Service) revokeFamilyBestEffort(ctx context.Context, userID, familyID string) {
	err := s.repo.WithTx(ctx, func(tx Repository) error {
		if _, err := tx.GetUserForUpdate(ctx, userID); err != nil {
			return err
		}
		return tx.RevokeFamily(ctx, familyID)
	})
	if err != nil {
		s.log.Error("identity: revoke family", zap.Error(err))
	}
}

// HashToken is how email tokens are stored: a database leak must not yield usable links.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const (
	backgroundTimeout = 30 * time.Second
	maxBackground     = 1024
)

// background runs fn off the request path so response time does not reveal account state.
// Overload drops the job with an error log; mail is best effort (reset covers a lost verification).
func (s *Service) background(ctx context.Context, what string, fn func(context.Context) error) {
	run := func(ctx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("identity: panic in "+what, zap.Any("panic", r))
			}
		}()
		if err := fn(ctx); err != nil {
			s.log.Error("identity: "+what, zap.Error(err))
		}
	}
	if s.syncEmail {
		run(ctx)
		return
	}
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.bgClosed || s.bgActive >= maxBackground {
		s.log.Error("identity: dropped "+what, zap.Bool("shutting_down", s.bgClosed))
		return
	}
	s.bgActive++
	s.bg.Add(1)
	bg := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			s.bgMu.Lock()
			s.bgActive--
			s.bgMu.Unlock()
			s.bg.Done()
		}()
		bg, cancel := context.WithTimeout(bg, backgroundTimeout)
		defer cancel()
		run(bg)
	}()
}

// Drain stops accepting background jobs and waits for running ones until ctx ends.
func (s *Service) Drain(ctx context.Context) {
	s.bgMu.Lock()
	s.bgClosed = true
	s.bgMu.Unlock()
	done := make(chan struct{})
	go func() { s.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn("identity: drain timed out with background jobs running")
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
		return apperr.ResourceExhausted("too many attempts").WithMsgID(i18n.MsgTooManyAttempts)
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
		return nil, apperr.ResourceExhausted("server busy", err)
	}
	select {
	case s.hashSlots <- struct{}{}:
		return func() { <-s.hashSlots }, nil
	case <-ctx.Done():
		return nil, apperr.ResourceExhausted("server busy", ctx.Err())
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
