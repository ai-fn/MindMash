//go:build integration

package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/cache"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
)

// ─── Test stubs ──────────────────────────────────────────────────────────────

// stubOAuthExchanger is a minimal OAuthExchanger that returns a pre-set value.
type stubOAuthExchanger struct {
	info OAuthUserInfo
	err  error
}

func (s *stubOAuthExchanger) Exchange(_ context.Context, _, _, _ string) (OAuthUserInfo, error) {
	return s.info, s.err
}

// captureEmailSender captures the last verification and reset tokens sent.
type captureEmailSender struct {
	verificationToken string
	resetToken        string
	lastLocale        string
}

func (c *captureEmailSender) SendVerification(_ context.Context, _, token, locale string) error {
	c.verificationToken = token
	c.lastLocale = locale
	return nil
}

func (c *captureEmailSender) SendPasswordReset(_ context.Context, _, token, locale string) error {
	c.resetToken = token
	c.lastLocale = locale
	return nil
}

// ─── Setup helpers ───────────────────────────────────────────────────────────

// integrationComponents holds all the pieces built during setup.
type integrationComponents struct {
	svc     *Service
	repo    *PostgresRepository
	signer  *auth.Signer
	limiter *RedisRateLimiter
}

// setupIntegration starts Postgres and Redis containers, runs migrations, and
// wires up a *Service using the default LogEmailSender and a no-op OAuth stub.
func setupIntegration(t *testing.T) integrationComponents {
	t.Helper()
	return setupFull(t, NewLogEmailSender(zap.NewNop(), true), &stubOAuthExchanger{})
}

// setupWithEmail is like setupIntegration but accepts a custom EmailSender so
// tests that need to capture tokens can inject their own implementation.
func setupWithEmail(t *testing.T, emailSender EmailSender) integrationComponents {
	t.Helper()
	return setupFull(t, emailSender, &stubOAuthExchanger{})
}

