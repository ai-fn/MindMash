package grpcx

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const healthCheck = "grpc.health.v1.Health/Check"

// probe intercepts Health/Check for the "probe" service, leaving the health checks dialServer makes alone.
func probe(fn func(ctx context.Context) error) Options {
	return Options{UnaryInterceptors: []grpc.UnaryServerInterceptor{
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if r, ok := req.(*grpc_health_v1.HealthCheckRequest); ok && r.GetService() == "probe" {
				if err := fn(ctx); err != nil {
					return nil, err
				}
				return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
			}
			return h(ctx, req)
		},
	}}
}

func checkProbe(ctx context.Context, conn *grpc.ClientConn) error {
	_, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "probe"})
	return err
}

func TestDial_BoundsCallsWithoutDeadline(t *testing.T) {
	var got atomic.Int64
	conn := dialServer(t, probe(func(ctx context.Context) error {
		if d, ok := ctx.Deadline(); ok {
			got.Store(int64(time.Until(d)))
		}
		return nil
	}), func(addr string) (*grpc.ClientConn, error) { return Dial(context.Background(), addr) })

	if err := checkProbe(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if left := time.Duration(got.Load()); left <= defaultCallTimeout-time.Second || left > defaultCallTimeout {
		t.Errorf("server saw %v left, want ~%v", left, defaultCallTimeout)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := checkProbe(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if left := time.Duration(got.Load()); left > 2*time.Second {
		t.Errorf("server saw %v left, want the caller's 2s deadline kept", left)
	}
}

func TestDial_RetriesUnavailableOnlyForListedMethods(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retry    []string
		wantCode codes.Code
		attempts int64
	}{
		{"listed method", []string{"/" + healthCheck}, codes.OK, 2},
		{"listed service", []string{"grpc.health.v1.Health"}, codes.OK, 2},
		{"other method", []string{"grpc.health.v1.Health/Watch"}, codes.Unavailable, 1},
		{"none", nil, codes.Unavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int64
			conn := dialServer(t, probe(func(context.Context) error {
				if attempts.Add(1) == 1 {
					return status.Error(codes.Unavailable, "restarting")
				}
				return nil
			}), func(addr string) (*grpc.ClientConn, error) {
				return Dial(context.Background(), addr, RetryOn(tc.retry...))
			})

			if code := status.Code(checkProbe(context.Background(), conn)); code != tc.wantCode {
				t.Errorf("code = %v, want %v", code, tc.wantCode)
			}
			if n := attempts.Load(); n != tc.attempts {
				t.Errorf("attempts = %d, want %d", n, tc.attempts)
			}
		})
	}
}

func TestDial_DoesNotRetryOtherCodes(t *testing.T) {
	var attempts atomic.Int64
	conn := dialServer(t, probe(func(context.Context) error {
		attempts.Add(1)
		return status.Error(codes.Internal, "boom")
	}), func(addr string) (*grpc.ClientConn, error) {
		return Dial(context.Background(), addr, RetryOn(healthCheck))
	})

	if code := status.Code(checkProbe(context.Background(), conn)); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

func TestDial_KeepaliveWithinServerPolicy(t *testing.T) {
	if clientKeepalive.Time < keepaliveEnforcement.MinTime {
		t.Errorf("client pings every %v, server allows no more often than %v", clientKeepalive.Time, keepaliveEnforcement.MinTime)
	}
	if clientKeepalive.PermitWithoutStream && !keepaliveEnforcement.PermitWithoutStream {
		t.Error("client pings idle connections, server forbids it")
	}
}
