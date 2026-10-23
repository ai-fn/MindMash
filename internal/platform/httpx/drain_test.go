package httpx

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestServer_FailsReadinessWhileDraining(t *testing.T) {
	cfg := DefaultConfig(freeAddr(t))
	cfg.DrainDelay = time.Second
	srv := New(cfg, Options{Handler: http.NotFoundHandler()})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	// No keep-alive: Shutdown waits up to 5s on a spare client connection that never sent a request.
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	get := func(path string) int {
		resp, err := client.Get("http://" + cfg.Addr + path)
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	deadline := time.Now().Add(3 * time.Second)
	for get("/readyz") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("server never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stoppedAt := time.Now()
	cancel()
	time.Sleep(100 * time.Millisecond)
	if got := get("/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining = %d, want 503 so load balancers stop routing here", got)
	}
	if got := get("/healthz"); got != http.StatusOK {
		t.Fatalf("healthz while draining = %d, want 200: draining is not dead", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d := time.Since(stoppedAt); d < time.Second {
		t.Fatalf("stopped after %v, before the drain delay", d)
	}
}

func TestCacheReady_CachesSuccessOnly(t *testing.T) {
	var calls atomic.Int32
	fail := true
	check := CacheReady(time.Hour, func(context.Context) error {
		calls.Add(1)
		if fail {
			return errors.New("down")
		}
		return nil
	})
	ctx := context.Background()
	for range 2 {
		if check(ctx) == nil {
			t.Fatal("failures must surface")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("failures must not be cached: %d calls", calls.Load())
	}
	fail = false
	for range 2 {
		if err := check(ctx); err != nil {
			t.Fatalf("want ready: %v", err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("a success must be reused within the TTL: %d calls", calls.Load())
	}
}