// setupFull is the core integration setup: starts Postgres and Redis containers,
// runs migrations, and wires a *Service with the provided EmailSender and OAuthExchanger.
func setupFull(t *testing.T, emailSender EmailSender, exchanger OAuthExchanger) integrationComponents {
	t.Helper()
	ctx := context.Background()

	// ── Postgres ────────────────────────────────────────────────────────────
	pgc, err := postgres.Run(ctx,
		"postgres:18-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Apply migrations (path relative to this package's directory).
	require.NoError(t, platformdb.RunMigrations(ctx, dsn, "../../migrations/identity"))

	dbCfg := platformdb.Config{
		DSN:             dsn,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
	pool, err := platformdb.New(ctx, dbCfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	repo := NewPostgresRepository(pool)

	// ── Redis ────────────────────────────────────────────────────────────────
	rc, err := tcredis.Run(ctx, "redis:8-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Terminate(ctx) })

	addr, err := rc.Endpoint(ctx, "")
	require.NoError(t, err)

	redisCfg := cache.Config{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	redisClient, err := cache.New(ctx, redisCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = redisClient.Close() })

	// ── Auth signer ──────────────────────────────────────────────────────────
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	authCfg := auth.Config{
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh-test",
	}
	signer := auth.NewSignerFromKeys(priv, pub, authCfg)

	// ── Service ──────────────────────────────────────────────────────────────
	limiter := NewRedisRateLimiter(redisClient)
	svc := NewService(repo, signer, exchanger, emailSender, zap.NewNop(), authCfg, WithRateLimiter(limiter), WithSyncEmail())

	return integrationComponents{
		svc:     svc,
		repo:    repo,
		signer:  signer,
		limiter: limiter,
	}
}

// ─── Test cases ──────────────────────────────────────────────────────────────

// TestRegisterLogin_Integration verifies happy-path registration and login.
func TestRegisterLogin_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	userID, err := c.svc.Register(ctx, "alice@example.com", "s3cret-pw!", "en")
	require.NoError(t, err)
	assert.NotEmpty(t, userID)

	accessToken, refreshToken, expiresIn, gotUserID, profileID, err := c.svc.Login(ctx, "alice@example.com", "s3cret-pw!")
	require.NoError(t, err)
	assert.NotEmpty(t, accessToken)
	assert.NotEmpty(t, refreshToken)
	assert.Greater(t, expiresIn, int64(0))
	assert.Equal(t, userID, gotUserID)
	assert.NotEmpty(t, profileID)
}

// TestFullAuthCycle_Integration exercises the complete auth lifecycle:
// Register → Login → Refresh → second Refresh (on rotated token) → Logout.
func TestFullAuthCycle_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	// Register and login.
	_, err := c.svc.Register(ctx, "bob@example.com", "hunter2-pw", "en")
	require.NoError(t, err)

	_, origRefresh, _, _, _, err := c.svc.Login(ctx, "bob@example.com", "hunter2-pw")
	require.NoError(t, err)

	// First Refresh: token should rotate.
	newAccess, newRefresh, _, err := c.svc.Refresh(ctx, origRefresh)
	require.NoError(t, err)
	assert.NotEmpty(t, newAccess)
	assert.NotEmpty(t, newRefresh)
	assert.NotEqual(t, origRefresh, newRefresh, "refresh token must rotate")

	// Second Refresh with the rotated token should succeed.
	newAccess2, newRefresh2, _, err := c.svc.Refresh(ctx, newRefresh)
	require.NoError(t, err)
	assert.NotEmpty(t, newAccess2)
	assert.NotEmpty(t, newRefresh2)

	// Logout using the current refresh token.
	c.svc.Logout(ctx, newRefresh2)

	// After logout, the current refresh token must be rejected.
	_, _, _, err = c.svc.Refresh(ctx, newRefresh2)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestRefreshReuseDetection_Integration verifies that reusing a rotated refresh
// token revokes the entire token family.
func TestRefreshReuseDetection_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	_, err := c.svc.Register(ctx, "carol@example.com", "p@ssw0rd", "en")
	require.NoError(t, err)

	_, origRefresh, _, _, _, err := c.svc.Login(ctx, "carol@example.com", "p@ssw0rd")
	require.NoError(t, err)

	// First, legitimately rotate the token.
	_, survivingRefresh, _, err := c.svc.Refresh(ctx, origRefresh)
	require.NoError(t, err)

	// Re-use the already-rotated original token → triggers family revocation.
	// A replay inside the concurrent-refresh grace window is not theft; age the rotation past it.
	_, err = c.repo.pool.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = now() - interval '1 minute' WHERE rotated_at IS NOT NULL`)
	require.NoError(t, err)

	_, _, _, err = c.svc.Refresh(ctx, origRefresh)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// The surviving token from the first rotation must also be revoked now.
	_, _, _, err = c.svc.Refresh(ctx, survivingRefresh)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// Verify at the DB level that every token in the family is revoked.
	survivingClaims, parseErr := c.signer.Verify(survivingRefresh)
	if parseErr == nil {
		var unrevoked int
		dbErr := c.repo.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM refresh_tokens
			 WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE jti = $1)
			   AND revoked = false`,
			survivingClaims.ID,
		).Scan(&unrevoked)
		require.NoError(t, dbErr)
		assert.Zero(t, unrevoked, "all family tokens must be revoked in the DB after reuse detection")
	}
}

// TestEmailVerification_Integration tests the full email-verification flow.
func TestEmailVerification_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	sender := &captureEmailSender{}
	c := setupWithEmail(t, sender)

	userID, err := c.svc.Register(ctx, "dave@example.com", "v3rify-pw!", "en")
	require.NoError(t, err)

	// email_verified must be false right after registration.
	user, err := c.repo.GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.False(t, user.EmailVerified)

	verToken := sender.verificationToken
	require.NotEmpty(t, verToken, "captureEmailSender should have received a verification token")

	// Verify the email.
	require.NoError(t, c.svc.VerifyEmail(ctx, verToken))

	// Re-read from DB and confirm email_verified is now true.
	user, err = c.repo.GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.True(t, user.EmailVerified)

	// Calling VerifyEmail again with the same (consumed) token must fail.
	err = c.svc.VerifyEmail(ctx, verToken)
	require.Error(t, err)
}

