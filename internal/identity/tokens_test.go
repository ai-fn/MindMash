package identity_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/mindmesh/mindmesh/internal/platform/auth"

	. "github.com/mindmesh/mindmesh/internal/identity"
)

func TestEmailTokensAreStoredHashed(t *testing.T) {
	svc, repo, emailer, _, _ := newTestService(t)
	mustRegister(t, svc, "hash@example.com")

	raw := emailer.verifications[0]
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if _, ok := repo.verifications[raw]; ok {
		t.Fatal("raw token must not be a storage key")
	}
	if _, ok := repo.verifications[HashToken(raw)]; !ok {
		t.Fatal("hash of the emailed token must be stored")
	}
}

func TestEmailTokensAreBoundToPurpose(t *testing.T) {
	svc, _, emailer, _, _ := newTestService(t)
	ctx := context.Background()
	mustRegister(t, svc, "purpose@example.com")
	if err := svc.RequestPasswordReset(ctx, "purpose@example.com"); err != nil {
		t.Fatal(err)
	}
	verify, reset := emailer.verifications[0], emailer.resets[0]

	if err := svc.ResetPassword(ctx, verify, "brand-new-pw"); grpcCode(err) != codes.InvalidArgument {
		t.Fatalf("verification token as reset: got %v, want InvalidArgument", grpcCode(err))
	}
	if err := svc.VerifyEmail(ctx, reset); grpcCode(err) != codes.InvalidArgument {
		t.Fatalf("reset token as verification: got %v, want InvalidArgument", grpcCode(err))
	}
	// The mismatched attempts must not have burned either token.
	if err := svc.VerifyEmail(ctx, verify); err != nil {
		t.Fatalf("verification token was burned: %v", err)
	}
	if err := svc.ResetPassword(ctx, reset, "brand-new-pw"); err != nil {
		t.Fatalf("reset token was burned: %v", err)
	}
}

