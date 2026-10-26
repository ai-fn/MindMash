package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/auth"

	. "github.com/mindmesh/mindmesh/internal/identity"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

// fakeRepo is an in-memory Repository implementation used exclusively in unit
// tests.  It is not safe for concurrent use from multiple goroutines.
type fakeRepo struct {
	mu            sync.Mutex
	users         map[string]*User              // keyed by ID
	usersByEmail  map[string]*User              // keyed by email
	oauthIDs      map[string]*OAuthIdentity     // keyed by "provider:uid"
	profiles      map[string]*Profile           // keyed by ID
	refreshTokens map[string]*RefreshToken      // keyed by JTI
	verifications map[string]*EmailVerification // keyed by token
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:         make(map[string]*User),
		usersByEmail:  make(map[string]*User),
		oauthIDs:      make(map[string]*OAuthIdentity),
		profiles:      make(map[string]*Profile),
		refreshTokens: make(map[string]*RefreshToken),
		verifications: make(map[string]*EmailVerification),
	}
}

func (r *fakeRepo) CreateUser(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.usersByEmail[u.Email]; exists {
		return apperr.AlreadyExists("user with this email already exists")
	}
	cp := *u
	r.users[u.ID] = &cp
	r.usersByEmail[u.Email] = &cp
	return nil
}

func (r *fakeRepo) GetUserByID(_ context.Context, id string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, apperr.NotFound("user not found")
	}
	cp := *u
	return &cp, nil
}

func (r *fakeRepo) GetUserForUpdate(ctx context.Context, id string) (*User, error) {
	return r.GetUserByID(ctx, id)
}

func (r *fakeRepo) GetUserForShare(ctx context.Context, id string) (*User, error) {
	return r.GetUserByID(ctx, id)
}

func (r *fakeRepo) GetUserByEmail(_ context.Context, email string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.usersByEmail[email]
	if !ok {
		return nil, apperr.NotFound("user not found")
	}
	cp := *u
	return &cp, nil
}

func (r *fakeRepo) SetEmailVerified(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return apperr.NotFound("user not found")
	}
	u.EmailVerified = true
	r.usersByEmail[u.Email].EmailVerified = true
	return nil
}

func (r *fakeRepo) UpdatePassword(_ context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return apperr.NotFound("user not found")
	}
	u.PasswordHash = &hash
	r.usersByEmail[u.Email].PasswordHash = &hash
	return nil
}

func (r *fakeRepo) CreateOAuthIdentity(_ context.Context, o *OAuthIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := o.Provider + ":" + o.ProviderUID
	if _, exists := r.oauthIDs[key]; exists {
		return apperr.AlreadyExists("oauth identity already exists")
	}
	cp := *o
	r.oauthIDs[key] = &cp
	return nil
}

func (r *fakeRepo) GetOAuthIdentity(_ context.Context, provider, uid string) (*OAuthIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := provider + ":" + uid
	o, ok := r.oauthIDs[key]
	if !ok {
		return nil, apperr.NotFound("oauth identity not found")
	}
	cp := *o
	return &cp, nil
}

func (r *fakeRepo) CreateProfile(_ context.Context, p *Profile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *p
	r.profiles[p.ID] = &cp
	return nil
}