// TestPasswordReset_Integration verifies the full password-reset flow.
func TestPasswordReset_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	sender := &captureEmailSender{}
	c := setupWithEmail(t, sender)

	_, err := c.svc.Register(ctx, "eve@example.com", "oldpass1", "en")
	require.NoError(t, err)

	// Verify the email so the account is fully active.
	verToken := sender.verificationToken
	require.NotEmpty(t, verToken)
	require.NoError(t, c.svc.VerifyEmail(ctx, verToken))

	// Request a password reset.
	require.NoError(t, c.svc.RequestPasswordReset(ctx, "eve@example.com"))

	resetToken := sender.resetToken
	require.NotEmpty(t, resetToken, "captureEmailSender should have received a reset token")

	// Perform the reset.
	require.NoError(t, c.svc.ResetPassword(ctx, resetToken, "newpass1"))

	// Old password must no longer work.
	_, _, _, _, _, err = c.svc.Login(ctx, "eve@example.com", "oldpass1")
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// New password must work.
	access, refresh, _, _, _, err := c.svc.Login(ctx, "eve@example.com", "newpass1")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
}

// TestOAuthExchange_Integration verifies that OAuthExchange creates a new user
// on first call and returns isNew=false on subsequent calls with the same
// provider+subject.
func TestOAuthExchange_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	stub := &stubOAuthExchanger{
		info: OAuthUserInfo{
			Provider:      "google",
			Subject:       "oauth-sub-unique-001",
			Email:         "frank@oauth.example.com",
			EmailVerified: true,
		},
	}
	c := setupFull(t, NewLogEmailSender(zap.NewNop(), true), stub)

	// First call: brand-new OAuth user.
	access, refresh, _, userID, profileID, isNew, err := c.svc.OAuthExchange(ctx, "google", "code-abc", "https://example.com/cb")
	require.NoError(t, err)
	assert.True(t, isNew, "first OAuth exchange must report isNew=true")
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
	assert.NotEmpty(t, userID)
	assert.NotEmpty(t, profileID)

	// Second call with same provider+subject: existing identity.
	access2, refresh2, _, userID2, _, isNew2, err := c.svc.OAuthExchange(ctx, "google", "code-abc", "https://example.com/cb")
	require.NoError(t, err)
	assert.False(t, isNew2, "second OAuth exchange must report isNew=false")
	assert.NotEmpty(t, access2)
	assert.NotEmpty(t, refresh2)
	assert.Equal(t, userID, userID2, "same user must be returned on repeat OAuth exchange")
}

// TestRedisRateLimiter_Integration verifies the fixed window: the limit holds, keys are
// independent, and the counter resets once the window passes.
func TestRedisRateLimiter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	for i := 1; i <= 3; i++ {
		ok, err := c.limiter.Allow(ctx, "rl:test:a", 3, 2*time.Second)
		require.NoError(t, err)
		assert.True(t, ok, "attempt %d is within the limit", i)
	}
	ok, err := c.limiter.Allow(ctx, "rl:test:a", 3, 2*time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "attempt 4 exceeds the limit")

	ok, err = c.limiter.Allow(ctx, "rl:test:b", 3, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "other keys keep their own budget")

	// Later attempts must not extend the window, or a steady attacker would never be released.
	time.Sleep(1200 * time.Millisecond)
	ok, err = c.limiter.Allow(ctx, "rl:test:a", 3, 2*time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "still inside the window")
	time.Sleep(1200 * time.Millisecond)
	ok, err = c.limiter.Allow(ctx, "rl:test:a", 3, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "the window expired 2s after the first attempt")

	require.NoError(t, c.limiter.Reset(ctx, "rl:test:b"))
	for range 3 {
		ok, err = c.limiter.Allow(ctx, "rl:test:b", 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, ok, "Reset restores the full budget")
	}
}

