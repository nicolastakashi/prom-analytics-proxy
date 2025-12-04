package otlp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricsv1pb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	rpcMethod  = "Export"
	rpcService = "opentelemetry.proto.collector.metrics.v1.MetricsService"
	rpcOkCode  = "OK"
)

type OtlpIngester struct {
	metricspb.UnimplementedMetricsServiceServer
	config *config.Config
	db     db.Provider

	exporter      MetricsExporter
	exportTimeout time.Duration

	allowedJobs map[string]struct{}
	deniedJobs  map[string]struct{}
}

func NewOtlpIngester(config *config.Config, dbProvider db.Provider) (*OtlpIngester, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if dbProvider == nil {
		return nil, fmt.Errorf("db provider cannot be nil")
	}

	allowedJobs, deniedJobs := buildJobSets(config)
	return &OtlpIngester{
		config:        config,
		db:            dbProvider,
		exportTimeout: 5 * time.Second,
		allowedJobs:   allowedJobs,
		deniedJobs:    deniedJobs,
	}, nil
}

func buildJobSets(cfg *config.Config) (map[string]struct{}, map[string]struct{}) {
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
	return toSet(cfg.Ingester.AllowedJobs), toSet(cfg.Ingester.DeniedJobs)
}

func (i *OtlpIngester) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", i.config.Ingester.OTLP.ListenAddress)
	if err != nil {
		return err
	}

	serverOpts := grpcServerOptions(i.config)
	grpcServer := grpc.NewServer(serverOpts...)

	exp, err := initDownstreamExporter(i.config)
	if err != nil {
		_ = lis.Close()
		return err
	}
	if exp != nil {
		i.exporter = exp
		slog.InfoContext(ctx, "ingester.downstream.connected", "endpoint", i.config.Ingester.OTLP.DownstreamAddress)
	} else {
		slog.InfoContext(ctx, "ingester.downstream.disabled")
	}

	metricspb.RegisterMetricsServiceServer(grpcServer, i)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	slog.InfoContext(ctx, "ingester.starting", "address", i.config.Ingester.OTLP.ListenAddress)

	serveErrCh := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

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

func grpcServerOptions(cfg *config.Config) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(func() int {
			if cfg != nil && cfg.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes > 0 {
				return cfg.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes
			}
			return 10 * 1024 * 1024
		}()),
		grpc.MaxSendMsgSize(func() int {
			if cfg != nil && cfg.Ingester.OTLP.GRPCMaxSendMsgSizeBytes > 0 {
				return cfg.Ingester.OTLP.GRPCMaxSendMsgSizeBytes
			}
			return 10 * 1024 * 1024
		}()),
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

func logExportSuccess(ctx context.Context, downstreamEnabled, dryRun bool) {
	slog.DebugContext(ctx, "ingester.export.success",
		"rpc.method", rpcMethod,
		"downstream.enabled", downstreamEnabled,
		"dry_run", dryRun,
	)
}

func logExportFailure(ctx context.Context, err error, downstreamEnabled bool) {
	code := status.Code(err)
	switch {
	case errors.Is(err, context.Canceled) || code == codes.Canceled:
		slog.DebugContext(ctx, "ingester.export.canceled",
			"rpc.method", rpcMethod,
			"downstream.enabled", downstreamEnabled,
		)
		return
	case errors.Is(err, context.DeadlineExceeded) || code == codes.DeadlineExceeded:
		slog.InfoContext(ctx, "ingester.export.deadline_exceeded",
			"rpc.method", rpcMethod,
			"downstream.enabled", downstreamEnabled,
		)
		return
	default:
		slog.ErrorContext(ctx, "ingester.export.failure",
			"rpc.method", rpcMethod,
			"grpc.status_code", code.String(),
			"err", err,
			"downstream.enabled", downstreamEnabled,
		)
	}
}

