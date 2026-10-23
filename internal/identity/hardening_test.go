package identity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/mindmesh/mindmesh/internal/platform/auth"

	. "github.com/mindmesh/mindmesh/internal/identity"
)

// fakeLimiter counts attempts per key without expiry.
type fakeLimiter struct {
	mu     sync.Mutex
	counts map[string]int
	err    error
}

func (l *fakeLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return false, l.err
	}
	if l.counts == nil {
		l.counts = map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= limit, nil
}

func (l *fakeLimiter) Reset(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.counts, key)
	return nil
}

func suspend(repo *fakeRepo, email string) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.usersByEmail[email].Status = "suspended"
}

func mustRegister(t *testing.T, svc *Service, email string) string {
	t.Helper()
	id, err := svc.Register(context.Background(), email, "password1", "en")
	if err != nil {
		t.Fatalf("Register %s: %v", email, err)
	}
	return id
}

func TestRegister_Validation(t *testing.T) {
	svc, repo, _, emailer, _, _ := newTestService(t)
	ctx := context.Background()

	for _, tc := range []struct{ name, email, pass string }{
		{"not an email", "alice", "password1"},
		{"display name form", "Alice <alice@example.com>", "password1"},
		{"short password", "alice@example.com", "short"},
		{"huge password", "alice@example.com", strings.Repeat("x", 100_000)},
	} {
		if _, err := svc.Register(ctx, tc.email, tc.pass, ""); grpcCode(err) != codes.InvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", tc.name, grpcCode(err))
		}
	}
	emailer.mu.Lock()
	defer emailer.mu.Unlock()
	if len(emailer.verifications) != 0 || len(repo.users) != 0 {
		t.Fatal("rejected registrations must create nothing")
	}
}

func TestRegister_NormalizesInput(t *testing.T) {
	svc, repo, _, _, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Register(ctx, "  Zed@Example.com ", "password1", "ru-RU"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	u, err := repo.GetUserByEmail(ctx, "Zed@Example.com")
	if err != nil {
		t.Fatalf("trimmed email must be stored: %v", err)
	}
	if u.Locale != "ru" {
		t.Fatalf("locale: got %q, want ru", u.Locale)
	}
	// A login typed with stray whitespace reaches the same account.
	if _, _, _, _, _, err := svc.Login(ctx, " Zed@Example.com ", "password1"); err != nil {
		t.Fatalf("Login with padded email: %v", err)
	}
}

func TestLogin_UnknownEmailStillHashes(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	ctx := context.Background()
	mustRegister(t, svc, "carol@example.com")

	_ = svc.WarmUp()

	start := time.Now()
	_, _, _, _, _, err := svc.Login(ctx, "nobody@example.com", "password1")
	elapsed := time.Since(start)
	if grpcCode(err) != codes.Unauthenticated {
		t.Fatalf("got %v, want Unauthenticated", grpcCode(err))
	}
	// An argon2id run (64 MiB, 3 passes) takes tens of ms; skipping it would return in microseconds.
	if elapsed < 5*time.Millisecond {
		t.Fatalf("unknown email returned in %v; the dummy hash was skipped", elapsed)
	}
}

func TestLogin_MalformedInputIsInvalidCredentials(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	ctx := context.Background()

	for _, tc := range []struct{ email, pass string }{
		{"", "password1"},
		{"not-an-email", "password1"},
		{"a@example.com", strings.Repeat("x", 100_000)},
	} {
		if _, _, _, _, _, err := svc.Login(ctx, tc.email, tc.pass); grpcCode(err) != codes.Unauthenticated {
			t.Errorf("Login(%q): got %v, want Unauthenticated", tc.email, grpcCode(err))
		}
	}
}

func TestLogin_SuspendedAccount(t *testing.T) {
	svc, repo, _, _, _, _ := newTestService(t)
	ctx := context.Background()
	mustRegister(t, svc, "sam@example.com")
	suspend(repo, "sam@example.com")

	if _, _, _, _, _, err := svc.Login(ctx, "sam@example.com", "password1"); grpcCode(err) != codes.PermissionDenied {
		t.Fatalf("correct password: got %v, want PermissionDenied", grpcCode(err))
	}
	// A wrong password must not reveal the suspension.
	if _, _, _, _, _, err := svc.Login(ctx, "sam@example.com", "wrong-password"); grpcCode(err) != codes.Unauthenticated {
		t.Fatalf("wrong password: got %v, want Unauthenticated", grpcCode(err))
	}
}

func TestLogin_RateLimited(t *testing.T) {
	lim := &fakeLimiter{}
	svc, _, _, _, _, _ := newTestService(t, WithRateLimiter(lim))
	ctx := context.Background()
	mustRegister(t, svc, "rate@example.com")

	for i := range 10 {
		if _, _, _, _, _, err := svc.Login(ctx, "rate@example.com", "wrong-password"); grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("attempt %d: got %v, want Unauthenticated", i+1, grpcCode(err))
		}
	}
	// Past the limit even the right password is refused, and the case/whitespace variant shares the budget.
	for _, email := range []string{"rate@example.com", " RATE@example.com "} {
		_, _, _, _, _, err := svc.Login(ctx, email, "password1")
		if grpcCode(err) != codes.ResourceExhausted {
			t.Fatalf("Login(%q): got %v, want ResourceExhausted", email, grpcCode(err))
		}
	}
	// Another account is unaffected.
	mustRegister(t, svc, "other@example.com")
	if _, _, _, _, _, err := svc.Login(ctx, "other@example.com", "password1"); err != nil {
		t.Fatalf("other account: %v", err)
	}
}

