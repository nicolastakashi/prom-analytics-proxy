package otlp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

type OtlpIngester struct {
	metricspb.UnimplementedMetricsServiceServer
	config *config.Config
	db     db.Provider

	exporter      MetricsExporter
	exportTimeout time.Duration
}

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
		[]string{"protocol", "reason"},
	)

	datapointsDroppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_export_datapoints_dropped_total",
			Help: "Total number of datapoints dropped during filtering",
		},
		[]string{"protocol", "reason"},
	)

	lookupErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingester_lookup_errors_total",
			Help: "Total number of database lookup errors",
		},
		[]string{"protocol"},
	)

	exportDurationMs = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingester_export_duration_ms",
			Help:    "Duration of export requests in milliseconds",
			Buckets: prometheus.DefBuckets,
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
)

func NewOtlpIngester(config *config.Config, dbProvider db.Provider) *OtlpIngester {
	return &OtlpIngester{config: config, db: dbProvider, exportTimeout: 5 * time.Second}
}

func (i *OtlpIngester) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", i.config.Ingester.OTLP.ListenAddress)
	if err != nil {
		return err
	}

	serverOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(64 * 1024 * 1024),
		grpc.MaxSendMsgSize(64 * 1024 * 1024),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      0,
			MaxConnectionAgeGrace: 30 * time.Second,
			Time:                  2 * time.Minute,
			Timeout:               20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             1 * time.Minute,
			PermitWithoutStream: true,
		}),
	}

	grpcServer := grpc.NewServer(serverOpts...)

	// Downstream exporter (optional)
	downstreamEndpoint := i.config.Ingester.OTLP.DownstreamAddress
	if downstreamEndpoint != "" {
		exp, err := NewOTLPExporter(downstreamEndpoint, i.config.Ingester.Protocol, nil)
		if err != nil {
			return err
		}
		i.exporter = exp
		slog.Info("ingester: connected to downstream OTLP", "endpoint", downstreamEndpoint)
	} else {
		slog.Info("ingester: downstream disabled (no config ingester.otlp.downstream_address)")
	}

	metricspb.RegisterMetricsServiceServer(grpcServer, i)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	slog.Debug("ingester: starting", "address", i.config.Ingester.OTLP.ListenAddress)

	serveErrCh := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	select {
	case <-ctx.Done():
		healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		if d := i.config.Ingester.DrainDelay; d > 0 {
			time.Sleep(d)
		}
		_ = lis.Close()
		if i.exporter != nil {
			_ = i.exporter.Close()
		}
		shutdownDone := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(shutdownDone)
		}()
		timeout := i.config.Ingester.GracefulShutdownTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		select {
		case <-shutdownDone:
			return nil
		case <-time.After(timeout):
			grpcServer.Stop()
			return ctx.Err()
		}
	case err := <-serveErrCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}
}