func (i *OtlpIngester) Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) (*metricspb.ExportMetricsServiceResponse, error) {
	start := time.Now()
	var code string
	rpcInFlight.Inc()
	defer func() {
		rpcInFlight.Dec()
		labels := prometheus.Labels{
			"rpc.system":        rpcSystem,
			"rpc.service":       rpcService,
			"rpc.method":        rpcMethod,
			"network.transport": networkTransport,
			"code":              code,
		}
		rpcServerDurationSeconds.With(labels).Observe(time.Since(start).Seconds())
	}()

	namesSet, histogramBases, beforeMetricsCount, seenDatapoints := i.collectNamesAndCounts(req)
	if seenDatapoints > 0 {
		receiverReceivedMetricPointsTotal.Add(float64(seenDatapoints))
	}

	if len(namesSet) == 0 {
		code = rpcOkCode
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	unused, ok := i.lookupUnused(ctx, namesSet, histogramBases)
	if !ok {
		code = rpcOkCode
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}
	slog.DebugContext(ctx, "ingester.unused_metrics.count", "count", len(unused))

	dryRun := i.config != nil && i.config.Ingester.DryRun

	var droppedDatapoints int64

	if len(unused) > 0 {
		if dryRun {
			_, droppedDatapoints = i.countWouldDrop(req, unused, i.allowedJobs, i.deniedJobs)
		} else {
			_, droppedDatapoints = i.filterUnused(req, unused, i.allowedJobs, i.deniedJobs, beforeMetricsCount)
		}
		if droppedDatapoints > 0 {
			processorDroppedMetricPointsTotal.With(prometheus.Labels{"reason": "unused_metric"}).Add(float64(droppedDatapoints))
		}
	}

	if i.exporter == nil {
		code = rpcOkCode
		logExportSuccess(ctx, false, dryRun)
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	tout := computeExportTimeout(ctx, i.exportTimeout)
	fctx, cancel := context.WithTimeout(ctx, tout)
	defer cancel()

	err := i.exporter.Export(fctx, req)
	if err != nil && status.Code(err) == codes.Unavailable {
		select {
		case <-time.After(250 * time.Millisecond):
		case <-fctx.Done():
			code = status.Code(fctx.Err()).String()
			logExportFailure(ctx, fctx.Err(), true)
			return nil, fctx.Err()
		}
		fctx2, cancel2 := context.WithTimeout(ctx, tout)
		defer cancel2()
		exporterRetriesTotal.Inc()
		if err2 := i.exporter.Export(fctx2, req); err2 != nil {
			code = status.Code(err2).String()
			logExportFailure(ctx, err2, true)
			return nil, err2
		}
		code = rpcOkCode
		logExportSuccess(ctx, true, dryRun)
		return &metricspb.ExportMetricsServiceResponse{}, nil
	} else if err != nil {
		code = status.Code(err).String()
		logExportFailure(ctx, err, true)
		return nil, err
	}

	code = rpcOkCode
	logExportSuccess(ctx, true, dryRun)
	return &metricspb.ExportMetricsServiceResponse{}, nil
}

func (i *OtlpIngester) SetExporter(exp MetricsExporter) { i.exporter = exp }

func (i *OtlpIngester) collectNamesAndCounts(req *metricspb.ExportMetricsServiceRequest) (map[string]struct{}, map[string]struct{}, int64, int64) {
	names := make(map[string]struct{})
	histogramBases := make(map[string]struct{})
	var metricsCount int64
	var datapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			metricsCount += int64(len(sm.Metrics))
			for _, m := range sm.Metrics {
				if name := m.GetName(); name != "" {
					names[name] = struct{}{}
					// For histogram metrics, also collect derivative names used in Prometheus catalog
					if _, isHistogram := m.Data.(*metricsv1pb.Metric_Histogram); isHistogram {
						histogramBases[name] = struct{}{}
						names[name+"_bucket"] = struct{}{}
						names[name+"_count"] = struct{}{}
						names[name+"_sum"] = struct{}{}
					}
				}
				datapoints += int64(countMetricDatapoints(m))
			}
		}
	}
	return names, histogramBases, metricsCount, datapoints
}

// histogramVariantState tracks the unused status of histogram variant metrics
// as they are processed incrementally during streaming lookup.
type histogramVariantState struct {
	// seen tracks which variants we've encountered in metadata lookups
	seenBucket bool
	seenCount  bool
	seenSum    bool
	// unused tracks which variants are confirmed unused (only set if seen=true)
	unusedBucket bool
	unusedCount  bool
	unusedSum    bool
}

