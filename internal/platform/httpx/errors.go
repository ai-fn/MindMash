package httpx

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	apperrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/log"
)

// GatewayErrors sanitizes errors before next renders them (nil: the default handler). Gateway
// calls skip gRPC interceptors, so this is their only guard against leaking causes.
func GatewayErrors(next runtime.ErrorHandlerFunc) runtime.ServeMuxOption {
	if next == nil {
		next = runtime.DefaultHTTPErrorHandler
	}
	return runtime.WithErrorHandler(func(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		pub, logCause := apperrors.Public(err)
		if logCause {
			path := r.URL.Path
			if len(path) > maxLoggedPath {
				path = path[:maxLoggedPath]
			}
			log.L().Error("httpx: internal error", zap.String("method", r.Method), zap.String("path", path),
				zap.Error(err), zap.String("trace_id", trace.SpanContextFromContext(ctx).TraceID().String()))
		}
		next(ctx, mux, m, w, r, pub)
	})
}
