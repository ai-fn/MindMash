package httpx

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_http_server_requests_total",
		Help: "Application HTTP requests by method, route template and status code.",
	}, []string{"method", "route", "code"})
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mindmesh_http_server_request_duration_seconds",
		Help:    "Application HTTP request latency by method and route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// unrouted labels requests no route claimed: unknown paths and rejections before routing (e.g. auth).
const unrouted = "unrouted"

type routeKey struct{}

// SetRoute names the matched route for metrics; it must be a template, never a raw path.
func SetRoute(ctx context.Context, route string) {
	if p, ok := ctx.Value(routeKey{}).(*string); ok {
		*p = route
	}
}

// Route labels h's requests with route, for handlers mounted outside the gateway.
func Route(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), route)
		h.ServeHTTP(w, r)
	})
}

// GatewayRoutes labels gateway requests with their path template.
func GatewayRoutes() runtime.ServeMuxOption {
	return runtime.WithMiddlewares(func(next runtime.HandlerFunc) runtime.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request, params map[string]string) {
			if pat, ok := runtime.HTTPPattern(r.Context()); ok {
				SetRoute(r.Context(), strings.ReplaceAll(pat.String(), "=*}", "}"))
			}
			next(w, r, params)
		}
	})
}

func recordMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := unrouted
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), routeKey{}, &route)))
		method := metricMethod(r.Method)
		httpRequests.WithLabelValues(method, route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}

// metricMethod folds non-standard methods so clients can't mint series.
func metricMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}
