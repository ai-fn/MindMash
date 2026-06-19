package grpcx

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Dial opens a gRPC client connection to target using insecure credentials
// (mTLS / service-mesh TLS is handled at the infrastructure layer in prod).
// Additional dial options may be supplied via opts.
func Dial(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.NewClient(target, append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("grpcx: dial %s: %w", target, err)
	}
	return conn, nil
}

// ForwardAuthInterceptor returns a client-side unary interceptor that copies
// the incoming "authorization" metadata from the server context onto the
// outgoing context. This propagates the end-user JWT when the Scheduler calls
// Collection's internal gRPC API, so Collection's auth interceptor sees a
// valid identity without the Scheduler needing its own service account.
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
