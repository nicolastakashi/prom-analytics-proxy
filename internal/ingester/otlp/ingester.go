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

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
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

	defaultCatalogBufferSize       = 10000
	defaultCatalogFlushInterval    = 30 * time.Second
	defaultCatalogSeenTTL          = time.Hour
	defaultGracefulShutdownTimeout = 30 * time.Second
	defaultDownstreamTimeout       = 5 * time.Second
	defaultLookupTimeout           = 2500 * time.Millisecond
	minCacheTimeout                = 5 * time.Millisecond
	// forwardReserve is the time of the incoming request's deadline the usage
	// lookup leaves for forwarding downstream.
	forwardReserve = time.Second
)

type OtlpIngester struct {
	metricspb.UnimplementedMetricsServiceServer
	config *config.Config

	forwarder *forwarder
	lookups   *usageResolver
	healthSrv *health.Server

	allowedJobs map[string]struct{}
	deniedJobs  map[string]struct{}

	catalog *catalogSync // nil: catalog sync disabled
}

func NewOtlpIngester(config *config.Config, store Store) (*OtlpIngester, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if store == nil {
		return nil, fmt.Errorf("store cannot be nil")
	}

	allowedJobs, deniedJobs := buildJobSets(config)

	lookupChunkSize := 500 // default fallback
	if config.Ingester.OTLP.LookupChunkSize > 0 {
		lookupChunkSize = config.Ingester.OTLP.LookupChunkSize
	}
	operationTimeout := 200 * time.Millisecond
	if config.Ingester.Redis.OperationTimeout > 0 {
		operationTimeout = config.Ingester.Redis.OperationTimeout
	}
	lookupTimeout := defaultLookupTimeout
	if config.Ingester.OTLP.LookupTimeout > 0 {
		lookupTimeout = config.Ingester.OTLP.LookupTimeout
	}
	exportTimeout := defaultDownstreamTimeout
	if config.Ingester.OTLP.DownstreamTimeout > 0 {
		exportTimeout = config.Ingester.OTLP.DownstreamTimeout
	}

	// Initialize Redis cache if enabled
	var metricCache MetricUsageCache
	if config.Ingester.Redis.Enabled {
		cache, err := NewRedisMetricUsageCache(config.Ingester.Redis)
		if err != nil {
			slog.Error("ingester.cache.init.failed", "err", err)
		}
		if cache != nil {
			metricCache = cache
			slog.Info("ingester.cache.enabled", "addr", config.Ingester.Redis.Addr, "used_ttl", config.Ingester.Redis.UsedTTL, "unused_ttl", config.Ingester.Redis.UnusedTTL, "used_only", config.Ingester.Redis.UsedOnly)
		}
	}

	catalog := newCatalogSync(config, store)

	return &OtlpIngester{
		config:    config,
		forwarder: &forwarder{timeout: exportTimeout},
		lookups: &usageResolver{
			cache:            metricCache,
			store:            store,
			chunkSize:        lookupChunkSize,
			operationTimeout: operationTimeout,
			timeout:          lookupTimeout,
		},
		healthSrv:   health.NewServer(),
		allowedJobs: allowedJobs,
		deniedJobs:  deniedJobs,
		catalog:     catalog,
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
		i.forwarder.setExporter(exp)
		slog.InfoContext(ctx, "ingester.downstream.connected", "endpoint", i.config.Ingester.OTLP.DownstreamAddress)
	} else {
		slog.InfoContext(ctx, "ingester.downstream.disabled")
	}

	metricspb.RegisterMetricsServiceServer(grpcServer, i)
	healthpb.RegisterHealthServer(grpcServer, i.healthSrv)
	reflection.Register(grpcServer)

	slog.InfoContext(ctx, "ingester.starting", "address", i.config.Ingester.OTLP.ListenAddress)

	serveErrCh := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	i.healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	go i.catalog.run(ctx)

	select {
	case <-ctx.Done():
		i.healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		if d := i.config.Ingester.DrainDelay; d > 0 {
			time.Sleep(d)
		}
		_ = lis.Close()
		shutdownDone := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(shutdownDone)
		}()
		timeout := i.config.Ingester.GracefulShutdownTimeout
		if timeout <= 0 {
			timeout = defaultGracefulShutdownTimeout
		}
		timedOut := false
		select {
		case <-shutdownDone:
		case <-time.After(timeout):
			grpcServer.Stop()
			timedOut = true
		}

		i.catalog.flushOnShutdown(timeout)

		i.forwarder.close()
		i.lookups.close()
		i.catalog.close()
		if timedOut {
			return ctx.Err()
		}
		return nil
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
	}
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