// TestLoginRateLimit_Integration verifies the limiter wired through Login against real Redis.
func TestLoginRateLimit_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	_, err := c.svc.Register(ctx, "frank@example.com", "correct-horse", "en")
	require.NoError(t, err)

	for range loginLimit {
		_, _, _, _, _, err = c.svc.Login(ctx, "frank@example.com", "wrong-password")
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}
	_, _, _, _, _, err = c.svc.Login(ctx, "frank@example.com", "correct-horse")
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// TestAccountRecoveryRevokesSessions_Integration covers the SQL behind reset and OAuth claiming.
func TestAccountRecoveryRevokesSessions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	sender := &captureEmailSender{}
	stub := &stubOAuthExchanger{info: OAuthUserInfo{
		Provider: "google", Subject: "owner-sub", Email: "victim@example.com", EmailVerified: true,
	}}
	c := setupFull(t, sender, stub)

	// Reset: a stolen session dies, tokens are stored hashed and bound to their purpose.
	uid, err := c.svc.Register(ctx, "stolen@example.com", "password-one", "en")
	require.NoError(t, err)
	_, stolenRefresh, _, _, _, err := c.svc.Login(ctx, "stolen@example.com", "password-one")
	require.NoError(t, err)
	require.NoError(t, c.svc.RequestPasswordReset(ctx, "stolen@example.com"))

	assert.Equal(t, codes.InvalidArgument, status.Code(c.svc.VerifyEmail(ctx, sender.resetToken)), "purpose binding")
	require.NoError(t, c.svc.ResetPassword(ctx, sender.resetToken, "password-two"))
	_, _, _, err = c.svc.Refresh(ctx, stolenRefresh)
	assert.Error(t, err, "sessions must not survive a reset")
	user, err := c.repo.GetUserByID(ctx, uid)
	require.NoError(t, err)
	assert.True(t, user.EmailVerified)

	var storedRaw int
	require.NoError(t, c.repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_verifications WHERE token_hash = $1`, sender.resetToken).Scan(&storedRaw))
	assert.Zero(t, storedRaw, "raw tokens must not be stored")
	var storedHashed int
	require.NoError(t, c.repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_verifications WHERE token_hash = $1`, HashToken(sender.resetToken)).Scan(&storedHashed))
	assert.Equal(t, 1, storedHashed)

	// Claim: the provider-verified owner takes over an account whose password predates the proof.
	_, err = c.svc.Register(ctx, "victim@example.com", "attackers-pw", "en")
	require.NoError(t, err)
	_, attackerRefresh, _, _, _, err := c.svc.Login(ctx, "victim@example.com", "attackers-pw")
	require.NoError(t, err)

	_, _, _, ownerID, _, isNew, err := c.svc.OAuthExchange(ctx, "google", "code", "https://app/cb")
	require.NoError(t, err)
	assert.False(t, isNew)

	_, _, _, _, _, err = c.svc.Login(ctx, "victim@example.com", "attackers-pw")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, _, _, err = c.svc.Refresh(ctx, attackerRefresh)
	assert.Error(t, err)
	owner, err := c.repo.GetUserByID(ctx, ownerID)
	require.NoError(t, err)
	assert.Nil(t, owner.PasswordHash)
	assert.True(t, owner.EmailVerified)
	assert.Error(t, c.svc.VerifyEmail(ctx, sender.verificationToken), "pending tokens are invalidated by the claim")
}

// TestClaimEndsRacingRefreshLoop_Integration: an attacker rotating refresh tokens in a tight loop
// must not keep a live session once the owner claims the account.
func TestClaimEndsRacingRefreshLoop_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	stub := &stubOAuthExchanger{info: OAuthUserInfo{
		Provider: "google", Subject: "owner-sub", Email: "race@example.com", EmailVerified: true,
	}}
	c := setupFull(t, &captureEmailSender{}, stub)

	uid, err := c.svc.Register(ctx, "race@example.com", "attackers-pw", "en")
	require.NoError(t, err)
	_, refresh, _, _, _, err := c.svc.Login(ctx, "race@example.com", "attackers-pw")
	require.NoError(t, err)

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for token := refresh; ; {
			_, next, _, err := c.svc.Refresh(ctx, token)
			if err != nil {
				return
			}
			token = next
		}
	}()
	time.Sleep(200 * time.Millisecond)

	_, _, _, _, _, _, err = c.svc.OAuthExchange(ctx, "google", "code", "https://app/cb")
	require.NoError(t, err)

	select {
	case <-loopDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the attacker's refresh loop is still rotating after the claim")
	}
	var live int
	require.NoError(t, c.repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND NOT revoked`, uid).Scan(&live))
	assert.Equal(t, 1, live, "only the owner's new session may be live")
}

// slowMintRepo delays the refresh-token insert inside a transaction, widening the window in which
// a rotation holds its locks while a claim or reset runs.
type slowMintRepo struct {
	*PostgresRepository
	delay time.Duration
}

func (r slowMintRepo) WithTx(ctx context.Context, fn func(Repository) error) error {
	return r.PostgresRepository.WithTx(ctx, func(tx Repository) error { return fn(slowMintTx{tx, r.delay}) })
}

type slowMintTx struct {
	Repository
	delay time.Duration
}

func (r slowMintTx) CreateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	time.Sleep(r.delay)
	return r.Repository.CreateRefreshToken(ctx, rt)
}

// TestClaimRevokesInFlightRotation_Integration: a claim that starts while a refresh is mid-rotation
// must still end up revoking the token that rotation mints.
func TestClaimRevokesInFlightRotation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	stub := &stubOAuthExchanger{info: OAuthUserInfo{
		Provider: "google", Subject: "owner-sub", Email: "mint@example.com", EmailVerified: true,
	}}
	c := setupFull(t, &captureEmailSender{}, stub)
	cfg := auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}
	slow := NewService(slowMintRepo{c.repo, 800 * time.Millisecond}, c.signer, stub, &captureEmailSender{}, zap.NewNop(), cfg, WithSyncEmail())

	uid, err := c.svc.Register(ctx, "mint@example.com", "attackers-pw", "en")
	require.NoError(t, err)
	_, refresh, _, _, _, err := c.svc.Login(ctx, "mint@example.com", "attackers-pw")
	require.NoError(t, err)

	refreshDone := make(chan error, 1)
	go func() {
		_, _, _, err := slow.Refresh(ctx, refresh)
		refreshDone <- err
	}()
	time.Sleep(300 * time.Millisecond) // Refresh now holds its locks and is sleeping before the insert

	_, _, _, _, _, _, err = c.svc.OAuthExchange(ctx, "google", "code", "https://app/cb")
	require.NoError(t, err)
	<-refreshDone

	var live int
	require.NoError(t, c.repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND NOT revoked`, uid).Scan(&live))
	assert.Equal(t, 1, live, "the rotated token must not outlive the claim; only the owner's session remains")
}

