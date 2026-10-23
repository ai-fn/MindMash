package grpcx

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestServer_ReportsNotServingWhileDraining(t *testing.T) {
	addr := freeAddr(t)
	srv := New(Config{Addr: addr, ShutdownTimeout: time.Second, DrainDelay: time.Second}, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hc := grpc_health_v1.NewHealthClient(conn)
	waitHealthy(t, hc, "")

	stoppedAt := time.Now()
	cancel()
	deadline := time.Now().Add(700 * time.Millisecond)
	for {
		cctx, ccancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		resp, err := hc.Check(cctx, &grpc_health_v1.HealthCheckRequest{})
		ccancel()
		if err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_NOT_SERVING {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want NOT_SERVING while still accepting calls during the drain, got %v, %v", resp, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d := time.Since(stoppedAt); d < time.Second {
		t.Fatalf("stopped after %v, before the drain delay", d)
	}
}
