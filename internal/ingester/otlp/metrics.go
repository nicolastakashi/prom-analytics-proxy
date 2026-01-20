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

	// In-flight RPCs for Export (direct concurrency signal)
	rpcInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ingester_rpc_inflight",
			Help: "Current in-flight Export RPCs",
		},
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

	processorLookupDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingester_processor_lookup_duration_seconds",
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

	// Exporter metrics (downstream forwarding health/backpressure)
	exporterRetriesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_exporter_retries_total",
			Help: "Total number of downstream exporter retries",
		},
	)

	// Cache metrics
	ingesterMetricCacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_metric_cache_hits_total",
			Help: "Total number of cache hits for metric usage states",
		},
		[]string{"state"},
	)

	ingesterMetricCacheMissesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_metric_cache_misses_total",
			Help: "Total number of cache misses for metric usage states",
		},
	)

	ingesterMetricCacheErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_metric_cache_errors_total",
			Help: "Total number of cache operation errors",
		},
		[]string{"operation"},
	)

	ingesterMetricCacheLookupSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingester_metric_cache_lookup_seconds",
			Help:    "Duration of cache lookup operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)

	ingesterMetricCacheWriteSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingester_metric_cache_write_seconds",
			Help:    "Duration of cache write operations in seconds",
			Buckets: prometheus.DefBuckets,
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
	ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "used"}).Add(0)
	ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "unused"}).Add(0)
	ingesterMetricCacheErrorsTotal.With(prometheus.Labels{"operation": "get"}).Add(0)
	ingesterMetricCacheErrorsTotal.With(prometheus.Labels{"operation": "set"}).Add(0)
}
