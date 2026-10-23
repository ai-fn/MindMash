package grpcx

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestServer_CountsRequestsByMethodAndCode(t *testing.T) {
	const check, watch = "/grpc.health.v1.Health/Check", "/grpc.health.v1.Health/Watch"
	count := func(method, code string) float64 {
		return testutil.ToFloat64(grpcRequests.WithLabelValues(method, code))
	}
	okBefore, notFoundBefore, internalBefore := count(check, "OK"), count(check, "NotFound"), count(watch, "Internal")

	conn := startServer(t, Options{StreamInterceptors: []grpc.StreamServerInterceptor{
		func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error { panic("boom") },
	}})
	hc := grpc_health_v1.NewHealthClient(conn)
	_, _ = hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	_, _ = hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "missing"})
	stream, err := hc.Watch(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()

	if d := count(check, "NotFound") - notFoundBefore; d != 1 {
		t.Errorf("Check NotFound delta = %v, want 1", d)
	}
	if d := count(check, "OK") - okBefore; d < 1 {
		t.Errorf("Check OK delta = %v, want >= 1", d)
	}
	if d := count(watch, "Internal") - internalBefore; d != 1 {
		t.Errorf("Watch Internal delta = %v, want 1 (a recovered stream panic is a server error)", d)
	}
	if n := testutil.CollectAndCount(grpcDuration, "mindmesh_grpc_server_request_duration_seconds"); n == 0 {
		t.Error("no duration series recorded")
	}
}