func TestResetPassword_RevokesSessionsAndVerifiesEmail(t *testing.T) {
	svc, repo, emailer, _, _ := newTestService(t)
	ctx := context.Background()
	uid := mustRegister(t, svc, "stolen@example.com")
	_, refreshA, _, _, _, err := svc.Login(ctx, "stolen@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	_, refreshB, _, _, _, err := svc.Login(ctx, "stolen@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := svc.RequestPasswordReset(ctx, "stolen@example.com"); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.ResetPassword(ctx, emailer.resets[0], "recovered-pw"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	for name, token := range map[string]string{"A": refreshA, "B": refreshB} {
		if _, _, _, err := svc.Refresh(ctx, token); err == nil {
			t.Errorf("session %s survived the password reset", name)
		}
	}
	if u, _ := repo.GetUserByID(ctx, uid); !u.EmailVerified {
		t.Error("completing a reset proves the mailbox; email must be verified")
	}
	if err := svc.ResetPassword(ctx, emailer.resets[1], "another-new-pw"); err == nil {
		t.Error("a second outstanding reset token must be invalidated")
	}
	if _, _, _, _, _, err := svc.Login(ctx, "stolen@example.com", "password1"); err == nil {
		t.Error("old password still works")
	}
}

func TestOAuthLink_ClaimsPasswordAccount(t *testing.T) {
	for _, verified := range []bool{false, true} {
		name := "unverified"
		if verified {
			name = "verified"
		}
		t.Run(name, func(t *testing.T) {
			svc, repo, emailer, exchanger, _ := newTestService(t)
			ctx := context.Background()
			uid := mustRegister(t, svc, "victim@example.com") // password set by whoever registered first
			_, attackerRefresh, _, _, _, err := svc.Login(ctx, "victim@example.com", "password1")
			if err != nil {
				t.Fatal(err)
			}
			if verified {
				if err := repo.SetEmailVerified(ctx, uid); err != nil {
					t.Fatal(err)
				}
			}
			exchanger.responses["c"] = OAuthUserInfo{Provider: "google", Subject: "owner", Email: "victim@example.com", EmailVerified: true}

			_, _, _, gotUID, _, isNew, err := svc.OAuthExchange(ctx, "google", "c", "")
			if err != nil || gotUID != uid || isNew {
				t.Fatalf("link: uid=%s new=%v err=%v", gotUID, isNew, err)
			}

			if _, _, _, _, _, err := svc.Login(ctx, "victim@example.com", "password1"); grpcCode(err) != codes.Unauthenticated {
				t.Errorf("the pre-link password must stop working, got %v", grpcCode(err))
			}
			if _, _, _, err := svc.Refresh(ctx, attackerRefresh); err == nil {
				t.Error("sessions opened before the link must be revoked")
			}
			if u, _ := repo.GetUserByID(ctx, uid); !u.EmailVerified || u.PasswordHash != nil {
				t.Errorf("want verified and passwordless, got verified=%v hash=%v", u.EmailVerified, u.PasswordHash != nil)
			}
			if err := svc.VerifyEmail(ctx, emailer.verifications[0]); err == nil {
				t.Error("outstanding email tokens must be invalidated by the claim")
			}
			// The owner can set their own password through the reset flow.
			if err := svc.RequestPasswordReset(ctx, "victim@example.com"); err != nil {
				t.Fatal(err)
			}
			if err := svc.ResetPassword(ctx, emailer.resets[len(emailer.resets)-1], "owners-own-pw"); err != nil {
				t.Fatalf("reset after claim: %v", err)
			}
		})
	}
}

func TestOAuthLink_PasswordlessAccountKeepsSessions(t *testing.T) {
	svc, _, _, exchanger, _ := newTestService(t)
	ctx := context.Background()
	exchanger.responses["g"] = OAuthUserInfo{Provider: "google", Subject: "g1", Email: "both@example.com", EmailVerified: true}
	exchanger.responses["a"] = OAuthUserInfo{Provider: "apple", Subject: "a1", Email: "both@example.com", EmailVerified: true}
	_, refresh, _, _, _, _, err := svc.OAuthExchange(ctx, "google", "g", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, _, err := svc.OAuthExchange(ctx, "apple", "a", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Refresh(ctx, refresh); err != nil {
		t.Fatalf("adding a provider must not log the owner out: %v", err)
	}
}

type gatedEmailSender struct {
	release chan struct{}
	mu      sync.Mutex
	sent    int
}

func (g *gatedEmailSender) wait() {
	<-g.release
	g.mu.Lock()
	g.sent++
	g.mu.Unlock()
}

func (g *gatedEmailSender) SendVerification(context.Context, string, string, string) error {
	g.wait()
	return nil
}

func (g *gatedEmailSender) SendPasswordReset(context.Context, string, string, string) error {
	g.wait()
	return nil
}

func TestEmailIsSentOffTheRequestPath(t *testing.T) {
	sender := &gatedEmailSender{release: make(chan struct{})}
	svc := NewService(newFakeRepo(), newTestSigner(t), &fakeOAuthExchanger{}, sender, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.Register(ctx, "slow@example.com", "password1", ""); err != nil {
			t.Error(err)
		}
		if err := svc.RequestPasswordReset(ctx, "slow@example.com"); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("requests blocked on a slow mail gateway")
	}

	close(sender.release)
	svc.Drain(ctx)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.sent != 2 {
		t.Fatalf("emails delivered after Drain: got %d, want 2", sender.sent)
	}
}

// racyRepo runs hook inside the first transaction that locks a user for sharing, as a
// concurrent reset or claim committing between the caller's read and its token insert.
type racyRepo struct {
	*fakeRepo
	hook func()
}

func (r *racyRepo) WithTx(ctx context.Context, fn func(Repository) error) error {
	return r.fakeRepo.WithTx(ctx, func(Repository) error { return fn(r) })
}

func (r *racyRepo) GetUserForShare(ctx context.Context, id string) (*User, error) {
	if h := r.hook; h != nil {
		r.hook = nil
		h()
	}
	return r.GetUserByID(ctx, id)
}

func TestLoginCannotOutliveAConcurrentPasswordChange(t *testing.T) {
	repo := &racyRepo{fakeRepo: newFakeRepo()}
	svc := NewService(repo, newTestSigner(t), &fakeOAuthExchanger{}, &fakeEmailSender{}, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}, WithSyncEmail())
	ctx := context.Background()
	uid := mustRegister(t, svc, "race@example.com")

	// The claim lands after Login verified the old password but before it mints a session.
	repo.hook = func() {
		repo.mu.Lock()
		repo.users[uid].PasswordHash = nil
		repo.mu.Unlock()
	}
	if _, _, _, _, _, err := svc.Login(ctx, "race@example.com", "password1"); grpcCode(err) != codes.Unauthenticated {
		t.Fatalf("got %v, want Unauthenticated", grpcCode(err))
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.refreshTokens) != 0 {
		t.Fatalf("a refresh token was minted against a stale password: %d", len(repo.refreshTokens))
	}
}

type panickyEmailSender struct{}

func (panickyEmailSender) SendVerification(context.Context, string, string, string) error {
	panic("smtp exploded")
}

func (panickyEmailSender) SendPasswordReset(context.Context, string, string, string) error {
	panic("smtp exploded")
}

func TestBackgroundEmailPanicDoesNotCrash(t *testing.T) {
	svc := NewService(newFakeRepo(), newTestSigner(t), &fakeOAuthExchanger{}, panickyEmailSender{}, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	if _, err := svc.Register(context.Background(), "boom@example.com", "password1", ""); err != nil {
		t.Fatal(err)
	}
	svc.Drain(context.Background())
}

func TestDrainIsBoundedAndStopsNewJobs(t *testing.T) {
	sender := &gatedEmailSender{release: make(chan struct{})}
	svc := NewService(newFakeRepo(), newTestSigner(t), &fakeOAuthExchanger{}, sender, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "stuck@example.com", "password1", ""); err != nil {
		t.Fatal(err)
	}

	drainCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	svc.Drain(drainCtx)
	if time.Since(start) > 5*time.Second {
		t.Fatal("Drain must give up when its context ends")
	}

	// After Drain, new jobs are dropped rather than racing the shutdown.
	if err := svc.RequestPasswordReset(ctx, "stuck@example.com"); err != nil {
		t.Fatal(err)
	}
	close(sender.release)
	svc.Drain(ctx)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.sent != 1 {
		t.Fatalf("only the pre-drain email may be sent, got %d", sender.sent)
	}
}

// slowLookupRepo blocks every email lookup until released.
type slowLookupRepo struct {
	*fakeRepo
	release chan struct{}
}

func (r slowLookupRepo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	<-r.release
	return r.fakeRepo.GetUserByEmail(ctx, email)
}

func TestPasswordResetRequestDoesNoAccountWorkInline(t *testing.T) {
	repo := slowLookupRepo{newFakeRepo(), make(chan struct{})}
	svc := NewService(repo, newTestSigner(t), &fakeOAuthExchanger{}, &fakeEmailSender{}, zap.NewNop(),
		auth.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})

	done := make(chan error, 1)
	go func() { done <- svc.RequestPasswordReset(context.Background(), "anyone@example.com") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request waited on the account lookup, so its timing reveals whether the account exists")
	}
	close(repo.release)
	svc.Drain(context.Background())
}
