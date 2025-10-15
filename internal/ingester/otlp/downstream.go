package otlp

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

var (
	downstreamExportDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Name: "ingester_downstream_export_duration_seconds", Help: "Duration of downstream export calls", Buckets: prometheus.DefBuckets},
		[]string{"protocol"},
	)
	downstreamExportFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{Name: "ingester_downstream_export_failures_total", Help: "Total downstream export failures"},
		[]string{"protocol", "code"},
	)
)

type MetricsExporter interface {
	Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) error
	Close() error
}

type otlpExporter struct {
	protocol string
	client   metricspb.MetricsServiceClient
	conn     *grpc.ClientConn
}

type RetryPolicy struct {
	MaxAttempts          int
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	BackoffMultiplier    float64
	RetryableStatusCodes []string
}

type ExporterOptions struct{ Retry RetryPolicy }

func defaultExporterOptions() ExporterOptions {
	return ExporterOptions{Retry: RetryPolicy{MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 1 * time.Second, BackoffMultiplier: 1.6, RetryableStatusCodes: []string{"UNAVAILABLE"}}}
}

type grpcServiceConfigJSON struct {
	MethodConfig []grpcMethodConfigJSON `json:"methodConfig"`
}
type grpcMethodConfigJSON struct {
	Name        []grpcNameJSON      `json:"name"`
	RetryPolicy grpcRetryPolicyJSON `json:"retryPolicy"`
}
type grpcNameJSON struct{ Service, Method string }
type grpcRetryPolicyJSON struct {
	MaxAttempts          int      `json:"MaxAttempts"`
	InitialBackoff       string   `json:"InitialBackoff"`
	MaxBackoff           string   `json:"MaxBackoff"`
	BackoffMultiplier    float64  `json:"BackoffMultiplier"`
	RetryableStatusCodes []string `json:"RetryableStatusCodes"`
}

func buildServiceConfigJSON(o ExporterOptions) (string, error) {
	sec := func(d time.Duration) string {
		return strconv.FormatFloat(float64(d)/float64(time.Second), 'f', -1, 64) + "s"
	}
	cfg := grpcServiceConfigJSON{MethodConfig: []grpcMethodConfigJSON{{
		Name:        []grpcNameJSON{{Service: "opentelemetry.proto.collector.metrics.v1.MetricsService", Method: "Export"}},
		RetryPolicy: grpcRetryPolicyJSON{MaxAttempts: o.Retry.MaxAttempts, InitialBackoff: sec(o.Retry.InitialBackoff), MaxBackoff: sec(o.Retry.MaxBackoff), BackoffMultiplier: o.Retry.BackoffMultiplier, RetryableStatusCodes: o.Retry.RetryableStatusCodes},
	}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func NewOTLPExporter(endpoint string, protocol string, opts *ExporterOptions) (MetricsExporter, error) {
	o := defaultExporterOptions()
	if opts != nil {
		o = *opts
	}
	serviceConfig, err := buildServiceConfigJSON(o)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(64*1024*1024), grpc.MaxCallRecvMsgSize(64*1024*1024)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 2 * time.Minute, Timeout: 20 * time.Second, PermitWithoutStream: true}),
	)
	if err != nil {
		return nil, err
	}
	return &otlpExporter{protocol: protocol, client: metricspb.NewMetricsServiceClient(conn), conn: conn}, nil
}

func (e *otlpExporter) Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) error {
	start := time.Now()
	_, err := e.client.Export(ctx, req)
	downstreamExportDurationSeconds.With(prometheus.Labels{"protocol": e.protocol}).Observe(time.Since(start).Seconds())
	if err != nil {
		downstreamExportFailuresTotal.With(prometheus.Labels{"protocol": e.protocol, "code": status.Code(err).String()}).Inc()
		return err
	}
	return nil
}

func (e *otlpExporter) Close() error {
	if e.conn != nil {
		return e.conn.Close()
	}
	return nil
}