func (i *OtlpIngester) Export(ctx context.Context, req *metricspb.ExportMetricsServiceRequest) (_ *metricspb.ExportMetricsServiceResponse, err error) {
	start := time.Now()
	rpcInFlight.Inc()
	defer func() {
		rpcInFlight.Dec()
		code := rpcOkCode
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
		rpcServerDurationSeconds.With(labels).Observe(time.Since(start).Seconds())
	}()

	namesSet, histogramBases, summaryBases, catalogMetrics, beforeMetricsCount, seenDatapoints := i.collectNamesAndCounts(req)
	if seenDatapoints > 0 {
		receiverReceivedMetricPointsTotal.Add(float64(seenDatapoints))
	}

	if len(namesSet) == 0 {
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	i.catalog.add(ctx, catalogMetrics)

	unused := i.lookups.newLookup(histogramBases, summaryBases).resolve(ctx, namesSet)
	slog.DebugContext(ctx, "ingester.unused_metrics.count", "count", len(unused))

	dryRun := i.config.Ingester.DryRun

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

	if !i.forwarder.enabled() {
		logExportSuccess(ctx, false, dryRun)
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}
	if err = i.forwarder.forward(ctx, req); err != nil {
		logExportFailure(ctx, err, true)
		return nil, err
	}
	logExportSuccess(ctx, true, dryRun)
	return &metricspb.ExportMetricsServiceResponse{}, nil
}

func (i *OtlpIngester) SetExporter(exp MetricsExporter) { i.forwarder.setExporter(exp) }

func (i *OtlpIngester) SetMetricCache(cache MetricUsageCache) {
	i.lookups.setCache(cache)
}

// IsReady reports whether the ingester is ready to serve OTLP traffic.
// It delegates to the gRPC health server and returns true only when the
// health status for the empty service name ("") is SERVING.
func (i *OtlpIngester) IsReady(ctx context.Context) bool {
	if i == nil || i.healthSrv == nil {
		return false
	}

	resp, err := i.healthSrv.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		slog.ErrorContext(ctx, "ingester.readiness.health_check_error", "err", err)
		return false
	}

	return resp.Status == healthpb.HealthCheckResponse_SERVING
}

