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
}

func (c *captureEmailSender) SendVerification(_ context.Context, _, token string) error {
	c.verificationToken = token
	return nil
}

func (c *captureEmailSender) SendPasswordReset(_ context.Context, _, token string) error {
	c.resetToken = token
	return nil
}

// nilSessionStore implements SessionStore as a no-op; all methods succeed and
// Get always returns a miss.  Used to verify that the service continues to
// work when Redis is unavailable.
type nilSessionStore struct{}

func (nilSessionStore) Put(_ context.Context, _ string, _ RefreshSession, _ time.Duration) error {
	return nil
}
func (nilSessionStore) Get(_ context.Context, _ string) (RefreshSession, bool, error) {
	return RefreshSession{}, false, nil
}
func (nilSessionStore) Delete(_ context.Context, _ ...string) error { return nil }

// ─── Setup helpers ───────────────────────────────────────────────────────────

// integrationComponents holds all the pieces built during setup.
type integrationComponents struct {
	svc      *Service
	repo     *PostgresRepository
	sessions *RedisSessionStore
	signer   *auth.Signer
}

// setupIntegration starts Postgres and Redis containers, runs migrations, and
// wires up a *Service using the default LogEmailSender and a no-op OAuth stub.
func setupIntegration(t *testing.T) integrationComponents {
	t.Helper()
	return setupFull(t, NewLogEmailSender(zap.NewNop()), &stubOAuthExchanger{})
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

	sessions := NewRedisSessionStore(redisClient)

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
	svc := NewService(repo, sessions, signer, exchanger, emailSender, zap.NewNop(), authCfg)

	return integrationComponents{
		svc:      svc,
		repo:     repo,
		sessions: sessions,
		signer:   signer,
	}
}

// setupWithNilSessions is a variant that uses a nilSessionStore instead of
// Redis so that we can verify the service still works without a session store.
func setupWithNilSessions(t *testing.T) *Service {
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

	// ── Auth signer ──────────────────────────────────────────────────────────
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	authCfg := auth.Config{
		AccessTokenTTL:  10 * time.Minute,
		RefreshTokenTTL: 168 * time.Hour,
		Issuer:          "mindmesh-test",
	}
	signer := auth.NewSignerFromKeys(priv, pub, authCfg)

	return NewService(
		repo,
		nilSessionStore{},
		signer,
		&stubOAuthExchanger{},
		NewLogEmailSender(zap.NewNop()),
		zap.NewNop(),
		authCfg,
	)
}

// ─── Test cases ──────────────────────────────────────────────────────────────

// TestRegisterLogin_Integration verifies happy-path registration and login.
func TestRegisterLogin_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	c := setupIntegration(t)

	userID, err := c.svc.Register(ctx, "alice@example.com", "s3cret!", "en")
	require.NoError(t, err)
	assert.NotEmpty(t, userID)

	accessToken, refreshToken, expiresIn, gotUserID, profileID, err := c.svc.Login(ctx, "alice@example.com", "s3cret!")
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
	_, err := c.svc.Register(ctx, "bob@example.com", "hunter2", "en")
	require.NoError(t, err)

	_, origRefresh, _, _, _, err := c.svc.Login(ctx, "bob@example.com", "hunter2")
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

	// Verify the session is gone from Redis: parse the JTI from the token.
	// Cryptographic verification may still pass (token is structurally valid);
	// we only care that the DB-backed Refresh rejects it above.
	claims, verifyErr := c.signer.Verify(newRefresh2)
	if verifyErr == nil {
		_, found, sessErr := c.sessions.Get(ctx, claims.ID)
		require.NoError(t, sessErr)
		assert.False(t, found, "session should be evicted from Redis after logout")
	}
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

	userID, err := c.svc.Register(ctx, "dave@example.com", "v3rify!", "en")
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
	c := setupFull(t, NewLogEmailSender(zap.NewNop()), stub)

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

// TestRedisDownFallback_Integration verifies that token operations succeed even
// when the session store is a no-op (Redis unavailable).
func TestRedisDownFallback_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	svc := setupWithNilSessions(t)

	_, err := svc.Register(ctx, "grace@example.com", "nored1s!", "en")
	require.NoError(t, err)

	access, refresh, _, _, _, err := svc.Login(ctx, "grace@example.com", "nored1s!")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)

	// Refresh must succeed even without Redis.
	newAccess, newRefresh, _, err := svc.Refresh(ctx, refresh)
	require.NoError(t, err)
	assert.NotEmpty(t, newAccess)
	assert.NotEmpty(t, newRefresh)

	// Logout must succeed even without Redis.
	svc.Logout(ctx, newRefresh)
}
