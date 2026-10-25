package log

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestInitAndLevel(t *testing.T) {
	old := global.Load()
	t.Cleanup(func() { global.Store(old) })

	if err := Init("svc", "dev", "info"); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
	if L() == nil {
		t.Fatal("L() returned nil after Init()")
	}
}

func TestFromContextFallback(t *testing.T) {
	// Even without calling Init or WithContext, FromContext must return non-nil.
	// Reset global to simulate pre-Init state.
	prev := global.Load()
	global.Store(nil)
	t.Cleanup(func() { global.Store(prev) })

	l := FromContext(context.Background())
	if l == nil {
		t.Fatal("FromContext(Background()) returned nil without prior Init/WithContext")
	}
}

func TestWithTraceID(t *testing.T) {
	// Build an observable in-memory logger so we can inspect fields without
	// relying on stdout parsing.
	core, logs := observer.New(zapcore.DebugLevel)
	base := zap.New(core)

	// Inject the base logger into context then attach a trace-id.
	ctx := WithContext(context.Background(), base)
	ctx = WithTraceID(ctx, "trace-abc-123")

	// Retrieve and use the logger.
	l := FromContext(ctx)
	l.Info("hello")

	// Verify the "trace_id" field is present in the recorded log entry.
	entries := logs.All()
	if len(entries) == 0 {
		t.Fatal("expected at least one log entry")
	}

	var found bool
	for _, f := range entries[0].Context {
		if f.Key == "trace_id" && f.String == "trace-abc-123" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("trace_id field not found in log entry; fields: %v", entries[0].Context)
	}
}

// TestWithTraceIDOutput verifies trace_id appears in JSON output (belt-and-suspenders).
func TestWithTraceIDOutput(t *testing.T) {
	var buf bytes.Buffer
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(&buf), zapcore.DebugLevel)
	base := zap.New(core)

	ctx := WithContext(context.Background(), base)
	ctx = WithTraceID(ctx, "xyz-999")
	FromContext(ctx).Info("msg")

	if !strings.Contains(buf.String(), "xyz-999") {
		t.Errorf("trace_id not found in JSON output: %s", buf.String())
	}
}

// TestWith verifies that With returns a non-nil child logger with additional fields.
func TestWith(t *testing.T) {
	old := global.Load()
	t.Cleanup(func() { global.Store(old) })

	if err := Init("svc", "dev", "debug"); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
	l := With(zap.String("k", "v"))
	if l == nil {
		t.Fatal("With() returned nil")
	}
}

// TestServiceEnvFields verifies that service and env fields are present in log output.
func TestServiceEnvFields(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	l := zap.New(core)
	// Simulate what Init does: add service+env fields.
	l = l.With(zap.String("service", "testsvc"), zap.String("env", "test"))
	old := global.Load()
	global.Store(l)
	t.Cleanup(func() { global.Store(old) })

	L().Info("hello")
	require.Equal(t, 1, recorded.Len())
	fields := recorded.All()[0].ContextMap()
	assert.Equal(t, "testsvc", fields["service"])
	assert.Equal(t, "test", fields["env"])
}

func TestFromContext_AddsTraceID(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	t.Cleanup(Replace(zap.New(core)))
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))

	FromContext(ctx).Info("traced")
	FromContext(context.Background()).Info("untraced")

	entries := logs.All()
	require.Len(t, entries, 2)
	assert.Equal(t, tid.String(), entries[0].ContextMap()["trace_id"])
	assert.NotContains(t, entries[1].ContextMap(), "trace_id")
}

// Log shippers parse JSON; only a developer's terminal gets the console format.
func TestConfigFor_JSONOutsideDev(t *testing.T) {
	assert.Equal(t, "console", configFor("dev").Encoding)
	assert.Equal(t, "json", configFor("staging").Encoding)
	assert.Equal(t, "json", configFor("prod").Encoding)
}

func TestFromContext_StoredLoggerWins(t *testing.T) {
	stored := zap.NewNop()
	ctx := trace.ContextWithSpanContext(WithContext(context.Background(), stored), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1},
	}))
	if got := FromContext(ctx); got != stored {
		t.Fatalf("FromContext = %p, want stored logger %p", got, stored)
	}
}