func (r *fakeRepo) ListProfiles(_ context.Context, userID string) ([]Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Profile
	for _, p := range r.profiles {
		if p.UserID == userID {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetProfile(_ context.Context, id string) (*Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.profiles[id]
	if !ok {
		return nil, apperr.NotFound("profile not found")
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) CreateRefreshToken(_ context.Context, rt *RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rt
	r.refreshTokens[rt.JTI] = &cp
	return nil
}

func (r *fakeRepo) GetRefreshTokenForUpdate(_ context.Context, jti string) (*RefreshToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.refreshTokens[jti]
	if !ok {
		return nil, apperr.NotFound("refresh token not found")
	}
	cp := *rt
	return &cp, nil
}

var zero time.Duration

func (r *fakeRepo) RotateRefreshToken(_ context.Context, jti string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.refreshTokens[jti]
	if !ok {
		return apperr.NotFound("refresh token not found")
	}
	rt.Revoked = true
	rt.RotatedAgo = &zero
	return nil
}

func (r *fakeRepo) RevokeFamily(_ context.Context, familyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.refreshTokens {
		if rt.FamilyID == familyID {
			rt.Revoked = true
			rt.RotatedAgo = nil
		}
	}
	return nil
}

func (r *fakeRepo) CreateEmailVerification(_ context.Context, ev *EmailVerification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *ev
	r.verifications[ev.TokenHash] = &cp
	return nil
}

func (r *fakeRepo) ClearPassword(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return apperr.NotFound("user not found")
	}
	u.PasswordHash = nil
	return nil
}

func (r *fakeRepo) RevokeUserTokens(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.refreshTokens {
		if rt.UserID == userID {
			rt.Revoked = true
			rt.RotatedAgo = nil
		}
	}
	return nil
}

func (r *fakeRepo) InvalidateEmailVerifications(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, ev := range r.verifications {
		if ev.UserID == userID && ev.ConsumedAt == nil {
			ev.ConsumedAt = &now
		}
	}
	return nil
}

func (r *fakeRepo) EmailVerificationUser(_ context.Context, tokenHash, purpose string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev, ok := r.verifications[tokenHash]
	if !ok || ev.Purpose != purpose || ev.ConsumedAt != nil || time.Now().After(ev.ExpiresAt) {
		return "", apperr.NotFound("invalid or expired token")
	}
	return ev.UserID, nil
}

func (r *fakeRepo) ConsumeEmailVerification(_ context.Context, tokenHash, purpose string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev, ok := r.verifications[tokenHash]
	if !ok || ev.Purpose != purpose || ev.ConsumedAt != nil || time.Now().After(ev.ExpiresAt) {
		return "", apperr.NotFound("invalid or expired token")
	}
	now := time.Now()
	ev.ConsumedAt = &now
	return ev.UserID, nil
}

// WithTx restores a snapshot of the maps when fn fails, like a rollback.
func (r *fakeRepo) WithTx(ctx context.Context, fn func(Repository) error) error {
	r.mu.Lock()
	// Deep-copy every map so we can roll back on error.
	snapUsers := make(map[string]*User, len(r.users))
	for k, v := range r.users {
		cp := *v
		snapUsers[k] = &cp
	}
	snapByEmail := make(map[string]*User, len(r.usersByEmail))
	for k, v := range r.usersByEmail {
		cp := *v
		snapByEmail[k] = &cp
	}
	snapOAuth := make(map[string]*OAuthIdentity, len(r.oauthIDs))
	for k, v := range r.oauthIDs {
		cp := *v
		snapOAuth[k] = &cp
	}
	snapProfiles := make(map[string]*Profile, len(r.profiles))
	for k, v := range r.profiles {
		cp := *v
		snapProfiles[k] = &cp
	}
	snapTokens := make(map[string]*RefreshToken, len(r.refreshTokens))
	for k, v := range r.refreshTokens {
		cp := *v
		snapTokens[k] = &cp
	}
	snapVerifications := make(map[string]*EmailVerification, len(r.verifications))
	for k, v := range r.verifications {
		cp := *v
		snapVerifications[k] = &cp
	}
	r.mu.Unlock()

	if err := fn(r); err != nil {
		// Roll back: restore every map to its pre-fn state.
		r.mu.Lock()
		r.users = snapUsers
		r.usersByEmail = snapByEmail
		r.oauthIDs = snapOAuth
		r.profiles = snapProfiles
		r.refreshTokens = snapTokens
		r.verifications = snapVerifications
		r.mu.Unlock()
		return err
	}
	return nil
}

// ─── fakeEmailSender ─────────────────────────────────────────────────────────

type fakeEmailSender struct {
	mu            sync.Mutex
	verifications []string // tokens sent via SendVerification
	resets        []string // tokens sent via SendPasswordReset
	lastLocale    string   // locale of the most recent send
}

func (e *fakeEmailSender) SendVerification(_ context.Context, _, token, locale string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verifications = append(e.verifications, token)
	e.lastLocale = locale
	return nil
}

func (e *fakeEmailSender) SendPasswordReset(_ context.Context, _, token, locale string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resets = append(e.resets, token)
	e.lastLocale = locale
	return nil
}

// ─── fakeOAuthExchanger ──────────────────────────────────────────────────────

type fakeOAuthExchanger struct {
	// code → OAuthUserInfo to return
	responses map[string]OAuthUserInfo
	err       error
	flows     map[string]OAuthFlow // state → flow given to AuthURL
	lastFlow  OAuthFlow            // flow given to the latest Exchange
}

func (f *fakeOAuthExchanger) AuthURL(_ context.Context, _ string, flow OAuthFlow, state string) (string, error) {
	if f.flows == nil {
		f.flows = map[string]OAuthFlow{}
	}
	f.flows[state] = flow
	return "https://idp.example/auth?state=" + state, nil
}

func (f *fakeOAuthExchanger) Exchange(_ context.Context, _, code string, flow OAuthFlow) (OAuthUserInfo, error) {
	f.lastFlow = flow
	if f.err != nil {
		return OAuthUserInfo{}, f.err
	}
	info, ok := f.responses[code]
	if !ok {
		return OAuthUserInfo{}, errors.New("fakeOAuthExchanger: unknown code")
	}
	return info, nil
}

type fakeStateStore struct {
	mu   sync.Mutex
	data map[string]OAuthState
}

func (f *fakeStateStore) Put(_ context.Context, state string, st OAuthState, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]OAuthState{}
	}
	f.data[state] = st
	return nil
}

func (f *fakeStateStore) Take(_ context.Context, state string) (OAuthState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.data[state]
	delete(f.data, state)
	return st, ok, nil
}

const testRedirect = "https://app/cb"

var testOAuth = func() Option { return WithOAuth(&fakeStateStore{}, []string{testRedirect}) }

// oauthLogin runs a full start + exchange, as the SPA would.
func oauthLogin(ctx context.Context, svc *Service, provider, code string) (TokenSet, bool, error) {
	_, state, err := svc.OAuthStart(ctx, provider, testRedirect)
	if err != nil {
		return TokenSet{}, false, err
	}
	return svc.OAuthExchange(ctx, provider, code, state, testRedirect)
}

// ─── Test fixtures ───────────────────────────────────────────────────────────

func newTestSigner(t *testing.T) *auth.Signer {
	t.Helper()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	cfg := auth.Config{
		Issuer:          "test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	return auth.NewSignerFromKeys(privKey, pubKey, cfg)
}

func newTestService(t *testing.T, opts ...Option) (*Service, *fakeRepo, *fakeEmailSender, *fakeOAuthExchanger, *auth.Signer) {
	t.Helper()
	signer := newTestSigner(t)
	repo := newFakeRepo()
	emailer := &fakeEmailSender{}
	exchanger := &fakeOAuthExchanger{responses: make(map[string]OAuthUserInfo)}
	log := zap.NewNop()
	cfg := auth.Config{
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	svc := NewService(repo, signer, exchanger, emailer, log, cfg, append([]Option{WithSyncEmail(), testOAuth()}, opts...)...)
	return svc, repo, emailer, exchanger, signer
}

func grpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	var ae *apperr.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return codes.Unknown
}

// ─── Register tests ──────────────────────────────────────────────────────────

func TestRegister(t *testing.T) {
	t.Run("hashes password; stored hash verifies; default profile created; verification token sent", func(t *testing.T) {
		svc, repo, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		userID, err := svc.Register(ctx, "alice@example.com", "s3cret-pw", "en")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if userID == "" {
			t.Fatal("expected non-empty userID")
		}

		// Password must be stored as a hash, not plaintext.
		user, err := repo.GetUserByEmail(ctx, "alice@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		if user.PasswordHash == nil {
			t.Fatal("expected password hash to be set")
		}
		if *user.PasswordHash == "s3cret-pw" {
			t.Fatal("password must not be stored in plaintext")
		}
		ok, err := auth.VerifyPassword("s3cret-pw", *user.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword: %v", err)
		}
		if !ok {
			t.Fatal("stored hash does not verify against original password")
		}

		// A default profile must have been created.
		profiles, err := repo.ListProfiles(ctx, userID)
		if err != nil {
			t.Fatalf("ListProfiles: %v", err)
		}
		if len(profiles) != 1 {
			t.Fatalf("expected 1 profile, got %d", len(profiles))
		}
		if !profiles[0].IsDefault {
			t.Fatal("expected default profile")
		}

		// A verification email must have been sent.
		emailer.mu.Lock()
		verCount := len(emailer.verifications)
		emailer.mu.Unlock()
		if verCount != 1 {
			t.Fatalf("expected 1 verification email, got %d", verCount)
		}
	})

	t.Run("duplicate email returns AlreadyExists", func(t *testing.T) {
		svc, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "bob@example.com", "password1", ""); err != nil {
			t.Fatalf("first Register: %v", err)
		}
		_, err := svc.Register(ctx, "bob@example.com", "password2", "")
		if err == nil {
			t.Fatal("expected error for duplicate email")
		}
		if grpcCode(err) != codes.AlreadyExists {
			t.Fatalf("expected AlreadyExists, got %v", grpcCode(err))
		}
	})

	t.Run("empty email or password returns InvalidArgument", func(t *testing.T) {
		svc, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		for _, tc := range []struct{ email, pass string }{
			{"", "password1"},
			{"user@x.com", ""},
		} {
			_, err := svc.Register(ctx, tc.email, tc.pass, "")
			if err == nil {
				t.Fatalf("expected error for email=%q pass=%q", tc.email, tc.pass)
			}
			if grpcCode(err) != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v", grpcCode(err))
			}
		}
	})
}

