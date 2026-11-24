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

	processorLookupDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_processor_lookup_duration_seconds",
			Help:    "Duration of database lookups in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"outcome"},
	)
	usageResolutionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_processor_usage_resolutions_total",
			Help: "Metric names looked up, by the source that resolved their usage state; unresolved names are forwarded as used",
		},
		[]string{"source"},
	)
	processorUnresolvedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_processor_unresolved_total",
			Help: "Metric names forwarded as used because their usage state could not be resolved, by reason",
		},
		[]string{"reason"},
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
			Help: "Total number of failed cache calls",
		},
		[]string{"operation", "reason"},
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

	// Catalog sync metrics
	catalogBufferSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ingester_catalog_buffer_size",
			Help: "Current number of unique metrics in the catalog buffer",
		},
	)

	catalogBufferDroppedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_catalog_buffer_dropped_total",
			Help: "Total number of metrics dropped from the catalog buffer due to buffer full",
		},
	)

	catalogFlushDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingester_catalog_flush_duration_seconds",
			Help:    "Duration of catalog flush operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)

	catalogFlushMetricsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_catalog_flush_metrics_total",
			Help: "Total number of metrics flushed to the catalog",
		},
	)

	catalogFlushErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ingester_catalog_flush_errors_total",
			Help: "Total number of catalog flush errors",
		},
	)

	// Database connection metrics
	databaseUp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ingester_database_up",
			Help: "1 once the ingester has connected to the database, 0 before; until then only the cache filters metrics",
		},
	)

	databaseConnectAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_database_connect_attempts_total",
			Help: "Total number of attempts to connect to the database, by result",
		},
		[]string{"result"},
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
	for _, operation := range []string{cacheGet, cacheSet} {
		for _, reason := range []string{reasonTimeout, reasonError} {
			ingesterMetricCacheErrorsTotal.WithLabelValues(operation, reason).Add(0)
		}
	}
	for _, outcome := range []string{outcomeOK, reasonTimeout, reasonError} {
		processorLookupDurationSeconds.WithLabelValues(outcome)
	}
	for _, source := range []string{sourceCache, sourceDatabase, sourceUnresolved} {
		usageResolutionsTotal.WithLabelValues(source).Add(0)
	}
	for _, reason := range []string{unresolvedReason(reasonTimeout), unresolvedReason(reasonError), reasonDatabaseUnavailable} {
		processorUnresolvedTotal.WithLabelValues(reason).Add(0)
	}
	for _, result := range []string{resultSuccess, resultFailure} {
		databaseConnectAttemptsTotal.WithLabelValues(result).Add(0)
	}
}

// Label values of the lookup metrics. A lookup's outcome is outcomeOK or the
// reason it failed.
const (
	cacheGet = "get"
	cacheSet = "set"

	outcomeOK     = "ok"
	reasonTimeout = "timeout"
	reasonError   = "error"

	sourceCache      = "cache"
	sourceDatabase   = "database"
	sourceUnresolved = "unresolved"

	// reasonDatabaseUnavailable leaves names unresolved while the ingester
	// has not connected to the database.
	reasonDatabaseUnavailable = "db_unavailable"

	resultSuccess = "success"
	resultFailure = "failure"
)

// unresolvedReason returns the reason names are left unresolved when the
// database lookup fails for reason.
func unresolvedReason(reason string) string { return "db_" + reason }