// TestPurgeExpired_Integration: only rows expired longer than the cutoff go, across batch boundaries.
func TestPurgeExpired_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)
	uid, err := c.svc.Register(ctx, "purge@example.com", "password-one", "en")
	require.NoError(t, err) // leaves one live verification token
	_, _, _, _, _, err = c.svc.Login(ctx, "purge@example.com", "password-one")
	require.NoError(t, err) // and one live refresh token

	_, err = c.repo.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at)
		SELECT 'old-' || i, $1, 'f', now() - interval '3 days' FROM generate_series(1, $2) i`, uid, purgeBatch+200)
	require.NoError(t, err)
	_, err = c.repo.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at) VALUES ('recent', $1, 'f', now() - interval '1 hour')`, uid)
	require.NoError(t, err)
	_, err = c.repo.pool.Exec(ctx, `
		INSERT INTO email_verifications (token_hash, user_id, purpose, expires_at)
		VALUES (repeat('a', 64), $1, 'verify_email', now() - interval '3 days'),
		       (repeat('b', 64), $1, 'verify_email', now() - interval '1 hour')`, uid)
	require.NoError(t, err)

	tokens, verifications, err := c.repo.PurgeExpired(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(purgeBatch+200), tokens)
	assert.Equal(t, int64(1), verifications)

	var liveTokens, liveVerifications int
	require.NoError(t, c.repo.pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&liveTokens))
	require.NoError(t, c.repo.pool.QueryRow(ctx, `SELECT count(*) FROM email_verifications`).Scan(&liveVerifications))
	assert.Equal(t, 2, liveTokens, "the live login token and the recently expired one stay")
	assert.Equal(t, 2, liveVerifications, "the registration token and the recently expired one stay")
}

// TestConcurrentRefreshWithOneToken_Integration: two tabs refreshing with the same token both get a session.
func TestConcurrentRefreshWithOneToken_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)
	_, err := c.svc.Register(ctx, "tabs@example.com", "password-one", "en")
	require.NoError(t, err)
	_, refresh, _, _, _, err := c.svc.Login(ctx, "tabs@example.com", "password-one")
	require.NoError(t, err)

	errs := make(chan error, 2)
	tokens := make(chan string, 2)
	for range 2 {
		go func() {
			_, next, _, err := c.svc.Refresh(ctx, refresh)
			errs <- err
			tokens <- next
		}()
	}
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	first, second := <-tokens, <-tokens
	assert.NotEqual(t, first, second)

	for _, tok := range []string{first, second} {
		_, _, _, err := c.svc.Refresh(ctx, tok)
		require.NoError(t, err, "both siblings stay usable")
	}

	c.svc.Logout(ctx, first)
	_, _, _, err = c.svc.Refresh(ctx, refresh)
	assert.Error(t, err, "logout must end the family, grace window included")
}

