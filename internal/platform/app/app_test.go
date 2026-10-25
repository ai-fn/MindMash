package app

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/config"
)

func devEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ENV", "dev")
	t.Setenv("OTEL_SAMPLING_RATE", "0")
}

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func TestRun_RunsAfterSetupThenClosesInReverse(t *testing.T) {
	devEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rec recorder
	var started sync.WaitGroup
	started.Add(2)

	err := Run(ctx, "svc", func(_ context.Context, a *App) error {
		a.OnClose(func() { rec.add("close pool") })
		for _, name := range []string{"grpc", "http"} {
			a.Go(func(ctx context.Context) error {
				rec.add("run " + name)
				started.Done()
				<-ctx.Done()
				return nil
			})
		}
		a.OnClose(func() { rec.add("close producer") })
		go func() { started.Wait(); cancel() }()
		rec.add("setup done")
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := rec.get()
	if got[0] != "setup done" {
		t.Fatalf("a run started before setup returned: %v", got)
	}
	if !slices.Equal(got[len(got)-2:], []string{"close producer", "close pool"}) {
		t.Fatalf("closers ran out of order: %v", got)
	}
}

func TestRun_SetupErrorStillCloses(t *testing.T) {
	devEnv(t)
	var rec recorder
	boom := errors.New("boom")
	err := Run(context.Background(), "svc", func(_ context.Context, a *App) error {
		a.OnClose(func() { rec.add("close") })
		a.Go(func(context.Context) error { rec.add("run"); return nil })
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want boom", err)
	}
	if got := rec.get(); !slices.Equal(got, []string{"close"}) {
		t.Fatalf("events = %v, want only the closer", got)
	}
}

func TestRun_FailingRunStopsTheOthers(t *testing.T) {
	devEnv(t)
	boom := errors.New("listen failed")
	err := Run(context.Background(), "svc", func(_ context.Context, a *App) error {
		a.Go(func(ctx context.Context) error { <-ctx.Done(); return nil })
		a.Go(func(context.Context) error { return boom })
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want listen failed", err)
	}
}

func TestRun_RejectsMissingEnv(t *testing.T) {
	t.Setenv("ENV", "")
	os.Unsetenv("ENV")
	err := Run(context.Background(), "svc", func(context.Context, *App) error {
		t.Fatal("setup ran without ENV")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "ENV") {
		t.Fatalf("Run error = %v, want one naming ENV", err)
	}
}

func TestMigrate_OnlyWhenEnabled(t *testing.T) {
	// The DSN is unreachable, so an error means Migrate tried to run.
	const dsn = "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	cases := []struct {
		env, flag string
		wantRun   bool
		wantErr   bool
	}{
		{env: "prod", wantRun: false},
		{env: "dev", wantRun: true},
		{env: "dev", flag: "false", wantRun: false},
		{env: "prod", flag: "true", wantRun: true},
		{env: "prod", flag: "yes please", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.env+"/"+tc.flag, func(t *testing.T) {
			if tc.flag == "" {
				t.Setenv("RUN_MIGRATIONS", "")
				os.Unsetenv("RUN_MIGRATIONS")
			} else {
				t.Setenv("RUN_MIGRATIONS", tc.flag)
			}
			a := &App{Name: "svc", Base: &config.BaseConfig{Env: tc.env}, Log: zap.NewNop()}
			err := a.Migrate(context.Background(), dsn, "platform")
			if ran := err != nil && strings.Contains(err.Error(), "run migrations"); ran != tc.wantRun {
				t.Fatalf("ran = %v (err %v), want %v", ran, err, tc.wantRun)
			}
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "RUN_MIGRATIONS")) {
				t.Fatalf("err = %v, want one naming RUN_MIGRATIONS", err)
			}
		})
	}
}

func TestVerifier_LocalKeysOnlyInDev(t *testing.T) {
	t.Setenv("JWKS_URL", "")
	os.Unsetenv("JWKS_URL")
	a := &App{Name: "svc", Base: &config.BaseConfig{Env: "prod"}, Log: zap.NewNop()}
	if _, err := a.Verifier(context.Background()); err == nil || !strings.Contains(err.Error(), "JWKS_URL") {
		t.Fatalf("Verifier error = %v, want one naming JWKS_URL", err)
	}
}