// ─── Login tests ─────────────────────────────────────────────────────────────

func TestLogin(t *testing.T) {
	setup := func(t *testing.T) (*Service, *auth.Signer, context.Context) {
		t.Helper()
		svc, _, _, _, signer := newTestService(t)
		ctx := context.Background()
		if _, err := svc.Register(ctx, "carol@example.com", "hunter2-pw", "fr"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		return svc, signer, ctx
	}

	t.Run("success returns valid tokens and expiresIn", func(t *testing.T) {
		svc, signer, ctx := setup(t)

		tok, err := svc.Login(ctx, "carol@example.com", "hunter2-pw")
		access, refresh, expiresIn, userID, profileID := tok.AccessToken, tok.RefreshToken, tok.ExpiresIn, tok.UserID, tok.ProfileID
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		if userID == "" || profileID == "" {
			t.Fatal("expected non-empty userID and profileID")
		}
		if expiresIn != int64((15 * time.Minute).Seconds()) {
			t.Fatalf("unexpected expiresIn %d", expiresIn)
		}

		// Access token must be verifiable.
		accessClaims, err := signer.Verify(access)
		if err != nil {
			t.Fatalf("verify access token: %v", err)
		}
		if accessClaims.Kind != "access" {
			t.Fatalf("expected access kind, got %q", accessClaims.Kind)
		}

		// Refresh token must be verifiable.
		refreshClaims, err := signer.Verify(refresh)
		if err != nil {
			t.Fatalf("verify refresh token: %v", err)
		}
		if refreshClaims.Kind != "refresh" {
			t.Fatalf("expected refresh kind, got %q", refreshClaims.Kind)
		}
	})

	t.Run("wrong password returns Unauthenticated", func(t *testing.T) {
		svc, _, ctx := setup(t)
		_, err := svc.Login(ctx, "carol@example.com", "wrong")
		if err == nil {
			t.Fatal("expected error")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})

	t.Run("unknown email returns Unauthenticated (not NotFound — no enumeration)", func(t *testing.T) {
		svc, _, ctx := setup(t)
		_, err := svc.Login(ctx, "nobody@example.com", "password1")
		if err == nil {
			t.Fatal("expected error")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})

	t.Run("OAuth-only user (nil hash) returns Unauthenticated", func(t *testing.T) {
		svc, repo, _, _, _ := newTestService(t)
		ctx := context.Background()

		// Insert an OAuth-only user directly.
		oauthUser := &User{
			ID:           "oauth-user-1",
			Email:        "oauth@example.com",
			PasswordHash: nil,
			Locale:       "en",
			Status:       "active",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		if err := repo.CreateUser(ctx, oauthUser); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		profile := &Profile{
			ID: "profile-1", UserID: "oauth-user-1",
			Name: "Default", IsDefault: true, CreatedAt: time.Now(),
		}
		if err := repo.CreateProfile(ctx, profile); err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}

		_, err := svc.Login(ctx, "oauth@example.com", "any")
		if err == nil {
			t.Fatal("expected error")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})
}

// ─── Refresh / rotation tests ─────────────────────────────────────────────────

func TestRefresh(t *testing.T) {
	setup := func(t *testing.T) (*Service, *auth.Signer, context.Context, string /*refresh*/) {
		t.Helper()
		svc, _, _, _, signer := newTestService(t)
		ctx := context.Background()
		if _, err := svc.Register(ctx, "dave@example.com", "password1", "de"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		tok, err := svc.Login(ctx, "dave@example.com", "password1")
		refresh := tok.RefreshToken
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		return svc, signer, ctx, refresh
	}

	t.Run("happy path rotates tokens; old JTI revoked; new JTI issued; same family", func(t *testing.T) {
		svc, signer, ctx, oldRefresh := setup(t)

		oldClaims, err := signer.Verify(oldRefresh)
		if err != nil {
			t.Fatalf("verify old refresh: %v", err)
		}
		oldJTI := oldClaims.ID

		tok2, err := svc.Refresh(ctx, oldRefresh)
		newAccess, newRefresh, expiresIn := tok2.AccessToken, tok2.RefreshToken, tok2.ExpiresIn
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if newAccess == "" || newRefresh == "" {
			t.Fatal("expected non-empty tokens")
		}
		if expiresIn != int64((15 * time.Minute).Seconds()) {
			t.Fatalf("unexpected expiresIn %d", expiresIn)
		}
		if tok2.UserID != oldClaims.Subject || tok2.ProfileID != oldClaims.ProfileID || tok2.ProfileID == "" {
			t.Fatalf("tokens for %s/%s, want %s/%s", tok2.UserID, tok2.ProfileID, oldClaims.Subject, oldClaims.ProfileID)
		}

		newClaims, err := signer.Verify(newRefresh)
		if err != nil {
			t.Fatalf("verify new refresh: %v", err)
		}
		if newClaims.ID == oldJTI {
			t.Fatal("new JTI must differ from old JTI")
		}
	})

	t.Run("reuse detection: presenting an already-revoked JTI revokes the family and returns Unauthenticated", func(t *testing.T) {
		svc, repo, ctx, oldRefresh := registerAndLogin(t, "reuse@example.com")

		// Rotate once legitimately — oldRefresh → tokenB.
		tok3, err := svc.Refresh(ctx, oldRefresh)
		tokenB := tok3.RefreshToken
		if err != nil {
			t.Fatalf("first Refresh: %v", err)
		}
		// Rotate again with tokenB so the family has >1 member — tokenB → tokenC.
		tok4, err := svc.Refresh(ctx, tokenB)
		tokenC := tok4.RefreshToken
		if err != nil {
			t.Fatalf("second Refresh: %v", err)
		}
		_ = tokenC

		// Replayed after the concurrent-refresh grace window, the original token is theft: revoke the family.
		ageRotations(repo, time.Minute)
		_, err = svc.Refresh(ctx, oldRefresh)
		if err == nil {
			t.Fatal("expected error on revoked-token reuse")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}

		// GAP-2: the currently-valid token (tokenC) must also now be rejected because
		// the family was fully revoked when the replay was detected.
		_, err = svc.Refresh(ctx, tokenC)
		if err == nil {
			t.Fatal("expected tokenC to be rejected after family revocation")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated for tokenC after family revocation, got %v", grpcCode(err))
		}
	})

	t.Run("invalid/garbage token returns Unauthenticated", func(t *testing.T) {
		svc, _, ctx, _ := setup(t)
		_, err := svc.Refresh(ctx, "not.a.jwt")
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})
}

// ─── OAuthExchange tests ──────────────────────────────────────────────────────

func TestOAuthExchange(t *testing.T) {
	t.Run("new user isNewUser=true; user + profile + identity created", func(t *testing.T) {
		svc, repo, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		exchanger.responses["code1"] = OAuthUserInfo{
			Provider: "google", Subject: "g-sub-1",
			Email: "eve@example.com", EmailVerified: true,
		}

		tok, isNew, err := oauthLogin(ctx, svc, "google", "code1")
		userID := tok.UserID
		if err != nil {
			t.Fatalf("OAuthExchange: %v", err)
		}
		if !isNew {
			t.Fatal("expected isNewUser=true")
		}
		if userID == "" {
			t.Fatal("expected non-empty userID")
		}

		// Verify identity was persisted.
		oauthID, err := repo.GetOAuthIdentity(ctx, "google", "g-sub-1")
		if err != nil {
			t.Fatalf("GetOAuthIdentity: %v", err)
		}
		if oauthID.UserID != userID {
			t.Fatalf("oauth identity user mismatch: %s vs %s", oauthID.UserID, userID)
		}
		if u, err := repo.GetUserByID(ctx, userID); err != nil || !u.EmailVerified {
			t.Fatalf("want the provider-verified email stored as verified, got %+v, %v", u, err)
		}
	})

	t.Run("existing identity isNewUser=false; returns same userID", func(t *testing.T) {
		svc, _, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		exchanger.responses["code2"] = OAuthUserInfo{
			Provider: "google", Subject: "g-sub-2",
			Email: "frank@example.com", EmailVerified: true,
		}

		tok2, _, err := oauthLogin(ctx, svc, "google", "code2")
		userID1 := tok2.UserID
		if err != nil {
			t.Fatalf("first OAuthExchange: %v", err)
		}

		tok3, isNew, err := oauthLogin(ctx, svc, "google", "code2")
		userID2 := tok3.UserID
		if err != nil {
			t.Fatalf("second OAuthExchange: %v", err)
		}
		if isNew {
			t.Fatal("expected isNewUser=false on second exchange")
		}
		if userID1 != userID2 {
			t.Fatalf("userID changed across exchanges: %s vs %s", userID1, userID2)
		}
	})

	t.Run("link-by-email: existing user without identity gets linked; isNewUser=false", func(t *testing.T) {
		svc, repo, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		// Create a password user with a verified email first.
		userID, err := svc.Register(ctx, "grace@example.com", "password1", "")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := repo.SetEmailVerified(ctx, userID); err != nil {
			t.Fatalf("SetEmailVerified: %v", err)
		}

		exchanger.responses["code3"] = OAuthUserInfo{
			Provider: "google", Subject: "g-sub-3",
			Email: "grace@example.com", EmailVerified: true,
		}

		tok4, isNew, err := oauthLogin(ctx, svc, "google", "code3")
		returnedUserID := tok4.UserID
		if err != nil {
			t.Fatalf("OAuthExchange: %v", err)
		}
		if isNew {
			t.Fatal("expected isNewUser=false when linking to existing account")
		}
		if returnedUserID != userID {
			t.Fatalf("expected same userID %s, got %s", userID, returnedUserID)
		}

		// OAuth identity should now be linked.
		oauthID, err := repo.GetOAuthIdentity(ctx, "google", "g-sub-3")
		if err != nil {
			t.Fatalf("GetOAuthIdentity after link: %v", err)
		}
		if oauthID.UserID != userID {
			t.Fatal("oauth identity linked to wrong user")
		}
	})
}

func TestOAuthExchange_RefusesUnverifiedEmails(t *testing.T) {
	tests := []struct {
		name             string
		existing         bool
		accountVerified  bool
		providerVerified bool
		want             codes.Code
	}{
		{"link: provider email unverified", true, true, false, codes.FailedPrecondition},
		{"link: both unverified", true, false, false, codes.FailedPrecondition},
		{"create: provider email unverified would squat the address", false, false, false, codes.FailedPrecondition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _, exchanger, _ := newTestService(t)
			ctx := context.Background()
			if tc.existing {
				userID, err := svc.Register(ctx, "ivy@example.com", "password1", "")
				if err != nil {
					t.Fatalf("Register: %v", err)
				}
				if tc.accountVerified {
					if err := repo.SetEmailVerified(ctx, userID); err != nil {
						t.Fatalf("SetEmailVerified: %v", err)
					}
				}
			}
			exchanger.responses["c"] = OAuthUserInfo{
				Provider: "google", Subject: "g-attacker", Email: "ivy@example.com", EmailVerified: tc.providerVerified,
			}

			_, _, err := oauthLogin(ctx, svc, "google", "c")
			if got := grpcCode(err); got != tc.want {
				t.Fatalf("want %v, got %v (%v)", tc.want, got, err)
			}
			if _, err := repo.GetOAuthIdentity(ctx, "google", "g-attacker"); !apperr.IsNotFound(err) {
				t.Fatalf("want no identity stored, got %v", err)
			}
			if _, err := repo.GetUserByEmail(ctx, "ivy@example.com"); tc.existing == apperr.IsNotFound(err) {
				t.Fatalf("want users unchanged (existing=%v), got %v", tc.existing, err)
			}
		})
	}
}

// failingEmailRepo makes the email lookup fail with a non-NotFound error.
type failingEmailRepo struct{ *fakeRepo }

func (failingEmailRepo) GetUserByEmail(context.Context, string) (*User, error) {
	return nil, errors.New("db down")
}

func TestOAuthExchange_LookupErrorCreatesNothing(t *testing.T) {
	repo := failingEmailRepo{newFakeRepo()}
	exchanger := &fakeOAuthExchanger{responses: map[string]OAuthUserInfo{
		"c": {Provider: "google", Subject: "g-1", Email: "kim@example.com", EmailVerified: true},
	}}
	svc := NewService(repo, newTestSigner(t), exchanger, &fakeEmailSender{}, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}, testOAuth())

	if _, _, err := oauthLogin(context.Background(), svc, "google", "c"); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("want the lookup error, got %v", err)
	}
	if _, err := repo.GetOAuthIdentity(context.Background(), "google", "g-1"); !apperr.IsNotFound(err) {
		t.Fatalf("want no identity created on a lookup error, got %v", err)
	}
}

func TestOAuthExchange_RejectsMissingClaims(t *testing.T) {
	svc, repo, _, exchanger, _ := newTestService(t)
	ctx := context.Background()
	// An account stored without an email must not be reachable through an email-less token.
	if err := repo.CreateUser(ctx, &User{ID: "u-blank", Email: "", EmailVerified: true, Status: "active"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	exchanger.responses["no-email"] = OAuthUserInfo{Provider: "google", Subject: "g-1", EmailVerified: true}
	exchanger.responses["no-sub"] = OAuthUserInfo{Provider: "google", Email: "jo@example.com", EmailVerified: true}

	if _, _, err := oauthLogin(ctx, svc, "google", "no-email"); !apperr.IsFailedPrecondition(err) {
		t.Fatalf("empty email: want FailedPrecondition, got %v", err)
	}
	if _, _, err := oauthLogin(ctx, svc, "google", "no-sub"); !apperr.IsUnauthenticated(err) {
		t.Fatalf("empty subject: want Unauthenticated, got %v", err)
	}
}

// ─── ActivateProfile tests ────────────────────────────────────────────────────

func TestActivateProfile(t *testing.T) {
	t.Run("own profile: new access token carries the new profile_id", func(t *testing.T) {
		svc, _, _, _, signer := newTestService(t)
		ctx := context.Background()

		userID, err := svc.Register(ctx, "henry@example.com", "password1", "en")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		tok, err := svc.Login(ctx, "henry@example.com", "password1")
		loginProfileID := tok.ProfileID
		if err != nil {
			t.Fatalf("Login: %v", err)
		}

		// Create a second profile.
		secondProfile, err := svc.CreateProfile(ctx, userID, "Work")
		if err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}

		accessToken, expiresIn, err := svc.ActivateProfile(ctx, userID, secondProfile.ID)
		if err != nil {
			t.Fatalf("ActivateProfile: %v", err)
		}
		if expiresIn != int64((15 * time.Minute).Seconds()) {
			t.Fatalf("unexpected expiresIn %d", expiresIn)
		}

		claims, err := signer.Verify(accessToken)
		if err != nil {
			t.Fatalf("verify access token: %v", err)
		}
		if claims.ProfileID != secondProfile.ID {
			t.Fatalf("access token profile_id %q; want %q", claims.ProfileID, secondProfile.ID)
		}
		if claims.ProfileID == loginProfileID {
			t.Fatal("activate profile should change the profile_id in the token")
		}
	})

	t.Run("foreign profile returns PermissionDenied", func(t *testing.T) {
		svc, repo, _, _, _ := newTestService(t)
		ctx := context.Background()

		userID1, err := svc.Register(ctx, "ida@example.com", "password1", "")
		if err != nil {
			t.Fatalf("Register user1: %v", err)
		}
		_ = userID1

		// Create a profile belonging to a different user.
		otherProfile := &Profile{
			ID: "foreign-profile", UserID: "other-user-id",
			Name: "Other", IsDefault: false, CreatedAt: time.Now(),
		}
		if err := repo.CreateProfile(ctx, otherProfile); err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}

		_, _, err = svc.ActivateProfile(ctx, userID1, "foreign-profile")
		if err == nil {
			t.Fatal("expected error for foreign profile")
		}
		if grpcCode(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied, got %v", grpcCode(err))
		}
	})
}

// ─── VerifyEmail tests ────────────────────────────────────────────────────────

func TestVerifyEmail(t *testing.T) {
	t.Run("valid token marks email verified and consumes token", func(t *testing.T) {
		svc, repo, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		_, err := svc.Register(ctx, "jane@example.com", "password1", "")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}

		emailer.mu.Lock()
		token := emailer.verifications[0]
		emailer.mu.Unlock()

		if err := svc.VerifyEmail(ctx, token); err != nil {
			t.Fatalf("VerifyEmail: %v", err)
		}

		user, err := repo.GetUserByEmail(ctx, "jane@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		if !user.EmailVerified {
			t.Fatal("expected email_verified=true after VerifyEmail")
		}

		// Replaying the token must fail.
		if err := svc.VerifyEmail(ctx, token); err == nil {
			t.Fatal("expected error on replayed token")
		}
	})

	t.Run("expired token returns InvalidArgument", func(t *testing.T) {
		svc, repo, _, _, _ := newTestService(t)
		ctx := context.Background()

		// Insert an expired verification directly.
		past := time.Now().Add(-time.Hour)
		ev := &EmailVerification{
			TokenHash: HashToken("expired-tok"),
			UserID:    "user-x",
			Purpose:   "verify_email",
			ExpiresAt: past,
			CreatedAt: past,
		}
		if err := repo.CreateEmailVerification(ctx, ev); err != nil {
			t.Fatalf("CreateEmailVerification: %v", err)
		}

		err := svc.VerifyEmail(ctx, "expired-tok")
		if err == nil {
			t.Fatal("expected error for expired token")
		}
		if grpcCode(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", grpcCode(err))
		}
	})
}

// ─── Password-reset round-trip ────────────────────────────────────────────────

func TestPasswordReset(t *testing.T) {
	t.Run("request + reset updates the password", func(t *testing.T) {
		svc, repo, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "kate@example.com", "oldpass1", ""); err != nil {
			t.Fatalf("Register: %v", err)
		}

		if err := svc.RequestPasswordReset(ctx, "kate@example.com"); err != nil {
			t.Fatalf("RequestPasswordReset: %v", err)
		}

		emailer.mu.Lock()
		resetToken := emailer.resets[0]
		emailer.mu.Unlock()

		if err := svc.ResetPassword(ctx, resetToken, "newpass0"); err != nil {
			t.Fatalf("ResetPassword: %v", err)
		}

		// New password must verify; old one must not.
		user, err := repo.GetUserByEmail(ctx, "kate@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		ok, err := auth.VerifyPassword("newpass0", *user.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword: %v", err)
		}
		if !ok {
			t.Fatal("new password does not verify")
		}
		ok, err = auth.VerifyPassword("oldpass1", *user.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword old: %v", err)
		}
		if ok {
			t.Fatal("old password must not verify after reset")
		}
	})

	t.Run("RequestPasswordReset for unknown email silently succeeds (no enumeration)", func(t *testing.T) {
		svc, _, _, _, _ := newTestService(t)
		if err := svc.RequestPasswordReset(context.Background(), "nobody@example.com"); err != nil {
			t.Fatalf("RequestPasswordReset unknown: %v", err)
		}
	})

	t.Run("reset token already consumed returns InvalidArgument", func(t *testing.T) {
		svc, _, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "leo@example.com", "password1", ""); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := svc.RequestPasswordReset(ctx, "leo@example.com"); err != nil {
			t.Fatalf("RequestPasswordReset: %v", err)
		}
		emailer.mu.Lock()
		token := emailer.resets[0]
		emailer.mu.Unlock()

		if err := svc.ResetPassword(ctx, token, "newpass1"); err != nil {
			t.Fatalf("first ResetPassword: %v", err)
		}
		err := svc.ResetPassword(ctx, token, "newpass2")
		if err == nil {
			t.Fatal("expected error on second use of reset token")
		}
		if grpcCode(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", grpcCode(err))
		}
	})
}
