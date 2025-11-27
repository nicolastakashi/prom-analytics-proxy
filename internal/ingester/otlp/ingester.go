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
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

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
	exportRequestsTotal.With(labels).Inc()

	namesSet, beforeMetricsCount, seenDatapoints := i.collectNamesAndCounts(req)

	if beforeMetricsCount > 0 {
		metricsSeenTotal.With(labels).Add(float64(beforeMetricsCount))
	}
	if seenDatapoints > 0 {
		datapointsSeenTotal.With(labels).Add(float64(seenDatapoints))
	}

	if len(namesSet) == 0 {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	unused, ok := i.lookupUnused(ctx, namesSet)
	if !ok {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	// Determine if we're in dry-run mode
	dryRun := i.config != nil && i.config.Ingester.DryRun

	var droppedDatapoints int64
	var droppedMetrics int64

	if len(unused) > 0 {
		allowedSet, deniedSet := i.allowedDeniedSets()
		if dryRun {
			droppedMetrics, droppedDatapoints = i.countWouldDrop(req, unused, allowedSet, deniedSet)
		} else {
			droppedMetrics, droppedDatapoints = i.filterUnused(req, unused, allowedSet, deniedSet, beforeMetricsCount)
		}
	}

	datapointsDroppedTotal.With(labels).Add(float64(droppedDatapoints))
	metricsDroppedTotal.With(labels).Add(float64(droppedMetrics))
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

// ----- helpers -----

func (i *OtlpIngester) collectNamesAndCounts(req *metricspb.ExportMetricsServiceRequest) (map[string]struct{}, int64, int64) {
	names := make(map[string]struct{})
	var metricsCount int64
	var datapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			metricsCount += int64(len(sm.Metrics))
			for _, m := range sm.Metrics {
				if name := m.GetName(); name != "" {
					names[name] = struct{}{}
				}
				datapoints += int64(countMetricDatapoints(m))
			}
		}
	}
	return names, metricsCount, datapoints
}

func (i *OtlpIngester) lookupUnused(ctx context.Context, names map[string]struct{}) (map[string]struct{}, bool) {
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
			slog.Debug("ingester:metric metadata", "metric.name", mm.Name, "metric.alertCount", mm.AlertCount, "metric.recordCount", mm.RecordCount, "metric.dashboardCount", mm.DashboardCount, "metric.queryCount", mm.QueryCount)
			if mm.AlertCount == 0 && mm.RecordCount == 0 && mm.DashboardCount == 0 && mm.QueryCount == 0 {
				unused[mm.Name] = struct{}{}
				slog.Debug("ingester:metric is unused", "metric.name", mm.Name)
			}
		}
		batch = batch[:0]
		return true
	}
	for n := range names {
		batch = append(batch, n)
		if len(batch) == chunkSize {
			if ok := flush(); !ok {
				return nil, false
			}
		}
	}
	if ok := flush(); !ok {
		return nil, false
	}
	return unused, true
}

func (i *OtlpIngester) allowedDeniedSets() (map[string]struct{}, map[string]struct{}) {
	toSet := func(xs []string) map[string]struct{} {
		if len(xs) == 0 {
			return nil
		}
		m := make(map[string]struct{}, len(xs))
		for _, s := range xs {
			if s == "" {
				continue
			}
			m[s] = struct{}{}
		}
		return m
	}
	if i.config == nil {
		return nil, nil
	}
	return toSet(i.config.Ingester.OTLP.AllowedJobs), toSet(i.config.Ingester.OTLP.DeniedJobs)
}

func resolveJob(res *resourcepb.Resource) string {
	job := AttrView(res.GetAttributes()).Get("job")
	if job == "" {
		job = AttrView(res.GetAttributes()).Get("service.name")
	}
	if job == "" {
		emptyJobTotal.With(labels).Inc()
	}
	return job
}

func isUnusedDropActive(job string, allowed, denied map[string]struct{}) bool {
	if len(allowed) > 0 {
		if _, ok := allowed[job]; !ok {
			return false
		}
	}
	if _, ok := denied[job]; ok {
		return false
	}
	return true
}

func shouldDropUnused(res *resourcepb.Resource, metricName string, unused, allowed, denied map[string]struct{}) bool {
	if _, unusedMetric := unused[metricName]; !unusedMetric {
		return false
	}
	return isUnusedDropActive(resolveJob(res), allowed, denied)
}

func (i *OtlpIngester) countWouldDrop(req *metricspb.ExportMetricsServiceRequest, unused, allowed, denied map[string]struct{}) (int64, int64) {
	var droppedMetrics int64
	var droppedDatapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if name := m.GetName(); name != "" {
					if shouldDropUnused(rm.Resource, name, unused, allowed, denied) {
						droppedMetrics++
						droppedDatapoints += int64(countMetricDatapoints(m))
					}
				}
			}
		}
	}
	return droppedMetrics, droppedDatapoints
}

func (i *OtlpIngester) filterUnused(req *metricspb.ExportMetricsServiceRequest, unused, allowed, denied map[string]struct{}, beforeMetricsCount int64) (int64, int64) {
	cfg := FilterConfig{
		MetricKeep: func(ctx FilterContext) bool {
			return !shouldDropUnused(ctx.Resource, ctx.Metric.GetName(), unused, allowed, denied)
		},
	}
	droppedDatapoints := int64(FilterExport(req, cfg))
	var afterMetricsCount int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			afterMetricsCount += int64(len(sm.Metrics))
		}
	}
	droppedMetrics := beforeMetricsCount - afterMetricsCount
	return droppedMetrics, droppedDatapoints
}
