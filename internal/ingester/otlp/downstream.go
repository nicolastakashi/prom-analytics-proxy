package otlp

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	_ "google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	rpcClientDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_rpc_client_duration_seconds",
			Help:    "Duration of RPC client calls in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"rpc.system", "rpc.service", "rpc.method", "network.transport", "code"},
	)

	canonicalClientLabels = prometheus.Labels{
		"rpc.system":        rpcSystem,
		"rpc.service":       rpcService,
		"rpc.method":        rpcMethod,
		"network.transport": networkTransport,
		"code":              "OK",
	}
)

func init() {
	rpcClientDurationSeconds.With(canonicalClientLabels).Observe(0)
}

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

type ExporterOptions struct {
	Retry               RetryPolicy
	MaxSendMsgSizeBytes int
	MaxRecvMsgSizeBytes int
	BalancerName        string
	ConnectMinTimeout   time.Duration
	ConnectBaseDelay    time.Duration
	ConnectMaxDelay     time.Duration
	ConnectMultiplier   float64
}

func defaultExporterOptions() ExporterOptions {
	return ExporterOptions{
		Retry:               RetryPolicy{MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 1 * time.Second, BackoffMultiplier: 1.6, RetryableStatusCodes: []string{"UNAVAILABLE"}},
		MaxSendMsgSizeBytes: 10 * 1024 * 1024,
		MaxRecvMsgSizeBytes: 10 * 1024 * 1024,
		ConnectMinTimeout:   500 * time.Millisecond,
		ConnectBaseDelay:    250 * time.Millisecond,
		ConnectMaxDelay:     5 * time.Second,
		ConnectMultiplier:   1.6,
	}
}

type grpcServiceConfigJSON struct {
	LoadBalancingConfig []map[string]interface{} `json:"loadBalancingConfig,omitempty"`
	MethodConfig        []grpcMethodConfigJSON   `json:"methodConfig"`
}
type grpcMethodConfigJSON struct {
	Name        []grpcNameJSON      `json:"name"`
	RetryPolicy grpcRetryPolicyJSON `json:"retryPolicy"`
}
type grpcNameJSON struct {
	Service string `json:"service"`
	Method  string `json:"method"`
}
type grpcRetryPolicyJSON struct {
	MaxAttempts          int      `json:"maxAttempts"`
	InitialBackoff       string   `json:"initialBackoff"`
	MaxBackoff           string   `json:"maxBackoff"`
	BackoffMultiplier    float64  `json:"backoffMultiplier"`
	RetryableStatusCodes []string `json:"retryableStatusCodes"`
}

func buildServiceConfigJSON(o ExporterOptions) (string, error) {
	sec := func(d time.Duration) string {
		return strconv.FormatFloat(float64(d)/float64(time.Second), 'f', -1, 64) + "s"
	}
	cfg := grpcServiceConfigJSON{
		MethodConfig: []grpcMethodConfigJSON{{
			Name:        []grpcNameJSON{{Service: "opentelemetry.proto.collector.metrics.v1.MetricsService", Method: "Export"}},
			RetryPolicy: grpcRetryPolicyJSON{MaxAttempts: o.Retry.MaxAttempts, InitialBackoff: sec(o.Retry.InitialBackoff), MaxBackoff: sec(o.Retry.MaxBackoff), BackoffMultiplier: o.Retry.BackoffMultiplier, RetryableStatusCodes: o.Retry.RetryableStatusCodes},
		}},
	}
	// Add load balancing config if balancer name is specified
	if o.BalancerName == "round_robin" {
		cfg.LoadBalancingConfig = []map[string]interface{}{
			{"round_robin": map[string]interface{}{}},
		}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func NewOTLPExporter(endpoint string, protocol string, opts *ExporterOptions) (MetricsExporter, error) {
	o := defaultExporterOptions()
	if opts != nil {
		// Merge provided options with defaults so Retry policy remains valid
		if opts.MaxSendMsgSizeBytes > 0 {
			o.MaxSendMsgSizeBytes = opts.MaxSendMsgSizeBytes
		}
		if opts.MaxRecvMsgSizeBytes > 0 {
			o.MaxRecvMsgSizeBytes = opts.MaxRecvMsgSizeBytes
		}
		if opts.Retry.MaxAttempts > 0 {
			o.Retry.MaxAttempts = opts.Retry.MaxAttempts
		}
		if opts.Retry.InitialBackoff > 0 {
			o.Retry.InitialBackoff = opts.Retry.InitialBackoff
		}
		if opts.Retry.MaxBackoff > 0 {
			o.Retry.MaxBackoff = opts.Retry.MaxBackoff
		}
		if opts.Retry.BackoffMultiplier > 0 {
			o.Retry.BackoffMultiplier = opts.Retry.BackoffMultiplier
		}
		if len(opts.Retry.RetryableStatusCodes) > 0 {
			o.Retry.RetryableStatusCodes = opts.Retry.RetryableStatusCodes
		}
		if opts.BalancerName != "" {
			o.BalancerName = opts.BalancerName
		}
		if opts.ConnectMinTimeout > 0 {
			o.ConnectMinTimeout = opts.ConnectMinTimeout
		}
		if opts.ConnectBaseDelay > 0 {
			o.ConnectBaseDelay = opts.ConnectBaseDelay
		}
		if opts.ConnectMaxDelay > 0 {
			o.ConnectMaxDelay = opts.ConnectMaxDelay
		}
		if opts.ConnectMultiplier > 0 {
			o.ConnectMultiplier = opts.ConnectMultiplier
		}
	}
	serviceConfig, err := buildServiceConfigJSON(o)
	if err != nil {
		return nil, err
	}
	connectParams := grpc.ConnectParams{
		MinConnectTimeout: o.ConnectMinTimeout,
		Backoff: backoff.Config{
			BaseDelay:  o.ConnectBaseDelay,
			Multiplier: o.ConnectMultiplier,
			Jitter:     0.2,
			MaxDelay:   o.ConnectMaxDelay,
		},
	}
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(func() int {
				if o.MaxSendMsgSizeBytes > 0 {
					return o.MaxSendMsgSizeBytes
				}
				return 10 * 1024 * 1024
			}()),
			grpc.MaxCallRecvMsgSize(func() int {
				if o.MaxRecvMsgSizeBytes > 0 {
					return o.MaxRecvMsgSizeBytes
				}
				return 10 * 1024 * 1024
			}()),
		),
		grpc.WithConnectParams(connectParams),
	)
	if err != nil {
		return nil, err
	}
	return &otlpExporter{protocol: protocol, client: metricspb.NewMetricsServiceClient(conn), conn: conn}, nil
}

