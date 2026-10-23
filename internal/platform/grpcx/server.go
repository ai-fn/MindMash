// Package grpcx provides a reusable gRPC server with health, optional reflection,
// interceptors, and graceful shutdown.
package grpcx

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/log"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// Config holds gRPC server settings.
type Config struct {
	Addr            string        // listen address, e.g. ":50051"
	ShutdownTimeout time.Duration // default 15s
}

// DefaultConfig returns sane defaults for addr.
func DefaultConfig(addr string) Config {
	return Config{
		Addr:            addr,
		ShutdownTimeout: 15 * time.Second,
	}
}

// Options configure the gRPC server.
type Options struct {
	UnaryInterceptors  []grpc.UnaryServerInterceptor  // applied in order (after the built-in recovery + logging)
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

// New builds a Server with recovery + logging interceptors prepended to
// opts.Unary/StreamInterceptors, registers the health service (and reflection
// when enabled), and calls opts.Register to register application services.
func New(cfg Config, opts Options) *Server {
	hs := health.NewServer()

	recoveryInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer recoverPanic(info.FullMethod, &err)
		return handler(ctx, req)
	}
	streamRecoveryInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer recoverPanic(info.FullMethod, &err)
		return handler(srv, ss)
	}

	loggingInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logRequest(info.FullMethod, start, err)
		return resp, err
	}
	streamLoggingInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		logRequest(info.FullMethod, start, err)
		return err
	}

	unary := append(
		[]grpc.UnaryServerInterceptor{recoveryInterceptor, loggingInterceptor},
		opts.UnaryInterceptors...,
	)
	stream := append(
		[]grpc.StreamServerInterceptor{streamRecoveryInterceptor, streamLoggingInterceptor},
		opts.StreamInterceptors...,
	)

	gs := grpc.NewServer(
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

func recoverPanic(method string, err *error) {
	if rec := recover(); rec != nil {
		log.L().Error("grpcx: panic recovered",
			zap.Any("panic", rec),
			zap.String("method", method),
			zap.String("stack", string(debug.Stack())),
		)
		*err = status.Error(codes.Internal, "internal")
	}
}

func logRequest(method string, start time.Time, err error) {
	code := codes.OK
	if err != nil {
		if st, ok := status.FromError(err); ok {
			code = st.Code()
		} else {
			code = codes.Unknown
		}
	}
	log.L().Info("grpcx: request",
		zap.String("method", method),
		zap.String("duration_ms", fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)),
		zap.String("code", code.String()),
	)
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
		if err == grpc.ErrServerStopped {
			return nil
		}
		return fmt.Errorf("grpcx: Serve: %w", err)
	case <-ctx.Done():
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
