package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"

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
		return platformerrors.AlreadyExists("user with this email already exists")
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
		return nil, platformerrors.NotFound("user not found")
	}
	cp := *u
	return &cp, nil
}

func (r *fakeRepo) GetUserByEmail(_ context.Context, email string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.usersByEmail[email]
	if !ok {
		return nil, platformerrors.NotFound("user not found")
	}
	cp := *u
	return &cp, nil
}

func (r *fakeRepo) SetEmailVerified(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return platformerrors.NotFound("user not found")
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
		return platformerrors.NotFound("user not found")
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
		return platformerrors.AlreadyExists("oauth identity already exists")
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
		return nil, platformerrors.NotFound("oauth identity not found")
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
		return nil, platformerrors.NotFound("profile not found")
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
		return nil, platformerrors.NotFound("refresh token not found")
	}
	cp := *rt
	return &cp, nil
}

func (r *fakeRepo) RevokeRefreshToken(_ context.Context, jti string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.refreshTokens[jti]
	if !ok {
		return platformerrors.NotFound("refresh token not found")
	}
	rt.Revoked = true
	return nil
}

func (r *fakeRepo) RevokeFamily(_ context.Context, familyID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var jtis []string
	for _, rt := range r.refreshTokens {
		if rt.FamilyID == familyID && !rt.Revoked {
			rt.Revoked = true
			jtis = append(jtis, rt.JTI)
		}
	}
	return jtis, nil
}

func (r *fakeRepo) CreateEmailVerification(_ context.Context, ev *EmailVerification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *ev
	r.verifications[ev.Token] = &cp
	return nil
}

// ConsumeEmailVerification mirrors the real DB's single conditional UPDATE: it
// atomically marks the token consumed only when it exists, has not been
// consumed yet, and has not expired.  Returns the userID and purpose on
// success, or errors.InvalidArgument on any failure.
func (r *fakeRepo) ConsumeEmailVerification(_ context.Context, token string) (userID, purpose string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev, ok := r.verifications[token]
	if !ok || ev.ConsumedAt != nil || time.Now().After(ev.ExpiresAt) {
		return "", "", platformerrors.InvalidArgument("invalid or expired token")
	}
	now := time.Now()
	ev.ConsumedAt = &now
	return ev.UserID, ev.Purpose, nil
}

// WithTx simulates transaction semantics: it snapshots all in-memory maps
// before calling fn, and if fn returns an error it restores the snapshot so
// that the fake behaves like a real database that rolls back on error.
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

// ─── fakeSessionStore ────────────────────────────────────────────────────────

type fakeSessionStore struct {
	mu   sync.Mutex
	data map[string]RefreshSession
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{data: make(map[string]RefreshSession)}
}

func (s *fakeSessionStore) Put(_ context.Context, jti string, sess RefreshSession, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[jti] = sess
	return nil
}

func (s *fakeSessionStore) Get(_ context.Context, jti string) (RefreshSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data[jti]
	return sess, ok, nil
}

func (s *fakeSessionStore) Delete(_ context.Context, jtis ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, jti := range jtis {
		delete(s.data, jti)
	}
	return nil
}

// ─── fakeEmailSender ─────────────────────────────────────────────────────────

type fakeEmailSender struct {
	mu            sync.Mutex
	verifications []string // tokens sent via SendVerification
	resets        []string // tokens sent via SendPasswordReset
}

func (e *fakeEmailSender) SendVerification(_ context.Context, _, token string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verifications = append(e.verifications, token)
	return nil
}

func (e *fakeEmailSender) SendPasswordReset(_ context.Context, _, token string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resets = append(e.resets, token)
	return nil
}

// ─── fakeOAuthExchanger ──────────────────────────────────────────────────────

type fakeOAuthExchanger struct {
	// code → OAuthUserInfo to return
	responses map[string]OAuthUserInfo
	err       error
}

