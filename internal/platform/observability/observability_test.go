package observability

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestLoadConfig_Defaults(t *testing.T) {
	testhelpers.UnsetAll(t,
		"OTEL_SERVICE_NAME",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_SAMPLING_RATE",
		"METRICS_ADDR",
	)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "mindmesh", cfg.ServiceName)
	assert.Equal(t, "http://localhost:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 1.0, cfg.SamplingRate)
	assert.Equal(t, ":9090", cfg.MetricsAddr)
}

func TestLoadConfig_InvalidSamplingRate(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"below zero", "-0.1"},
		{"above one", "1.1"},
		{"far below", "-100"},
		{"far above", "2.5"},
		{"not a number", "bad"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SAMPLING_RATE", tc.value)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "OTEL_SAMPLING_RATE")
		})
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "my-service")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4317")
	t.Setenv("OTEL_SAMPLING_RATE", "0.5")
	t.Setenv("METRICS_ADDR", ":9100")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "my-service", cfg.ServiceName)
	assert.Equal(t, "http://otel-collector:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.Equal(t, ":9100", cfg.MetricsAddr)
}

type fakeCollector struct {
	coltracepb.UnimplementedTraceServiceServer
	spans chan string
}

func (c *fakeCollector) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				c.spans <- s.GetName()
			}
		}
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

func TestInitTracer_ExportsToEndpointURL(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	col := &fakeCollector{spans: make(chan string, 8)}
	srv := grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(srv, col)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })

	ctx := context.Background()
	shutdown, err := InitTracer(ctx, Config{ServiceName: "test", OTLPEndpoint: "http://" + lis.Addr().String(), SamplingRate: 1})
	require.NoError(t, err)
	_, span := Tracer("test").Start(ctx, "exported")
	span.End()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, shutdown(sctx))

	select {
	case name := <-col.spans:
		assert.Equal(t, "exported", name)
	default:
		t.Fatal("collector received no span")
	}
	assert.Contains(t, otel.GetTextMapPropagator().Fields(), "traceparent")
	assert.NotContains(t, otel.GetTextMapPropagator().Fields(), "baggage", "client baggage would be copied into every outbox row")
}

func TestLoadConfig_RejectsEndpointWithoutURLScheme(t *testing.T) {
	for _, v := range []string{"jaeger:4317", "localhost:4317", "ftp://jaeger:4317", "http://", "://x"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", v)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT")
		})
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel-collector:4317")
	_, err := LoadConfig()
	require.NoError(t, err)
}

func TestSampler_FollowsParentDecision(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	parent := func(flags trace.TraceFlags) context.Context {
		return trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: spanID, TraceFlags: flags, Remote: true,
		}))
	}
	params := func(ctx context.Context) sdktrace.SamplingParameters {
		return sdktrace.SamplingParameters{ParentContext: ctx, TraceID: traceID, Name: "child"}
	}

	assert.Equal(t, sdktrace.RecordAndSample, sampler(0).ShouldSample(params(parent(trace.FlagsSampled))).Decision,
		"a sampled upstream trace must not be broken by the local ratio")
	assert.Equal(t, sdktrace.Drop, sampler(1).ShouldSample(params(parent(0))).Decision,
		"an unsampled upstream trace stays unsampled")
}