func TestLogin_LimiterOutageFailsOpen(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t, WithRateLimiter(&fakeLimiter{err: errors.New("redis down")}))
	mustRegister(t, svc, "open@example.com")
	if _, _, _, _, _, err := svc.Login(context.Background(), "open@example.com", "password1"); err != nil {
		t.Fatalf("Login must succeed when the limiter is down: %v", err)
	}
}

func TestRequestPasswordReset_RateLimited(t *testing.T) {
	svc, _, _, emailer, _, _ := newTestService(t, WithRateLimiter(&fakeLimiter{}))
	ctx := context.Background()
	mustRegister(t, svc, "reset@example.com")

	for i := range 6 {
		if err := svc.RequestPasswordReset(ctx, "reset@example.com"); err != nil {
			t.Fatalf("request %d: %v (over-limit must look like success)", i+1, err)
		}
	}
	emailer.mu.Lock()
	defer emailer.mu.Unlock()
	if len(emailer.resets) != 3 {
		t.Fatalf("reset emails sent: got %d, want 3", len(emailer.resets))
	}
}

func TestRequestPasswordReset_InvalidEmailIsSilent(t *testing.T) {
	svc, _, _, emailer, _, _ := newTestService(t)
	if err := svc.RequestPasswordReset(context.Background(), "garbage"); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if len(emailer.resets) != 0 {
		t.Fatal("no email expected")
	}
}

func TestResetPassword_RejectsWeakPassword(t *testing.T) {
	svc, _, _, emailer, _, _ := newTestService(t)
	ctx := context.Background()
	mustRegister(t, svc, "weak@example.com")
	if err := svc.RequestPasswordReset(ctx, "weak@example.com"); err != nil {
		t.Fatal(err)
	}
	token := emailer.resets[0]

	if err := svc.ResetPassword(ctx, token, "short"); grpcCode(err) != codes.InvalidArgument {
		t.Fatalf("got %v, want InvalidArgument", grpcCode(err))
	}
	// The rejected attempt must not burn the token.
	if err := svc.ResetPassword(ctx, token, "long-enough-pw"); err != nil {
		t.Fatalf("token should still be valid: %v", err)
	}
}

func TestRefresh_SuspendedAccountRevokesFamily(t *testing.T) {
	svc, repo, _, _, _, _ := newTestService(t)
	ctx := context.Background()
	mustRegister(t, svc, "tom@example.com")
	_, refresh, _, _, _, err := svc.Login(ctx, "tom@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	suspend(repo, "tom@example.com")

	if _, _, _, err := svc.Refresh(ctx, refresh); grpcCode(err) != codes.PermissionDenied {
		t.Fatalf("got %v, want PermissionDenied", grpcCode(err))
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for jti, rt := range repo.refreshTokens {
		if !rt.Revoked {
			t.Errorf("token %s still live after the account was found suspended", jti)
		}
	}
}

func TestOAuthExchange_SuspendedAccount(t *testing.T) {
	svc, repo, _, _, exchanger, _ := newTestService(t)
	ctx := context.Background()

	exchanger.responses["known"] = OAuthUserInfo{Provider: "google", Subject: "sub-1", Email: "gus@example.com", EmailVerified: true}
	if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "known", ""); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	suspend(repo, "gus@example.com")
	if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "known", ""); grpcCode(err) != codes.PermissionDenied {
		t.Fatalf("known identity: got %v, want PermissionDenied", grpcCode(err))
	}

	// Linking a new provider to a suspended, verified account is refused too.
	uid := mustRegister(t, svc, "linda@example.com")
	repo.mu.Lock()
	repo.usersByEmail["linda@example.com"].EmailVerified = true
	repo.mu.Unlock()
	suspend(repo, "linda@example.com")
	exchanger.responses["link"] = OAuthUserInfo{Provider: "google", Subject: "sub-2", Email: "linda@example.com", EmailVerified: true}
	if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "link", ""); grpcCode(err) != codes.PermissionDenied {
		t.Fatalf("link: got %v, want PermissionDenied", grpcCode(err))
	}
	if _, err := repo.GetOAuthIdentity(ctx, "google", "sub-2"); err == nil {
		t.Fatalf("identity must not be linked to suspended user %s", uid)
	}
}