func (f *fakeOAuthExchanger) Exchange(_ context.Context, _, code, _ string) (OAuthUserInfo, error) {
	if f.err != nil {
		return OAuthUserInfo{}, f.err
	}
	info, ok := f.responses[code]
	if !ok {
		return OAuthUserInfo{}, errors.New("fakeOAuthExchanger: unknown code")
	}
	return info, nil
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

func newTestService(t *testing.T) (*Service, *fakeRepo, *fakeSessionStore, *fakeEmailSender, *fakeOAuthExchanger, *auth.Signer) {
	t.Helper()
	signer := newTestSigner(t)
	repo := newFakeRepo()
	sessions := newFakeSessionStore()
	emailer := &fakeEmailSender{}
	exchanger := &fakeOAuthExchanger{responses: make(map[string]OAuthUserInfo)}
	log := zap.NewNop()
	cfg := auth.Config{
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	svc := NewService(repo, sessions, signer, exchanger, emailer, log, cfg)
	return svc, repo, sessions, emailer, exchanger, signer
}

func grpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	var ae *platformerrors.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return codes.Unknown
}

// ─── Register tests ──────────────────────────────────────────────────────────

func TestRegister(t *testing.T) {
	t.Run("hashes password; stored hash verifies; default profile created; verification token sent", func(t *testing.T) {
		svc, repo, _, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		userID, err := svc.Register(ctx, "alice@example.com", "s3cret", "en")
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
		if *user.PasswordHash == "s3cret" {
			t.Fatal("password must not be stored in plaintext")
		}
		ok, err := auth.VerifyPassword("s3cret", *user.PasswordHash)
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
		svc, _, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "bob@example.com", "pass", ""); err != nil {
			t.Fatalf("first Register: %v", err)
		}
		_, err := svc.Register(ctx, "bob@example.com", "pass2", "")
		if err == nil {
			t.Fatal("expected error for duplicate email")
		}
		if grpcCode(err) != codes.AlreadyExists {
			t.Fatalf("expected AlreadyExists, got %v", grpcCode(err))
		}
	})

	t.Run("empty email or password returns InvalidArgument", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		for _, tc := range []struct{ email, pass string }{
			{"", "pass"},
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
		svc, _, _, _, _, signer := newTestService(t)
		ctx := context.Background()
		if _, err := svc.Register(ctx, "carol@example.com", "hunter2", "fr"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		return svc, signer, ctx
	}

	t.Run("success returns valid tokens and expiresIn", func(t *testing.T) {
		svc, signer, ctx := setup(t)

		access, refresh, expiresIn, userID, profileID, err := svc.Login(ctx, "carol@example.com", "hunter2")
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
		_, _, _, _, _, err := svc.Login(ctx, "carol@example.com", "wrong")
		if err == nil {
			t.Fatal("expected error")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})

	t.Run("unknown email returns Unauthenticated (not NotFound — no enumeration)", func(t *testing.T) {
		svc, _, ctx := setup(t)
		_, _, _, _, _, err := svc.Login(ctx, "nobody@example.com", "pass")
		if err == nil {
			t.Fatal("expected error")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})

	t.Run("OAuth-only user (nil hash) returns Unauthenticated", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestService(t)
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

		_, _, _, _, _, err := svc.Login(ctx, "oauth@example.com", "any")
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
		svc, _, _, _, _, signer := newTestService(t)
		ctx := context.Background()
		if _, err := svc.Register(ctx, "dave@example.com", "pass", "de"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		_, refresh, _, _, _, err := svc.Login(ctx, "dave@example.com", "pass")
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

		newAccess, newRefresh, expiresIn, err := svc.Refresh(ctx, oldRefresh)
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if newAccess == "" || newRefresh == "" {
			t.Fatal("expected non-empty tokens")
		}
		if expiresIn != int64((15 * time.Minute).Seconds()) {
			t.Fatalf("unexpected expiresIn %d", expiresIn)
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
		svc, _, ctx, oldRefresh := setup(t)

		// Rotate once legitimately — oldRefresh → tokenB.
		_, tokenB, _, err := svc.Refresh(ctx, oldRefresh)
		if err != nil {
			t.Fatalf("first Refresh: %v", err)
		}
		// Rotate again with tokenB so the family has >1 member — tokenB → tokenC.
		_, tokenC, _, err := svc.Refresh(ctx, tokenB)
		if err != nil {
			t.Fatalf("second Refresh: %v", err)
		}
		_ = tokenC

		// Now replay the original (already-revoked) token — must revoke the whole family.
		_, _, _, err = svc.Refresh(ctx, oldRefresh)
		if err == nil {
			t.Fatal("expected error on revoked-token reuse")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}

		// GAP-2: the currently-valid token (tokenC) must also now be rejected because
		// the family was fully revoked when the replay was detected.
		_, _, _, err = svc.Refresh(ctx, tokenC)
		if err == nil {
			t.Fatal("expected tokenC to be rejected after family revocation")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated for tokenC after family revocation, got %v", grpcCode(err))
		}
	})

	t.Run("invalid/garbage token returns Unauthenticated", func(t *testing.T) {
		svc, _, ctx, _ := setup(t)
		_, _, _, err := svc.Refresh(ctx, "not.a.jwt")
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})
}

// ─── OAuthExchange tests ──────────────────────────────────────────────────────

func TestOAuthExchange(t *testing.T) {
	t.Run("new user isNewUser=true; user + profile + identity created", func(t *testing.T) {
		svc, repo, _, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		exchanger.responses["code1"] = OAuthUserInfo{
			Provider: "google", Subject: "g-sub-1",
			Email: "eve@example.com", EmailVerified: true,
		}

		_, _, _, userID, _, isNew, err := svc.OAuthExchange(ctx, "google", "code1", "https://app/cb")
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
	})

	t.Run("existing identity isNewUser=false; returns same userID", func(t *testing.T) {
		svc, _, _, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		exchanger.responses["code2"] = OAuthUserInfo{
			Provider: "google", Subject: "g-sub-2",
			Email: "frank@example.com", EmailVerified: true,
		}

		_, _, _, userID1, _, _, err := svc.OAuthExchange(ctx, "google", "code2", "https://app/cb")
		if err != nil {
			t.Fatalf("first OAuthExchange: %v", err)
		}

		_, _, _, userID2, _, isNew, err := svc.OAuthExchange(ctx, "google", "code2", "https://app/cb")
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
		svc, repo, _, _, exchanger, _ := newTestService(t)
		ctx := context.Background()

		// Create a password user first.
		userID, err := svc.Register(ctx, "grace@example.com", "pass", "")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}

		exchanger.responses["code3"] = OAuthUserInfo{
			Provider: "github", Subject: "gh-sub-1",
			Email: "grace@example.com", EmailVerified: true,
		}

		_, _, _, returnedUserID, _, isNew, err := svc.OAuthExchange(ctx, "github", "code3", "https://app/cb")
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
		oauthID, err := repo.GetOAuthIdentity(ctx, "github", "gh-sub-1")
		if err != nil {
			t.Fatalf("GetOAuthIdentity after link: %v", err)
		}
		if oauthID.UserID != userID {
			t.Fatal("oauth identity linked to wrong user")
		}
	})
}

// ─── ActivateProfile tests ────────────────────────────────────────────────────

func TestActivateProfile(t *testing.T) {
	t.Run("own profile: new access token carries the new profile_id", func(t *testing.T) {
		svc, _, _, _, _, signer := newTestService(t)
		ctx := context.Background()

		userID, err := svc.Register(ctx, "henry@example.com", "pass", "en")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		_, _, _, _, loginProfileID, err := svc.Login(ctx, "henry@example.com", "pass")
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
		svc, repo, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		userID1, err := svc.Register(ctx, "ida@example.com", "pass", "")
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

// ─── ValidateToken tests ──────────────────────────────────────────────────────

func TestValidateToken(t *testing.T) {
	svc, _, _, _, _, signer := newTestService(t)
	ctx := context.Background()

	// Register and login to get real tokens minted by the same signer.
	if _, err := svc.Register(ctx, "validate@example.com", "pass", "fr"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, _, _, userID, profileID, err := svc.Login(ctx, "validate@example.com", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Mint a valid access token and a valid refresh token via the signer directly.
	accessToken, _, err := signer.SignAccess(userID, profileID, "fr", DefaultRoles())
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	refreshToken, _, err := signer.SignRefresh(userID, profileID)
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	t.Run("valid access token returns correct claims", func(t *testing.T) {
		gotUserID, gotProfileID, locale, roles, err := svc.ValidateToken(ctx, accessToken)
		if err != nil {
			t.Fatalf("ValidateToken: %v", err)
		}
		if gotUserID != userID {
			t.Fatalf("user_id: got %q, want %q", gotUserID, userID)
		}
		if gotProfileID != profileID {
			t.Fatalf("profile_id: got %q, want %q", gotProfileID, profileID)
		}
		if locale != "fr" {
			t.Fatalf("locale: got %q, want %q", locale, "fr")
		}
		if len(roles) == 0 || roles[0] != "user" {
			t.Fatalf("roles: got %v, want [user]", roles)
		}
	})

	t.Run("refresh token is rejected with Unauthenticated", func(t *testing.T) {
		_, _, _, _, err := svc.ValidateToken(ctx, refreshToken)
		if err == nil {
			t.Fatal("expected error for refresh token")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})

	t.Run("garbage token is rejected with Unauthenticated", func(t *testing.T) {
		_, _, _, _, err := svc.ValidateToken(ctx, "not.a.real.token")
		if err == nil {
			t.Fatal("expected error for garbage token")
		}
		if grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", grpcCode(err))
		}
	})
}

// ─── VerifyEmail tests ────────────────────────────────────────────────────────

func TestVerifyEmail(t *testing.T) {
	t.Run("valid token marks email verified and consumes token", func(t *testing.T) {
		svc, repo, _, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		_, err := svc.Register(ctx, "jane@example.com", "pass", "")
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
		svc, repo, _, _, _, _ := newTestService(t)
		ctx := context.Background()

		// Insert an expired verification directly.
		past := time.Now().Add(-time.Hour)
		ev := &EmailVerification{
			Token:     "expired-tok",
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
		svc, repo, _, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "kate@example.com", "oldpass", ""); err != nil {
			t.Fatalf("Register: %v", err)
		}

		if err := svc.RequestPasswordReset(ctx, "kate@example.com"); err != nil {
			t.Fatalf("RequestPasswordReset: %v", err)
		}

		emailer.mu.Lock()
		resetToken := emailer.resets[0]
		emailer.mu.Unlock()

		if err := svc.ResetPassword(ctx, resetToken, "newpass"); err != nil {
			t.Fatalf("ResetPassword: %v", err)
		}

		// New password must verify; old one must not.
		user, err := repo.GetUserByEmail(ctx, "kate@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		ok, err := auth.VerifyPassword("newpass", *user.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword: %v", err)
		}
		if !ok {
			t.Fatal("new password does not verify")
		}
		ok, err = auth.VerifyPassword("oldpass", *user.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword old: %v", err)
		}
		if ok {
			t.Fatal("old password must not verify after reset")
		}
	})

	t.Run("RequestPasswordReset for unknown email silently succeeds (no enumeration)", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestService(t)
		if err := svc.RequestPasswordReset(context.Background(), "nobody@example.com"); err != nil {
			t.Fatalf("RequestPasswordReset unknown: %v", err)
		}
	})

	t.Run("reset token already consumed returns InvalidArgument", func(t *testing.T) {
		svc, _, _, emailer, _, _ := newTestService(t)
		ctx := context.Background()

		if _, err := svc.Register(ctx, "leo@example.com", "pass", ""); err != nil {
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
