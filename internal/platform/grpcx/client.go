package grpcx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// Dial uses insecure credentials (TLS is the mesh's job), defaults unary deadlines and pings idle
// connections so dead peers are noticed.
func Dial(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithKeepaliveParams(clientKeepalive),
		grpc.WithChainUnaryInterceptor(defaultDeadline),
	}
	conn, err := grpc.NewClient(target, append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("grpcx: dial %s: %w", target, err)
	}
	return conn, nil
}

const defaultCallTimeout = 10 * time.Second

// Must stay within the server's keepaliveEnforcement, or the server drops the connection for pinging too often.
var clientKeepalive = keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}

func defaultDeadline(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// RetryOn retries Unavailable on "pkg.Service" or "pkg.Service/Method"; list only idempotent calls
// (a lost reply may hide an applied attempt), and pass it once, as a later service config replaces it.
func RetryOn(names ...string) grpc.DialOption {
	if len(names) == 0 {
		return grpc.EmptyDialOption{}
	}
	type name struct {
		Service string `json:"service"`
		Method  string `json:"method,omitempty"`
	}
	ns := make([]name, len(names))
	for i, n := range names {
		svc, method, _ := strings.Cut(strings.TrimPrefix(n, "/"), "/")
		if svc == "" {
			panic("grpcx: RetryOn needs a service name; an empty one would match every method")
		}
		ns[i] = name{Service: svc, Method: method}
	}
	cfg, err := json.Marshal(map[string]any{
		"methodConfig": []any{map[string]any{
			"name": ns,
			"retryPolicy": map[string]any{
				"maxAttempts":          3,
				"initialBackoff":       "0.1s",
				"maxBackoff":           "1s",
				"backoffMultiplier":    2,
				"retryableStatusCodes": []string{"UNAVAILABLE"},
			},
		}},
		// Stops retrying once over ~10% of calls fail, so an outage isn't amplified.
		"retryThrottling": map[string]any{"maxTokens": 10, "tokenRatio": 0.1},
	})
	if err != nil {
		panic(err) // static shape; cannot fail
	}
	return grpc.WithDefaultServiceConfig(string(cfg))
}

// ForwardAuthInterceptor passes the caller's JWT on, so the callee authenticates the end user.
func ForwardAuthInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get("authorization"); len(vals) > 0 {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", vals[0])
			}
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