func (e *otlpExporter) Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) error {
	start := time.Now()
	_, err := e.client.Export(ctx, req)
	code := "OK"
	if err != nil {
		code = status.Code(err).String()
	}
	labels := prometheus.Labels{
		"rpc.system":        rpcSystem,
		"rpc.service":       rpcService,
		"rpc.method":        rpcMethod,
		"network.transport": networkTransport,
		"code":              code,
	}
	rpcClientDurationSeconds.With(labels).Observe(time.Since(start).Seconds())
	return err
}

func (e *otlpExporter) Close() error {
	if e.conn != nil {
		return e.conn.Close()
	}
	return nil
}

// forwarder sends requests to the downstream endpoint under a timeout,
// retrying once, 250ms later, a send that fails with Unavailable. A forwarder
// without an exporter has no downstream.
type forwarder struct {
	exporter MetricsExporter
	timeout  time.Duration
}

func (f *forwarder) enabled() bool { return f.exporter != nil }

// setExporter replaces the exporter; nil leaves no downstream.
func (f *forwarder) setExporter(exp MetricsExporter) { f.exporter = exp }

// forward sends req downstream and returns nil, or the error that ended the send.
func (f *forwarder) forward(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) error {
	tout := computeExportTimeout(ctx, f.timeout)
	fctx, cancel := context.WithTimeout(ctx, tout)
	defer cancel()

	err := f.exporter.Export(fctx, req)
	if err == nil || status.Code(err) != codes.Unavailable {
		return err
	}
	select {
	case <-time.After(250 * time.Millisecond):
	case <-fctx.Done():
		return fctx.Err()
	}
	fctx2, cancel2 := context.WithTimeout(ctx, tout)
	defer cancel2()
	exporterRetriesTotal.Inc()
	return f.exporter.Export(fctx2, req)
}

// close closes the exporter, if any.
func (f *forwarder) close() {
	if f.exporter != nil {
		_ = f.exporter.Close()
	}
}

func initDownstreamExporter(cfg *config.Config) (MetricsExporter, error) {
	downstreamEndpoint := cfg.Ingester.OTLP.DownstreamAddress
	if downstreamEndpoint == "" {
		return nil, nil
	}
	return NewOTLPExporter(downstreamEndpoint, cfg.Ingester.Protocol, &ExporterOptions{
		MaxSendMsgSizeBytes: cfg.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes,
		MaxRecvMsgSizeBytes: cfg.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes,
		BalancerName:        cfg.Ingester.OTLP.BalancerName,
		Retry: RetryPolicy{
			MaxAttempts:          cfg.Ingester.OTLP.DownstreamRetryMaxAttempts,
			InitialBackoff:       cfg.Ingester.OTLP.DownstreamRetryInitialBackoff,
			MaxBackoff:           cfg.Ingester.OTLP.DownstreamRetryMaxBackoff,
			BackoffMultiplier:    cfg.Ingester.OTLP.DownstreamRetryBackoffMultiplier,
			RetryableStatusCodes: cfg.Ingester.OTLP.DownstreamRetryCodes,
		},
		ConnectMinTimeout: cfg.Ingester.OTLP.DownstreamConnectMinTimeout,
		ConnectBaseDelay:  cfg.Ingester.OTLP.DownstreamConnectBaseDelay,
		ConnectMaxDelay:   cfg.Ingester.OTLP.DownstreamConnectMaxDelay,
		ConnectMultiplier: cfg.Ingester.OTLP.DownstreamConnectBackoffMultiplier,
	})
}

func computeExportTimeout(ctx context.Context, base time.Duration) time.Duration {
	tout := base
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < tout {
			tout = d - 50*time.Millisecond
			if tout <= 0 {
				tout = 50 * time.Millisecond
			}
		}
	}
	return tout
}
