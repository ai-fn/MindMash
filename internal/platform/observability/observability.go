// Package observability sets up OpenTelemetry tracing and Prometheus metrics.
package observability

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds observability configuration for a single service instance.
type Config struct {
	ServiceName  string  // OTEL_SERVICE_NAME, default "mindmesh"
	OTLPEndpoint string  // OTEL_EXPORTER_OTLP_ENDPOINT, default "http://localhost:4317"
	SamplingRate float64 // OTEL_SAMPLING_RATE, 0.0–1.0, default 1.0
	MetricsAddr  string  // METRICS_ADDR, default ":9090"
}

// LoadConfig reads observability configuration from environment variables.
// It returns an error when OTEL_SAMPLING_RATE is set but outside [0.0, 1.0].
func LoadConfig() (Config, error) {
	cfg := Config{
		ServiceName:  envStr("OTEL_SERVICE_NAME", "mindmesh"),
		OTLPEndpoint: envStr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		SamplingRate: 1.0,
		MetricsAddr:  envStr("METRICS_ADDR", ":9090"),
	}

	if v, ok := os.LookupEnv("OTEL_SAMPLING_RATE"); ok {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return Config{}, fmt.Errorf("observability: OTEL_SAMPLING_RATE: cannot parse %q as float64: %w", v, err)
		}
		if rate < 0.0 || rate > 1.0 {
			return Config{}, fmt.Errorf("observability: OTEL_SAMPLING_RATE must be in [0.0, 1.0], got %v", rate)
		}
		cfg.SamplingRate = rate
	}

	return cfg, nil
}

// InitTracer configures and registers a global OTLP gRPC TracerProvider.
// The returned shutdown function must be called during graceful shutdown to
// flush pending spans before the process exits.
func InitTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SamplingRate)),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// InitMetrics returns an http.Handler that serves Prometheus metrics on /metrics.
func InitMetrics() http.Handler {
	return promhttp.Handler()
}

// Tracer returns a named tracer from the globally registered TracerProvider.
// Call InitTracer first so the global provider is set; otherwise this returns
// the no-op tracer installed by default.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// envStr returns the env var value for key, or def when the variable is unset.
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