func (i *OtlpIngester) collectNamesAndCounts(req *metricspb.ExportMetricsServiceRequest) (map[string]struct{}, map[string]struct{}, map[string]struct{}, []*metricsv1pb.Metric, int64, int64) {
	names := make(map[string]struct{})
	histogramBases := make(map[string]struct{})
	summaryBases := make(map[string]struct{})
	var catalogMetrics []*metricsv1pb.Metric
	var metricsCount int64
	var datapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			metricsCount += int64(len(sm.Metrics))
			for _, m := range sm.Metrics {
				name := prometheusMetricName(m)
				if name == "" {
					datapoints += int64(countMetricDatapoints(m))
					continue
				}
				names[name] = struct{}{}

				if _, isHistogram := m.Data.(*metricsv1pb.Metric_Histogram); isHistogram {
					// For histogram metrics, also collect derivative names used in Prometheus catalog
					histogramBases[name] = struct{}{}
					names[name+"_bucket"] = struct{}{}
					names[name+"_count"] = struct{}{}
					names[name+"_sum"] = struct{}{}
				}

				if _, isSummary := m.Data.(*metricsv1pb.Metric_Summary); isSummary {
					summaryBases[name] = struct{}{}
					names[name+"_count"] = struct{}{}
					names[name+"_sum"] = struct{}{}
				}

				if i.catalog != nil {
					catalogMetrics = append(catalogMetrics, m)
				}

				datapoints += int64(countMetricDatapoints(m))
			}
		}
	}
	return names, histogramBases, summaryBases, catalogMetrics, metricsCount, datapoints
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
				if name := prometheusMetricName(m); name != "" {
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
			return !shouldDropUnused(ctx.Resource, prometheusMetricName(ctx.Metric), unused, allowed, denied)
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

// FlushCatalog writes any metrics waiting for the catalog to the database.
func (i *OtlpIngester) FlushCatalog(ctx context.Context) {
	i.catalog.flush(ctx)
}

func RegisterOTLPFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&config.DefaultConfig.Ingester.OTLP.ListenAddress, "otlp-listen-address", ":4317", "The address the metrics ingester should listen on.")
	flagSet.StringVar(&config.DefaultConfig.Ingester.OTLP.DownstreamAddress, "otlp-downstream-address", "", "Optional downstream OTLP gRPC address to forward filtered metrics")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "otlp-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for OTLP server (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "otlp-max-send-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "Max gRPC send message size for OTLP server (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "otlp-downstream-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for downstream OTLP client (bytes)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "otlp-downstream-max-send-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "Max gRPC send message size for downstream OTLP client (bytes)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamTimeout, "otlp-downstream-timeout", config.DefaultConfig.Ingester.OTLP.DownstreamTimeout, "Timeout for each forward to the downstream OTLP endpoint, shortened when the incoming request's deadline leaves less time. 0 or negative uses the default.")
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
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectMinTimeout, "otlp-downstream-connect-min-timeout", config.DefaultConfig.Ingester.OTLP.DownstreamConnectMinTimeout, "Minimum connect timeout for downstream OTLP client dial attempts")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectBaseDelay, "otlp-downstream-connect-base-delay", config.DefaultConfig.Ingester.OTLP.DownstreamConnectBaseDelay, "Base delay for downstream OTLP client dial backoff")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectMaxDelay, "otlp-downstream-connect-max-delay", config.DefaultConfig.Ingester.OTLP.DownstreamConnectMaxDelay, "Max delay for downstream OTLP client dial backoff")
	flagSet.Float64Var(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectBackoffMultiplier, "otlp-downstream-connect-backoff-multiplier", config.DefaultConfig.Ingester.OTLP.DownstreamConnectBackoffMultiplier, "Multiplier applied to downstream OTLP client dial backoff")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.LookupTimeout, "otlp-lookup-timeout", config.DefaultConfig.Ingester.OTLP.LookupTimeout, "Time limit for looking up a request's metric usage in the cache and database, shortened to leave 1s of the incoming request's deadline for forwarding downstream. 0 or negative uses the default.")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.LookupChunkSize, "otlp-lookup-chunk-size", config.DefaultConfig.Ingester.OTLP.LookupChunkSize, "Batch size for database lookups when checking metric usage (default 500, SQLite max 999)")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.Redis.Enabled, "ingester-cache-enabled", config.DefaultConfig.Ingester.Redis.Enabled, "Enable metric usage caching")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Addr, "ingester-cache-addr", config.DefaultConfig.Ingester.Redis.Addr, "Cache server address (host:port)")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Username, "ingester-cache-username", config.DefaultConfig.Ingester.Redis.Username, "Cache username (optional)")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Password, "ingester-cache-password", config.DefaultConfig.Ingester.Redis.Password, "Cache password (optional)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.Redis.DB, "ingester-cache-db", config.DefaultConfig.Ingester.Redis.DB, "Cache database number")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.UsedTTL, "ingester-cache-used-ttl", config.DefaultConfig.Ingester.Redis.UsedTTL, "TTL for caching 'used' metric states")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.UnusedTTL, "ingester-cache-unused-ttl", config.DefaultConfig.Ingester.Redis.UnusedTTL, "TTL for caching 'unused' metric states")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.Redis.UsedOnly, "ingester-cache-used-only", config.DefaultConfig.Ingester.Redis.UsedOnly, "Only cache 'used' states, never cache 'unused' states")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.OperationTimeout, "ingester-cache-operation-timeout", config.DefaultConfig.Ingester.Redis.OperationTimeout, "Timeout for cache get/set operations, capped at a tenth of the time the usage lookup has left; the cap is at least 5ms")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.DialTimeout, "ingester-cache-dial-timeout", config.DefaultConfig.Ingester.Redis.DialTimeout, "TCP dial timeout for Redis connections")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.ConnWriteTimeout, "ingester-cache-conn-write-timeout", config.DefaultConfig.Ingester.Redis.ConnWriteTimeout, "Read/write timeout for Redis connections")
	flagSet.IntVar(&config.DefaultConfig.Ingester.Redis.BatchSize, "ingester-cache-batch-size", config.DefaultConfig.Ingester.Redis.BatchSize, "Max commands per DoMulti batch")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.CatalogSync.Enabled, "ingester-catalog-sync-enabled", config.DefaultConfig.Ingester.CatalogSync.Enabled, "Enable catalog population from OTLP traffic (disable inventory.metadata_sync_enabled on API server when using this)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.CatalogSync.FlushInterval, "ingester-catalog-sync-flush-interval", config.DefaultConfig.Ingester.CatalogSync.FlushInterval, "How often to flush buffered metrics to the catalog DB")
	flagSet.IntVar(&config.DefaultConfig.Ingester.CatalogSync.BufferSize, "ingester-catalog-sync-buffer-size", config.DefaultConfig.Ingester.CatalogSync.BufferSize, "Maximum number of unique metrics to buffer before dropping (per flush interval)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.CatalogSync.SeenTTL, "ingester-catalog-sync-seen-ttl", config.DefaultConfig.Ingester.CatalogSync.SeenTTL, "How long a metric is suppressed from re-flushing after first write (reduces duplicate DB upserts)")
}
