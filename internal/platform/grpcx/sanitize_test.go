package grpcx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/log"
)

func TestServer_HidesInternalErrorsAndLogsThem(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(log.Replace(zap.New(core)))
	secret := errors.New("pq: password authentication failed for user mindmesh at 10.0.3.7")
	conn := startServer(t, Options{
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
				if r, ok := req.(*grpc_health_v1.HealthCheckRequest); ok && r.Service == "boom" {
					return nil, fmt.Errorf("repo: load: %w", secret)
				}
				return h(ctx, req)
			},
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{
			func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error {
				return fmt.Errorf("stream: %w", secret)
			},
		},
	})
	hc := grpc_health_v1.NewHealthClient(conn)
	internalCount := func() float64 {
		return testutil.ToFloat64(grpcRequests.WithLabelValues("/grpc.health.v1.Health/Check", "Internal"))
	}
	before := internalCount()

	_, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "boom"})
	if st := status.Convert(err); st.Code() != codes.Internal || strings.Contains(st.Message(), "10.0.3.7") {
		t.Fatalf("unary: got %v %q, want Internal without the cause", st.Code(), st.Message())
	}
	if d := internalCount() - before; d != 1 {
		t.Fatalf("metrics must see the sanitized code (Internal, not Unknown), delta = %v", d)
	}
	stream, err := hc.Watch(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	if st := status.Convert(err); st.Code() != codes.Internal || strings.Contains(st.Message(), "10.0.3.7") {
		t.Fatalf("stream: got %v %q, want Internal without the cause", st.Code(), st.Message())
	}

	var logged int
	for _, e := range logs.FilterMessage("grpcx: internal error").All() {
		if strings.Contains(e.ContextMap()["error"].(string), "10.0.3.7") {
			logged++
		}
	}
	if logged != 2 {
		t.Fatalf("want both causes logged server-side, got %d: %v", logged, logs.All())
	}
}