func (i *OtlpIngester) lookupUnused(ctx context.Context, names map[string]struct{}, histogramBases map[string]struct{}) (map[string]struct{}, bool) {
	unused := make(map[string]struct{})
	// Track histogram variant states incrementally instead of storing full metadata
	histogramStates := make(map[string]*histogramVariantState, len(histogramBases))
	for baseName := range histogramBases {
		histogramStates[baseName] = &histogramVariantState{}
	}

	const chunkSize = 500
	batch := make([]string, 0, chunkSize)

	// Process each chunk immediately and free memory before next chunk
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		t0 := time.Now()
		metas, err := i.db.GetSeriesMetadataByNames(ctx, batch, "")
		if err != nil {
			slog.ErrorContext(ctx, "ingester.lookup.failed_skipping_drops", "err", err)
			processorLookupErrorsTotal.Inc()
			return false
		}
		processorLookupLatencySeconds.Observe(time.Since(t0).Seconds())

		// Process metadata immediately and update state incrementally
		for _, mm := range metas {
			isUnused := metricMetadataUnused(mm)

			// Check if this is a histogram variant
			if baseName, isVariant := i.isHistogramVariant(mm.Name, histogramBases); isVariant {
				state := histogramStates[baseName]
				switch mm.Name {
				case baseName + "_bucket":
					state.seenBucket = true
					state.unusedBucket = isUnused
				case baseName + "_count":
					state.seenCount = true
					state.unusedCount = isUnused
				case baseName + "_sum":
					state.seenSum = true
					state.unusedSum = isUnused
				}
			} else {
				// Regular metric (not a histogram variant)
				if isUnused {
					unused[mm.Name] = struct{}{}
					slog.DebugContext(ctx, "ingester.unused_metric.found", "metric_name", mm.Name)
				}
			}
		}

		// Clear batch to free memory before next chunk
		batch = batch[:0]
		return true
	}

	// Stream through names in chunks
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

	// Reconcile histogram bases using lightweight state
	// Mark as unused only if ALL variants are seen AND all are unused
	// If any variant is missing, fail open (don't mark as unused)
	for baseName, state := range histogramStates {
		// If any variant is missing, fail open (don't mark as unused)
		if !state.seenBucket || !state.seenCount || !state.seenSum {
			delete(unused, baseName)
			continue
		}

		// All variants seen - mark as unused only if all are unused
		if state.unusedBucket && state.unusedCount && state.unusedSum {
			unused[baseName] = struct{}{}
			slog.DebugContext(ctx, "ingester.unused_histogram.found", "metric_name", baseName)
		} else {
			// At least one variant is used, so don't mark base as unused
			delete(unused, baseName)
		}
	}

	return unused, true
}

// isHistogramVariant checks if a metric name is a histogram variant (_bucket, _count, _sum)
// and returns the base name if it is, along with a boolean indicating if it's a variant.
func (i *OtlpIngester) isHistogramVariant(name string, histogramBases map[string]struct{}) (string, bool) {
	for baseName := range histogramBases {
		if name == baseName+"_bucket" || name == baseName+"_count" || name == baseName+"_sum" {
			return baseName, true
		}
	}
	return "", false
}

func metricMetadataUnused(mm models.MetricMetadata) bool {
	return mm.AlertCount == 0 &&
		mm.RecordCount == 0 &&
		mm.DashboardCount == 0 &&
		mm.QueryCount == 0
}

func resolveJob(res *resourcepb.Resource) string {
	if res == nil {
		receiverMissingJobTotal.Inc()
		return ""
	}
	job := AttrView(res.GetAttributes()).Get("job")
	if job == "" {
		job = AttrView(res.GetAttributes()).Get("service.name")
	}
	if job == "" {
		receiverMissingJobTotal.Inc()
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

func RegisterOTLPFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&config.DefaultConfig.Ingester.OTLP.ListenAddress, "otlp-listen-address", ":4317", "The address the metrics ingester should listen on.")
	flagSet.StringVar(&config.DefaultConfig.Ingester.OTLP.DownstreamAddress, "otlp-downstream-address", "", "Optional downstream OTLP gRPC address to forward filtered metrics")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "otlp-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for OTLP server (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "otlp-max-send-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "Max gRPC send message size for OTLP server (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "otlp-downstream-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for downstream OTLP client (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "otlp-downstream-max-send-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "Max gRPC send message size for downstream OTLP client (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxAttempts, "otlp-downstream-retry-max-attempts", config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxAttempts, "Downstream OTLP retry max attempts")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryInitialBackoff, "otlp-downstream-retry-initial-backoff", config.DefaultConfig.Ingester.OTLP.DownstreamRetryInitialBackoff, "Downstream OTLP retry initial backoff (duration)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxBackoff, "otlp-downstream-retry-max-backoff", config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxBackoff, "Downstream OTLP retry max backoff (duration)")
	flagSet.Float64Var(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryBackoffMultiplier, "otlp-downstream-retry-backoff-multiplier", config.DefaultConfig.Ingester.OTLP.DownstreamRetryBackoffMultiplier, "Downstream OTLP retry backoff multiplier")
	flagSet.Func("otlp-downstream-retry-codes", "Comma-separated gRPC status codes to retry (e.g., UNAVAILABLE,RESOURCE_EXHAUSTED)", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.OTLP.DownstreamRetryCodes = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.OTLP.DownstreamRetryCodes = out
		return nil
	})
	flagSet.StringVar(&config.DefaultConfig.Ingester.OTLP.BalancerName, "otlp-balancer-name", "", "gRPC load balancer name for downstream OTLP client (e.g., round_robin). If empty, defaults to pick_first")
}
