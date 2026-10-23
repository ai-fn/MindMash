package httpx

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/log"
	"go.uber.org/zap"
)

// statusRecorder wraps http.ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader keeps the first final code: net/http ignores later calls and 1xx isn't final.
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader && code >= 200 {
		r.status, r.wroteHeader = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush and write deadlines (SSE).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Recoverer wraps next and converts panics into 500 responses, logging the panic.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.L().Error("httpx: panic recovered",
					zap.Any("panic", rec),
					zap.String("stack", string(debug.Stack())),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestLogger logs each request (method, path, status, duration) via platform/log.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.L().Info("httpx: request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rec.status),
			zap.String("duration_ms", fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)),
		)
	})
}

// Chain applies middlewares in order (first listed is outermost).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
