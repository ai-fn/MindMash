package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/log"
)

const defaultMaxBodyBytes = 4 << 20 // matches gRPC's default max receive size

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

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush and write deadlines (SSE).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

const (
	maxLoggedPath = 256
	// statusNoResponse records an abort before any header was sent (nginx's "client closed request").
	statusNoResponse = 499
)

// observe recovers panics, then records metrics and logs every app request.
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := unrouted
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			p := recover()
			abort := p != nil
			switch {
			case p == http.ErrAbortHandler: // the handler chose to cut the response; not a crash
				if !rec.wroteHeader {
					rec.status = statusNoResponse
				}
			case p != nil:
				log.L().Error("httpx: panic recovered", zap.Any("panic", p), zap.String("method", r.Method),
					zap.String("route", route), zap.String("stack", string(debug.Stack())))
				if !rec.wroteHeader {
					h := rec.Header()
					for _, k := range []string{"Content-Length", "Content-Encoding", "ETag", "Last-Modified"} {
						h.Del(k)
					}
					h.Set("Content-Type", "application/json")
					rec.WriteHeader(http.StatusInternalServerError)
					_, _ = rec.Write([]byte(`{"error":"internal"}`))
					abort = false
				}
				rec.status = http.StatusInternalServerError
			}
			record(r, route, rec.status, time.Since(start))
			if abort {
				// otelhttp never reaches its own status handling once we unwind through it.
				trace.SpanFromContext(r.Context()).SetStatus(codes.Error, "response aborted")
				// Headers may be out: abort so the client sees a broken response, not a clean truncated one.
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), routeKey{}, &route)))
	})
}

func record(r *http.Request, route string, status int, elapsed time.Duration) {
	method := metricMethod(r.Method)
	httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())

	fields := []zap.Field{
		zap.String("method", r.Method),
		zap.String("route", route),
		zap.Int("status", status),
		zap.String("duration_ms", fmt.Sprintf("%.2f", float64(elapsed.Microseconds())/1000.0)),
	}
	// Routed paths carry ids and add nothing over the template; unrouted ones are attacker-chosen.
	if route == unrouted {
		path := r.URL.Path
		if len(path) > maxLoggedPath {
			path = path[:maxLoggedPath]
		}
		fields = append(fields, zap.String("path", path))
	}
	level := zap.InfoLevel
	if status >= 500 {
		level = zap.WarnLevel
	}
	log.L().Log(level, "httpx: request", fields...)
}

// limitBody rejects declared oversized bodies up front and caps chunked ones while read.
func limitBody(max int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > max {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"request body too large"}`))
			return
		}
		body := &cappedBody{ReadCloser: http.MaxBytesReader(w, r.Body, max)}
		r.Body = body
		next.ServeHTTP(&overflowWriter{ResponseWriter: w, body: body}, r)
	})
}

type cappedBody struct {
	io.ReadCloser
	overflowed bool
}

func (b *cappedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		b.overflowed = true
	}
	return n, err
}

// overflowWriter turns the 400 the gateway makes of any decode error into 413 when the cap was hit.
type overflowWriter struct {
	http.ResponseWriter
	body *cappedBody
}

func (w *overflowWriter) WriteHeader(code int) {
	if code == http.StatusBadRequest && w.body.overflowed {
		code = http.StatusRequestEntityTooLarge
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *overflowWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
