package events

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	outboxOldestAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mindmesh_outbox_oldest_unpublished_seconds",
		Help: "Age of the oldest unpublished outbox row, reported by the relay leader.",
	}, []string{"store"})
	outboxPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_outbox_published_total",
		Help: "Outbox rows published to Kafka and marked.",
	}, []string{"store"})
	outboxPurged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_outbox_purged_total",
		Help: "Published outbox rows deleted by retention.",
	}, []string{"store"})
	consumerHandlerFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_consumer_handler_failures_total",
		Help: "Kafka handler attempts that returned an error.",
	}, []string{"topic", "group"})
	consumerDeadLettered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_consumer_dead_lettered_total",
		Help: "Messages moved to <topic>.dlq after exhausting handler retries.",
	}, []string{"topic", "group"})
	consumerDLQWriteFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mindmesh_consumer_dlq_write_failures_total",
		Help: "Failed writes to <topic>.dlq; the partition is blocked until one succeeds.",
	}, []string{"topic", "group"})
	relayLeader = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mindmesh_outbox_relay_leader",
		Help: "1 while this instance is the outbox relay leader.",
	}, []string{"store"})
)