func TestActivateProfile_SuspendedAccount(t *testing.T) {
	svc, repo, _, _, _, _ := newTestService(t)
	ctx := context.Background()
	uid := mustRegister(t, svc, "pat@example.com")
	profiles, err := repo.ListProfiles(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	suspend(repo, "pat@example.com")
	if _, _, err := svc.ActivateProfile(ctx, uid, profiles[0].ID); grpcCode(err) != codes.PermissionDenied {
		t.Fatalf("got %v, want PermissionDenied", grpcCode(err))
	}
}

func TestCreateProfile_NameValidation(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	ctx := context.Background()
	uid := mustRegister(t, svc, "nina@example.com")

	for _, name := range []string{"", "   ", "a\nb", strings.Repeat("x", 65)} {
		if _, err := svc.CreateProfile(ctx, uid, name); grpcCode(err) != codes.InvalidArgument {
			t.Errorf("CreateProfile(%q): got %v, want InvalidArgument", name, grpcCode(err))
		}
	}
	p, err := svc.CreateProfile(ctx, uid, "  Work ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Work" {
		t.Fatalf("name: got %q, want trimmed", p.Name)
	}
}

func TestHashConcurrencyIsBounded(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t, WithHashConcurrency(1))
	_ = svc.WarmUp()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, _, _, _ = svc.Login(context.Background(), "a@example.com", "password1")
	}()

	// While that hash holds the only slot, a request with a tight deadline must give up instead of piling on.
	deadline := time.Now().Add(5 * time.Second)
	busy := false
	for !busy && time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		_, _, _, _, _, err := svc.Login(ctx, "b@example.com", "password1")
		cancel()
		busy = grpcCode(err) == codes.ResourceExhausted
	}
	wg.Wait()
	if !busy {
		t.Fatal("no request was ever rejected as busy; the argon2 slot limit is not enforced")
	}
}

func TestLogin_UnknownAndOAuthOnlyAccountsPayForAHash(t *testing.T) {
	svc, _, _, _, exchanger, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.WarmUp()
	exchanger.responses["c"] = OAuthUserInfo{Provider: "google", Subject: "s", Email: "oauth@example.com", EmailVerified: true}
	if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "c", ""); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, _, _, _, _, err := svc.Login(ctx, "oauth@example.com", "password1")
	if grpcCode(err) != codes.Unauthenticated || time.Since(start) < 5*time.Millisecond {
		t.Fatalf("OAuth-only account: code=%v elapsed=%v; want Unauthenticated after a hash", grpcCode(err), time.Since(start))
	}
}

func TestLogin_SuccessRefundsRateLimit(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t, WithRateLimiter(&fakeLimiter{}))
	ctx := context.Background()
	mustRegister(t, svc, "multi@example.com")

	// Legitimate logins (several devices) must never exhaust the budget.
	for i := range 25 {
		if _, _, _, _, _, err := svc.Login(ctx, "multi@example.com", "password1"); err != nil {
			t.Fatalf("login %d: %v", i+1, err)
		}
	}
	// Failures before a success are forgiven by it.
	for range 9 {
		_, _, _, _, _, _ = svc.Login(ctx, "multi@example.com", "wrong-password")
	}
	if _, _, _, _, _, err := svc.Login(ctx, "multi@example.com", "password1"); err != nil {
		t.Fatalf("tenth attempt: %v", err)
	}
	for range 9 {
		_, _, _, _, _, _ = svc.Login(ctx, "multi@example.com", "wrong-password")
	}
	if _, _, _, _, _, err := svc.Login(ctx, "multi@example.com", "password1"); err != nil {
		t.Fatalf("budget should have been reset by the previous success: %v", err)
	}
}

func TestLogin_LookupErrorIsInternal(t *testing.T) {
	svc := NewService(failingEmailRepo{newFakeRepo()}, newFakeSessionStore(), newTestSigner(t), &fakeOAuthExchanger{}, &fakeEmailSender{}, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	if _, _, _, _, _, err := svc.Login(context.Background(), "x@example.com", "password1"); grpcCode(err) == codes.Unauthenticated {
		t.Fatal("an infrastructure failure must not look like bad credentials")
	}
}

func TestCancelledContextIsRejectedBeforeHashing(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Register(ctx, "c@example.com", "password1", ""); grpcCode(err) != codes.ResourceExhausted {
		t.Fatalf("Register: got %v, want ResourceExhausted", grpcCode(err))
	}
}

func TestOAuthExchange_TrimsProviderEmail(t *testing.T) {
	svc, repo, _, _, exchanger, _ := newTestService(t)
	exchanger.responses["c"] = OAuthUserInfo{Provider: "google", Subject: "s", Email: " trim@example.com ", EmailVerified: true}
	if _, _, _, _, _, _, err := svc.OAuthExchange(context.Background(), "google", "c", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetUserByEmail(context.Background(), "trim@example.com"); err != nil {
		t.Fatalf("stored email must be trimmed: %v", err)
	}
}
