// Package grpcx provides a reusable gRPC server with health, reflection,
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
	UnaryInterceptors []grpc.UnaryServerInterceptor // applied in order (after the built-in recovery + logging)
	Register          func(*grpc.Server)            // registers application services; called in New
}

// Server wraps a *grpc.Server with lifecycle management.
type Server struct {
	grpcServer *grpc.Server
	cfg        Config
	health     *health.Server
}

// New builds a Server with recovery + logging interceptors prepended to
// opts.UnaryInterceptors, registers the health service and reflection, and
// calls opts.Register to register application services.
func New(cfg Config, opts Options) *Server {
	hs := health.NewServer()

	recoveryInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				log.L().Error("grpcx: panic recovered",
					zap.Any("panic", rec),
					zap.String("method", info.FullMethod),
					zap.String("stack", string(debug.Stack())),
				)
				err = status.Error(codes.Internal, "internal")
			}
		}()
		return handler(ctx, req)
	}

	loggingInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}
		log.L().Info("grpcx: request",
			zap.String("method", info.FullMethod),
			zap.String("duration_ms", fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)),
			zap.String("code", code.String()),
		)
		return resp, err
	}

	interceptors := append(
		[]grpc.UnaryServerInterceptor{recoveryInterceptor, loggingInterceptor},
		opts.UnaryInterceptors...,
	)

	gs := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptors...),
	)

	grpc_health_v1.RegisterHealthServer(gs, hs)
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	reflection.Register(gs)

	if opts.Register != nil {
		opts.Register(gs)
	}

	return &Server{
		grpcServer: gs,
		cfg:        cfg,
		health:     hs,
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