// Export receives OTLP metrics and removes globally unused metrics before forwarding.
// See original implementation notes in the prior file; logic unchanged.
func (i *OtlpIngester) Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) (*metricspb.ExportMetricsServiceResponse, error) {
	start := time.Now()
	protocol := "otlp"
	if i.config != nil && i.config.Ingester.Protocol != "" {
		protocol = i.config.Ingester.Protocol
	}
	labels := prometheus.Labels{
		"protocol": protocol,
	}
	exportRequestsTotal.With(labels).Inc()

	namesSet := make(map[string]struct{})
	var beforeMetricsCount int64
	var seenDatapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			beforeMetricsCount += int64(len(sm.Metrics))
			for _, m := range sm.Metrics {
				if name := m.GetName(); name != "" {
					namesSet[name] = struct{}{}
				}
				seenDatapoints += int64(countMetricDatapoints(m))
			}
		}
	}

	if beforeMetricsCount > 0 {
		metricsSeenTotal.With(labels).Add(float64(beforeMetricsCount))
	}
	if seenDatapoints > 0 {
		datapointsSeenTotal.With(labels).Add(float64(seenDatapoints))
	}

	if len(namesSet) == 0 {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	unused := make(map[string]struct{})
	const chunkSize = 500
	batch := make([]string, 0, chunkSize)
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		t0 := time.Now()
		metas, err := i.db.GetSeriesMetadataByNames(ctx, batch, "")
		if err != nil {
			slog.Error("ingester: GetSeriesMetadataByNames failed, skipping drops", "err", err)
			lookupErrorsTotal.With(labels).Inc()
			return false
		}
		lookupLatencySeconds.With(labels).Observe(float64(time.Since(t0).Seconds()))
		for _, mm := range metas {
			if mm.AlertCount == 0 && mm.RecordCount == 0 && mm.DashboardCount == 0 && mm.QueryCount == 0 {
				unused[mm.Name] = struct{}{}
			}
		}
		batch = batch[:0]
		return true
	}
	for n := range namesSet {
		batch = append(batch, n)
		if len(batch) == chunkSize {
			if ok := flush(); !ok {
				return &metricspb.ExportMetricsServiceResponse{}, nil
			}
		}
	}
	if ok := flush(); !ok {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	// Determine if we're in dry-run mode
	dryRun := i.config != nil && i.config.Ingester.DryRun

	var droppedDatapoints int64
	var droppedMetrics int64

	if len(unused) > 0 {
		if dryRun {
			// Only compute what would be dropped; do not mutate req
			for _, rm := range req.ResourceMetrics {
				for _, sm := range rm.ScopeMetrics {
					for _, m := range sm.Metrics {
						if name := m.GetName(); name != "" {
							if _, shouldDrop := unused[name]; shouldDrop {
								droppedMetrics++
								droppedDatapoints += int64(countMetricDatapoints(m))
							}
						}
					}
				}
			}
		} else {
			cfg := FilterConfig{
				MetricKeep: func(ctx FilterContext) bool {
					name := ctx.Metric.GetName()
					_, drop := unused[name]
					return !drop
				},
			}
			droppedDatapoints = int64(FilterExport(req, cfg))
			var afterMetricsCount int64
			for _, rm := range req.ResourceMetrics {
				for _, sm := range rm.ScopeMetrics {
					afterMetricsCount += int64(len(sm.Metrics))
				}
			}
			droppedMetrics = beforeMetricsCount - afterMetricsCount
		}
	}

	if droppedDatapoints > 0 {
		dropLabels := prometheus.Labels{
			"protocol": protocol,
			"reason":   "unused",
		}
		datapointsDroppedTotal.With(dropLabels).Add(float64(droppedDatapoints))
	}
	if droppedMetrics > 0 {
		dropLabels := prometheus.Labels{
			"protocol": protocol,
			"reason":   "unused",
		}
		metricsDroppedTotal.With(dropLabels).Add(float64(droppedMetrics))
	}

	exportDurationMs.With(labels).Observe(float64(time.Since(start).Milliseconds()))

	if i.exporter == nil {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	tout := i.exportTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < tout {
			tout = d - 50*time.Millisecond
			if tout <= 0 {
				tout = 50 * time.Millisecond
			}
		}
	}
	fctx, cancel := context.WithTimeout(ctx, tout)
	defer cancel()

	err := i.exporter.Export(fctx, req)
	if err != nil && status.Code(err) == codes.Unavailable {
		select {
		case <-time.After(250 * time.Millisecond):
		case <-fctx.Done():
			return nil, fctx.Err()
		}
		fctx2, cancel2 := context.WithTimeout(ctx, tout)
		defer cancel2()
		if err2 := i.exporter.Export(fctx2, req); err2 != nil {
			return nil, err2
		}
		return &metricspb.ExportMetricsServiceResponse{}, nil
	} else if err != nil {
		return nil, err
	}

	return &metricspb.ExportMetricsServiceResponse{}, nil
}

// SetExporter allows tests to inject a custom exporter.
func (i *OtlpIngester) SetExporter(exp MetricsExporter) { i.exporter = exp }
