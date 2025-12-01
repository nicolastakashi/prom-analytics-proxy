package otlp

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	rpcSystem        = "grpc"
	networkTransport = "tcp"
)

var (
	// RPC server metrics (OTLP gRPC Export) following OTel RPC semantic conventions
	rpcServerDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_rpc_server_duration_seconds",
			Help:    "Duration of RPC server calls in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"rpc.system", "rpc.service", "rpc.method", "network.transport", "code"},
	)

	// Pipeline metrics (not RPC semantics, but needed for observability)
	receiverReceivedMetricPointsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_receiver_received_metric_points_total",
			Help: "Total number of metric points received",
		},
	)

	processorDroppedMetricPointsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_processor_dropped_metric_points_total",
			Help: "Total number of metric points dropped during processing",
		},
		[]string{"reason"},
	)

	processorLookupLatencySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingester_processor_lookup_latency_seconds",
			Help:    "Duration of database lookups in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)

	processorLookupErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_processor_lookup_errors_total",
			Help: "Total number of database lookup errors",
		},
	)

	receiverMissingJobTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_receiver_missing_job_total",
			Help: "Total number of occurrences where service.name or job are missing",
		},
	)

	canonicalServerLabels = prometheus.Labels{
		"rpc.system":        rpcSystem,
		"rpc.service":       rpcService,
		"rpc.method":        rpcMethod,
		"network.transport": networkTransport,
		"code":              "OK",
	}
)

func init() {
	rpcServerDurationSeconds.With(canonicalServerLabels).Observe(0)
	processorDroppedMetricPointsTotal.With(prometheus.Labels{"reason": "unused_metric"}).Add(0)
	processorDroppedMetricPointsTotal.With(prometheus.Labels{"reason": "job_denied"}).Add(0)
}
