// Package metrics defines the Prometheus metrics exported on /metrics.
//
// End-to-end latency is measured from the moment the ingest role accepted an
// event to the moment a channel delivered the resulting notification, so the
// p50/p99 numbers in the load-test report are measured, not estimated.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var latencyBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
}

var (
	EventsIngested = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_events_ingested_total",
		Help: "Events accepted by the ingest role, by source.",
	}, []string{"source"})

	IngestRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_events_rejected_total",
		Help: "Events refused at ingest, by reason.",
	}, []string{"reason"})

	IngestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "kardan_ingest_duration_seconds",
		Help:    "Time to durably append an accepted event to the events topic.",
		Buckets: latencyBuckets,
	})

	EventsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "kardan_events_processed_total",
		Help: "Events evaluated by the processor role.",
	})

	RuleMatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_rule_matches_total",
		Help: "Rule matches, by rule.",
	}, []string{"rule"})

	AlertsCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_alerts_created_total",
		Help: "New alerts opened, by team.",
	}, []string{"team"})

	AlertsDeduplicated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "kardan_alerts_deduplicated_total",
		Help: "Matching events folded into an existing open alert.",
	})

	ProcessBatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "kardan_process_batch_duration_seconds",
		Help:    "Time to process one batch of events end to end (rules, dedup, commit).",
		Buckets: latencyBuckets,
	})

	NotificationsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_notifications_total",
		Help: "Notification delivery attempts, by channel and outcome.",
	}, []string{"channel", "outcome"})

	EndToEndLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kardan_e2e_latency_seconds",
		Help:    "Ingest-to-delivery latency, by channel.",
		Buckets: latencyBuckets,
	}, []string{"channel"})

	AlertsAutoResolved = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_alerts_auto_resolved_total",
		Help: "Alerts closed without a human, by reason (recovery, silence, window).",
	}, []string{"reason"})

	NotificationsHeld = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_notifications_held_total",
		Help: "Notifications held by per-recipient throttling, by channel.",
	}, []string{"channel"})

	Escalations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_escalations_total",
		Help: "Escalation steps taken for unacknowledged alerts, by team.",
	}, []string{"team"})

	DigestsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_digests_total",
		Help: "Digest notifications created, by kind (window, throttle).",
	}, []string{"kind"})

	ChannelState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kardan_channel_breaker_state",
		Help: "Circuit breaker state per channel on this replica: 0 closed, 1 half-open, 2 open.",
	}, []string{"channel"})

	InboundReplies = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kardan_inbound_replies_total",
		Help: "Replies received from SMS / phone, by channel and outcome.",
	}, []string{"channel", "outcome"})

	SSEClients = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "kardan_sse_clients",
		Help: "Connected live-feed clients on this replica.",
	})
)
