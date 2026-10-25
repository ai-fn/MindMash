// Package grpcx provides a reusable gRPC server with health, optional reflection,
// interceptors, and graceful shutdown.
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// Config holds gRPC server settings.
type Config struct {
	Addr            string        // listen address, e.g. ":50051"
	ShutdownTimeout time.Duration // default 15s
	DrainDelay      time.Duration // default 5s
}

// DefaultConfig returns sane defaults for addr.
func DefaultConfig(addr string) Config {
	return Config{
		Addr:            addr,
		ShutdownTimeout: 15 * time.Second,
		DrainDelay:      5 * time.Second,
	}
}

// The default (5m, no idle pings) would reject Dial's keepalive pings.
var keepaliveEnforcement = keepalive.EnforcementPolicy{MinTime: 15 * time.Second, PermitWithoutStream: true}

// Options configure the gRPC server.
type Options struct {
	UnaryInterceptors  []grpc.UnaryServerInterceptor  // applied in order (after the built-in logging/metrics, sanitizing, recovery)
	StreamInterceptors []grpc.StreamServerInterceptor // same, for streaming RPCs
	Register           func(*grpc.Server)             // registers application services; called in New
	Reflection         bool                           // dev only: exposes the full API schema unauthenticated
}

// Server wraps a *grpc.Server with lifecycle management.
type Server struct {
	grpcServer *grpc.Server
	cfg        Config
	health     *health.Server
}

// New builds a Server with logging/metrics, error sanitizing and recovery interceptors prepended to
// opts.Unary/StreamInterceptors, registers the health service (and reflection
// when enabled), and calls opts.Register to register application services.
func New(cfg Config, opts Options) *Server {
	hs := health.NewServer()

	recoveryInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer recoverPanic(ctx, info.FullMethod, &err)
		return handler(ctx, req)
	}
	streamRecoveryInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer recoverPanic(ss.Context(), info.FullMethod, &err)
		return handler(srv, ss)
	}

	// Outside recovery so a recovered panic is observed as Internal.
	observeInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		observe(ctx, info.FullMethod, start, err)
		return resp, err
	}
	streamObserveInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		observe(ss.Context(), info.FullMethod, start, err)
		return err
	}

	sanitizeInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, sanitize(ctx, info.FullMethod, err)
	}
	streamSanitizeInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return sanitize(ss.Context(), info.FullMethod, handler(srv, ss))
	}

	unary := append(
		[]grpc.UnaryServerInterceptor{observeInterceptor, sanitizeInterceptor, recoveryInterceptor},
		opts.UnaryInterceptors...,
	)
	stream := append(
		[]grpc.StreamServerInterceptor{streamObserveInterceptor, streamSanitizeInterceptor, streamRecoveryInterceptor},
		opts.StreamInterceptors...,
	)

	gs := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepaliveEnforcement),
		// Clients behind a ClusterIP stick to one pod per connection; aging them out rebalances.
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionAge: 5 * time.Minute, MaxConnectionAgeGrace: 30 * time.Second}),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)

	grpc_health_v1.RegisterHealthServer(gs, hs)
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	if opts.Reflection {
		reflection.Register(gs)
	}

	if opts.Register != nil {
		opts.Register(gs)
	}

	return &Server{
		grpcServer: gs,
		cfg:        cfg,
		health:     hs,
	}
}

func recoverPanic(ctx context.Context, method string, err *error) {
	if rec := recover(); rec != nil {
		log.FromContext(ctx).Error("grpcx: panic recovered",
			zap.Any("panic", rec),
			zap.String("method", method),
			zap.String("stack", string(debug.Stack())),
		)
		*err = status.Error(codes.Internal, "internal")
	}
}

// sanitize keeps causes server-side: clients get a fixed message for server faults.
func sanitize(ctx context.Context, method string, err error) error {
	pub, logCause := apperr.Public(err)
	if logCause {
		log.FromContext(ctx).Error("grpcx: internal error", zap.String("method", method), zap.Error(err))
	}
	return pub
}

func observe(ctx context.Context, method string, start time.Time, err error) {
	elapsed := time.Since(start)
	code := status.Code(err)
	grpcRequests.WithLabelValues(method, code.String()).Inc()
	grpcDuration.WithLabelValues(method).Observe(elapsed.Seconds())
	log.FromContext(ctx).Log(logLevel(method, code), "grpcx: request",
		zap.String("method", method),
		zap.String("duration_ms", fmt.Sprintf("%.2f", float64(elapsed.Microseconds())/1000.0)),
		zap.String("code", code.String()),
	)
}

// logLevel raises the codes the gateway answers with a 5xx and keeps healthy probe traffic at Debug.
func logLevel(method string, code codes.Code) zapcore.Level {
	switch {
	case code == codes.Internal, code == codes.Unknown, code == codes.DataLoss,
		code == codes.Unimplemented, code == codes.Unavailable, code == codes.DeadlineExceeded:
		return zapcore.WarnLevel
	case strings.HasPrefix(method, "/grpc.health.v1.Health/"):
		return zapcore.DebugLevel
	default:
		return zapcore.InfoLevel
	}
}

// Start begins serving on cfg.Addr and blocks until ctx is cancelled, then
// gracefully stops within cfg.ShutdownTimeout (falling back to Stop()).
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("grpcx: listen on %s: %w", s.cfg.Addr, err)
	}

	log.L().Info("grpcx: starting server", zap.String("addr", lis.Addr().String()))

	serveErr := make(chan error, 1)
	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return fmt.Errorf("grpcx: Serve: %w", err)
	case <-ctx.Done():
		// NOT_SERVING first, then keep serving so health-checking clients and LBs move away before we stop.
		s.health.Shutdown()
		log.L().Info("grpcx: draining", zap.Duration("delay", s.cfg.DrainDelay))
		time.Sleep(s.cfg.DrainDelay)
		log.L().Info("grpcx: shutting down server")

		stopped := make(chan struct{})
		go func() {
			s.grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
			return nil
		case <-time.After(s.cfg.ShutdownTimeout):
			// Graceful shutdown timed out; force stop.
			s.grpcServer.Stop()
			return nil
		}
	}
}

// SetServingStatus updates the health status for a service name ("" = whole server).
func (s *Server) SetServingStatus(service string, serving bool) {
	if serving {
		s.health.SetServingStatus(service, grpc_health_v1.HealthCheckResponse_SERVING)
	} else {
		s.health.SetServingStatus(service, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
}
