package httpx

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/log"
)

// GatewayErrors sanitizes errors before next renders them (nil: the default handler). Gateway
// calls skip gRPC interceptors, so this is their only guard against leaking causes.
func GatewayErrors(next runtime.ErrorHandlerFunc) runtime.ServeMuxOption {
	if next == nil {
		next = runtime.DefaultHTTPErrorHandler
	}
	return runtime.WithErrorHandler(func(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		pub, logCause := apperr.Public(err)
		if logCause {
			path := r.URL.Path
			if len(path) > maxLoggedPath {
				path = path[:maxLoggedPath]
			}
			log.FromContext(ctx).Error("httpx: internal error", zap.String("method", r.Method), zap.String("path", path),
				zap.Error(err))
		}
		next(ctx, mux, m, w, r, pub)
	})
}