// TestLogoutRevokesSiblingMintedMidFlight_Integration: after the grace window a family can hold several
// live tokens; logging out through one must still catch a sibling another tab is minting.
func TestLogoutRevokesSiblingMintedMidFlight_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)
	cfg := auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}
	slow := NewService(slowMintRepo{c.repo, 800 * time.Millisecond}, c.signer, &stubOAuthExchanger{}, &captureEmailSender{}, zap.NewNop(), cfg, WithSyncEmail())

	uid, err := c.svc.Register(ctx, "tabs2@example.com", "password-one", "en")
	require.NoError(t, err)
	_, root, _, _, _, err := c.svc.Login(ctx, "tabs2@example.com", "password-one")
	require.NoError(t, err)
	_, tabOne, _, err := c.svc.Refresh(ctx, root)
	require.NoError(t, err)
	_, tabTwo, _, err := c.svc.Refresh(ctx, root) // inside the grace window: a sibling
	require.NoError(t, err)

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		_, _, _, _ = slow.Refresh(ctx, tabTwo) // mid-flight when the logout arrives
	}()
	time.Sleep(300 * time.Millisecond)
	c.svc.Logout(ctx, tabOne)
	<-refreshDone

	var live int
	require.NoError(t, c.repo.pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND NOT revoked`, uid).Scan(&live))
	assert.Zero(t, live, "no token of the family may survive the logout")
}

// TestPurgeExpiredSkipsLockedRows_Integration: rows held by another transaction are left for the next run.
func TestPurgeExpiredSkipsLockedRows_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)
	uid, err := c.svc.Register(ctx, "skip@example.com", "password-one", "en")
	require.NoError(t, err)
	_, err = c.repo.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at)
		VALUES ('held', $1, 'f', now() - interval '3 days'), ('free', $1, 'f', now() - interval '3 days')`, uid)
	require.NoError(t, err)

	tx, err := c.repo.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT 1 FROM refresh_tokens WHERE jti = 'held' FOR UPDATE`)
	require.NoError(t, err)

	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tokens, _, err := c.repo.PurgeExpired(pctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err, "the purge must not block on a locked row")
	assert.Equal(t, int64(1), tokens)

	var left int
	require.NoError(t, c.repo.pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE jti = 'held'`).Scan(&left))
	assert.Equal(t, 1, left)
}

// slowLockRepo delays the token-row lock, so a transaction that began first can lose the race to rotate.
type slowLockRepo struct {
	*PostgresRepository
	delay time.Duration
}

func (r slowLockRepo) WithTx(ctx context.Context, fn func(Repository) error) error {
	return r.PostgresRepository.WithTx(ctx, func(tx Repository) error { return fn(slowLockTx{tx, r.delay}) })
}

type slowLockTx struct {
	Repository
	delay time.Duration
}

func (r slowLockTx) GetRefreshTokenForUpdate(ctx context.Context, jti string) (*RefreshToken, error) {
	time.Sleep(r.delay)
	return r.Repository.GetRefreshTokenForUpdate(ctx, jti)
}

// TestRefreshLosingTheRotationRaceIsNotTheft_Integration: the request whose transaction began first but
// reached the token row second must still be treated as concurrent, not as a replay.
func TestRefreshLosingTheRotationRaceIsNotTheft_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)
	cfg := auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}
	slow := NewService(slowLockRepo{c.repo, 600 * time.Millisecond}, c.signer, &stubOAuthExchanger{}, &captureEmailSender{}, zap.NewNop(), cfg, WithSyncEmail())

	_, err := c.svc.Register(ctx, "photo@example.com", "password-one", "en")
	require.NoError(t, err)
	_, refresh, _, _, _, err := c.svc.Login(ctx, "photo@example.com", "password-one")
	require.NoError(t, err)

	slowDone := make(chan error, 1)
	go func() {
		_, _, _, err := slow.Refresh(ctx, refresh) // begins first, locks the token last
		slowDone <- err
	}()
	time.Sleep(150 * time.Millisecond)
	_, _, _, err = c.svc.Refresh(ctx, refresh) // begins later, rotates first
	require.NoError(t, err)

	require.NoError(t, <-slowDone, "the loser of the race must get a sibling, not a family revoke")
}
