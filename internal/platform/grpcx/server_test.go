package grpcx

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// freeAddr finds a free TCP port on localhost, closes the listener, and
// returns the address string. There is an inherent TOCTOU race, but for
// tests on loopback this is acceptable.
func freeAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeAddr: %v", err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatalf("freeAddr close: %v", err)
	}
	return addr
}

// waitHealthy polls the health endpoint until the service reports SERVING or
// the deadline elapses, avoiding a fixed startup sleep that flakes under load.
func waitHealthy(t *testing.T, hc grpc_health_v1.HealthClient, service string) *grpc_health_v1.HealthCheckResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: service})
		if err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING {
			return resp
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("service %q did not become SERVING within deadline: %v", service, lastErr)
	return nil
}

func TestNewAndHealth(t *testing.T) {
	addr := freeAddr(t)
	cfg := Config{Addr: addr, ShutdownTimeout: 5 * time.Second}
	srv := New(cfg, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Start(ctx)
	}()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("conn.Close: %v", err)
		}
	})

	hc := grpc_health_v1.NewHealthClient(conn)
	resp := waitHealthy(t, hc, "")
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status: got %v, want SERVING", resp.Status)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Start did not return after context cancel")
	}
}

func TestSetServingStatus(t *testing.T) {
	addr := freeAddr(t)
	cfg := Config{Addr: addr, ShutdownTimeout: 5 * time.Second}
	srv := New(cfg, Options{})

	srv.SetServingStatus("myservice", true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Start(ctx)
	}()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("conn.Close: %v", err)
		}
	})

	hc := grpc_health_v1.NewHealthClient(conn)
	resp := waitHealthy(t, hc, "myservice")
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status for myservice: got %v, want SERVING", resp.Status)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Start did not return after context cancel")
	}
}
