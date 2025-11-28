package otlp

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	protocol = "otlp"
)

var (
	exportRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_requests_total",
			Help: "Total number of export requests received",
		},
		[]string{"protocol"},
	)

	metricsSeenTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_metrics_seen_total",
			Help: "Total number of metrics seen in export requests",
		},
		[]string{"protocol"},
	)

	datapointsSeenTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_datapoints_seen_total",
			Help: "Total number of datapoints seen in export requests",
		},
		[]string{"protocol"},
	)

	metricsDroppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_metrics_dropped_total",
			Help: "Total number of metrics dropped during filtering",
		},
		[]string{"protocol"},
	)

	datapointsDroppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_datapoints_dropped_total",
			Help: "Total number of datapoints dropped during filtering",
		},
		[]string{"protocol"},
	)

	lookupErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_lookup_errors_total",
			Help: "Total number of database lookup errors",
		},
		[]string{"protocol"},
	)

	lookupLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_lookup_latency_seconds",
			Help:    "Duration of database lookups in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"protocol"},
	)

	exportInflight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ingester_export_inflight",
			Help: "Current in-flight Export requests",
		},
		[]string{"protocol", "rpc_method"},
	)

	exportSuccessTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_success_total",
			Help: "Total successful Export responses",
		},
		[]string{"protocol", "rpc_method"},
	)

	exportFailureTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_failure_total",
			Help: "Total Export failures",
		},
		[]string{"protocol", "rpc_method", "grpc_status_code"},
	)

	exportDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_export_duration_seconds",
			Help:    "Duration of export requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"protocol", "rpc_method"},
	)

	exportMetricsPerRequest = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_export_metrics_per_request",
			Help:    "Number of metrics per request by stage",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"protocol", "stage"},
	)

	exportDatapointsPerRequest = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_export_datapoints_per_request",
			Help:    "Number of datapoints per request by stage",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"protocol", "stage"},
	)

	emptyJobTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_empty_job_total",
			Help: "Total number of occurrences where service.name or job are missing",
		},
		[]string{"protocol"},
	)

	labels = prometheus.Labels{
		"protocol": protocol,
	}
)
