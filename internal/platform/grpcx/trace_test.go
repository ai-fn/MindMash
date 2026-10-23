package grpcx

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestTraceContextPropagatesClientToServer(t *testing.T) {
	sr := testhelpers.RecordSpans(t)
	addr := freeAddr(t)
	srv := New(Config{Addr: addr, ShutdownTimeout: time.Second}, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	conn, err := Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hc := grpc_health_v1.NewHealthClient(conn)
	waitHealthy(t, hc, "")

	callCtx, caller := otel.Tracer("test").Start(ctx, "caller")
	if _, err := hc.Check(callCtx, &grpc_health_v1.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	caller.End()

	kinds := map[trace.SpanKind]bool{}
	for _, s := range sr.Ended() {
		if s.SpanContext().TraceID() == caller.SpanContext().TraceID() {
			kinds[s.SpanKind()] = true
		}
	}
	if !kinds[trace.SpanKindClient] || !kinds[trace.SpanKindServer] {
		t.Fatalf("spans in caller's trace by kind = %v, want client and server", kinds)
	}
}
