package grpcx

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Gateway calls are served in-process and bypass these; httpx counts them.
var (
	grpcRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_grpc_server_requests_total",
		Help: "gRPC requests by full method and status code.",
	}, []string{"method", "code"})
	grpcDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mindmesh_grpc_server_request_duration_seconds",
		Help:    "gRPC request latency by full method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method"})
)
