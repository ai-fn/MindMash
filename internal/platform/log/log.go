package log

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey struct{}

var global atomic.Pointer[zap.Logger]

// Init initialises the package-level logger from config values.
// Call once at service startup.
func Init(serviceName, env, level string) error {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("log: invalid level %q: %w", level, err)
	}

	// Sync the old logger before replacing it.
	if old := global.Load(); old != nil {
		_ = old.Sync()
	}

	var cfg zap.Config
	if env == "prod" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)

	l, err := cfg.Build()
	if err != nil {
		return fmt.Errorf("log: failed to build logger: %w", err)
	}

	global.Store(l.With(
		zap.String("service", serviceName),
		zap.String("env", env),
	))
	return nil
}

// Replace swaps the package-level logger and returns a func restoring the previous one (tests).
func Replace(l *zap.Logger) (restore func()) {
	prev := global.Swap(l)
	return func() { global.Store(prev) }
}

// L returns the package-level logger.
func L() *zap.Logger {
	if l := global.Load(); l != nil {
		return l
	}
	// Fallback: return a no-op logger so callers never get a nil dereference
	// before Init is called.
	return zap.NewNop()
}

// FromContext returns a logger from ctx, falling back to the package-level logger.
// The returned logger includes a "trace_id" field if one was stored via WithTraceID.
func FromContext(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(contextKey{}).(*zap.Logger); ok && l != nil {
		return l
	}
	if l := global.Load(); l != nil {
		return l
	}
	return zap.NewNop()
}

// WithContext returns a new context carrying the given logger.
func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// WithTraceID returns a new context carrying the given trace ID added to the logger.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	l := FromContext(ctx).With(zap.String("trace_id", traceID))
	return WithContext(ctx, l)
}

// With returns a child of the package-level logger with the given fields.
func With(fields ...zap.Field) *zap.Logger {
	return L().With(fields...)
}
