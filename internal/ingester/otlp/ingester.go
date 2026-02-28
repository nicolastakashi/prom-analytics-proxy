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

	exporter        MetricsExporter
	exportTimeout   time.Duration
	lookupChunkSize int
	healthSrv       *health.Server

	allowedJobs map[string]struct{}
	deniedJobs  map[string]struct{}

	metricCache    MetricUsageCache
	catalogBuf     *catalogBuffer
	catalogFlushIv time.Duration
	catalogSeenTTL time.Duration
}

func NewOtlpIngester(config *config.Config, dbProvider db.Provider) (*OtlpIngester, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if dbProvider == nil {
		return nil, fmt.Errorf("db provider cannot be nil")
	}

	allowedJobs, deniedJobs := buildJobSets(config)

	lookupChunkSize := 500 // default fallback
	if config.Ingester.OTLP.LookupChunkSize > 0 {
		lookupChunkSize = config.Ingester.OTLP.LookupChunkSize
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

	var catBuf *catalogBuffer
	var catFlushIv, catSeenTTL time.Duration
	if config.Ingester.CatalogSync.Enabled {
		bufSize := config.Ingester.CatalogSync.BufferSize
		if bufSize <= 0 {
			bufSize = 10000
		}
		catFlushIv = config.Ingester.CatalogSync.FlushInterval
		if catFlushIv <= 0 {
			catFlushIv = 30 * time.Second
		}
		catSeenTTL = config.Ingester.CatalogSync.SeenTTL
		if catSeenTTL <= 0 {
			catSeenTTL = time.Hour
		}

		// Wire up Redis seen cache when Redis is already configured, reusing the same
		// connection settings. Uses a separate key prefix ("catalog_seen:") to avoid
		// collisions with the metric usage cache ("metric_usage:").
		var seenCache CatalogSeenCache
		if config.Ingester.Redis.Enabled {
			rc, err := newRedisCatalogSeenCache(
				config.Ingester.Redis.Addr,
				config.Ingester.Redis.Username,
				config.Ingester.Redis.Password,
				config.Ingester.Redis.DB,
			)
			if err != nil {
				slog.Error("ingester.catalog.seen_cache.init.failed", "err", err)
			} else {
				seenCache = rc
				slog.Info("ingester.catalog.seen_cache.enabled", "addr", config.Ingester.Redis.Addr)
			}
		}

		catBuf = newCatalogBuffer(bufSize, catSeenTTL, seenCache)
		slog.Info("ingester.catalog.enabled",
			"flush_interval", catFlushIv,
			"buffer_size", bufSize,
			"seen_ttl", catSeenTTL,
			"redis_seen_cache", seenCache != nil)
	}

	return &OtlpIngester{
		config:          config,
		db:              dbProvider,
		exportTimeout:   5 * time.Second,
		lookupChunkSize: lookupChunkSize,
		healthSrv:       health.NewServer(),
		allowedJobs:     allowedJobs,
		deniedJobs:      deniedJobs,
		metricCache:     metricCache,
		catalogBuf:      catBuf,
		catalogFlushIv:  catFlushIv,
		catalogSeenTTL:  catSeenTTL,
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

	if i.catalogBuf != nil {
		go i.runCatalogFlusher(ctx)
	}

	select {
	case <-ctx.Done():
		i.healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		if d := i.config.Ingester.DrainDelay; d > 0 {
			time.Sleep(d)
		}
		_ = lis.Close()
		if i.exporter != nil {
			_ = i.exporter.Close()
		}
		if i.metricCache != nil {
			_ = i.metricCache.Close()
		}
		if i.catalogBuf != nil && i.catalogBuf.seenCache != nil {
			_ = i.catalogBuf.seenCache.Close()
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

	namesSet, histogramBases, catalogMetrics, beforeMetricsCount, seenDatapoints := i.collectNamesAndCounts(req)
	if seenDatapoints > 0 {
		receiverReceivedMetricPointsTotal.Add(float64(seenDatapoints))
	}

	if len(namesSet) == 0 {
		code = rpcOkCode
		return &metricspb.ExportMetricsServiceResponse{}, nil
	}

	if i.catalogBuf != nil && len(catalogMetrics) > 0 {
		i.bufferCatalogEntries(ctx, catalogMetrics)
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

func (i *OtlpIngester) SetMetricCache(cache MetricUsageCache) {
	i.metricCache = cache
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

func (i *OtlpIngester) collectNamesAndCounts(req *metricspb.ExportMetricsServiceRequest) (map[string]struct{}, map[string]struct{}, []*metricsv1pb.Metric, int64, int64) {
	names := make(map[string]struct{})
	histogramBases := make(map[string]struct{})
	var catalogMetrics []*metricsv1pb.Metric
	var metricsCount int64
	var datapoints int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			metricsCount += int64(len(sm.Metrics))
			for _, m := range sm.Metrics {
				name := m.GetName()
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

				if i.catalogBuf != nil {
					catalogMetrics = append(catalogMetrics, m)
				}

				datapoints += int64(countMetricDatapoints(m))
			}
		}
	}
	return names, histogramBases, catalogMetrics, metricsCount, datapoints
}

// bufferCatalogEntries queues metrics for the next catalog flush. It checks the in-memory
// seen map first (L1), then the distributed seen cache (L2, e.g. Redis) for any misses,
// so that metrics already in the catalog are not re-written unnecessarily.
func (i *OtlpIngester) bufferCatalogEntries(ctx context.Context, metrics []*metricsv1pb.Metric) {
	if len(metrics) == 0 {
		return
	}

	// L1: filter out metrics already suppressed by the in-memory seen map.
	candidates := i.catalogBuf.candidatesForBuffer(metrics)
	if len(candidates) == 0 {
		return
	}

	// L2: check distributed seen cache for any L1 misses (typically only after restart).
	remotelySeenNames := make(map[string]bool)
	if i.catalogBuf.seenCache != nil {
		names := make([]string, 0, len(candidates))
		for _, m := range candidates {
			names = append(names, m.GetName())
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		seen, err := i.catalogBuf.seenCache.HasMany(cacheCtx, names)
		if err != nil {
			slog.DebugContext(ctx, "ingester.catalog.seen_cache.lookup.failed", "err", err)
		} else {
			remotelySeenNames = seen
		}
	}

	i.catalogBuf.addBatch(candidates, remotelySeenNames)
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
	histogramStates := i.initHistogramStates(histogramBases)

	chunkSize := i.lookupChunkSize
	batch := make([]string, 0, chunkSize)

	flush := func() bool {
		if len(batch) == 0 {
			return true
		}

		usedFromCache, unusedFromCache, misses := i.lookupCache(ctx, batch)
		i.processCacheHits(usedFromCache, unusedFromCache, histogramBases, histogramStates, unused, ctx)

		if len(misses) > 0 {
			if !i.processDBMisses(ctx, misses, histogramBases, histogramStates, unused) {
				return false
			}
		}

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

	i.reconcileHistogramBases(histogramStates, unused, ctx)
	return unused, true
}

// initHistogramStates initializes histogram variant state tracking.
func (i *OtlpIngester) initHistogramStates(histogramBases map[string]struct{}) map[string]*histogramVariantState {
	states := make(map[string]*histogramVariantState, len(histogramBases))
	for baseName := range histogramBases {
		states[baseName] = &histogramVariantState{}
	}
	return states
}

// lookupCache queries the cache for metric usage states and partitions metrics into hits/misses.
func (i *OtlpIngester) lookupCache(ctx context.Context, batch []string) (usedFromCache, unusedFromCache, misses []string) {
	if i.metricCache == nil {
		return nil, nil, batch
	}

	cacheTimeout := i.computeCacheTimeout(ctx)
	cacheCtx, cancel := context.WithTimeout(ctx, cacheTimeout)
	defer cancel()

	cacheLookupStart := time.Now()
	cacheStates, err := i.metricCache.GetStates(cacheCtx, batch)
	if err != nil {
		i.handleCacheError(ctx, err, cacheTimeout)
		return nil, nil, batch
	}

	ingesterMetricCacheLookupSeconds.Observe(time.Since(cacheLookupStart).Seconds())
	return i.partitionCacheResults(batch, cacheStates)
}

// computeCacheTimeout calculates the timeout for cache operations.
func (i *OtlpIngester) computeCacheTimeout(ctx context.Context) time.Duration {
	cacheTimeout := 50 * time.Millisecond
	if exportTimeout := computeExportTimeout(ctx, i.exportTimeout); exportTimeout > 0 && exportTimeout/10 < cacheTimeout {
		cacheTimeout = exportTimeout / 10
	}
	return cacheTimeout
}

// handleCacheError logs cache errors and increments metrics.
func (i *OtlpIngester) handleCacheError(ctx context.Context, err error, timeout time.Duration) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		slog.DebugContext(ctx, "ingester.cache.get.timeout", "timeout", timeout)
	} else {
		slog.DebugContext(ctx, "ingester.cache.get.failed", "err", err)
	}
	ingesterMetricCacheErrorsTotal.With(prometheus.Labels{"operation": "get"}).Inc()
}

// partitionCacheResults splits metrics into used, unused, and misses based on cache results.
func (i *OtlpIngester) partitionCacheResults(batch []string, cacheStates map[string]MetricUsageState) (used, unused, misses []string) {
	for _, name := range batch {
		state := cacheStates[name]
		switch state {
		case StateUsed:
			used = append(used, name)
			ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "used"}).Inc()
		case StateUnused:
			unused = append(unused, name)
			ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "unused"}).Inc()
		case StateUnknown:
			misses = append(misses, name)
			ingesterMetricCacheMissesTotal.Inc()
		}
	}
	return used, unused, misses
}

// processCacheHits updates histogram states and unused map based on cache hits.
func (i *OtlpIngester) processCacheHits(usedFromCache, unusedFromCache []string, histogramBases map[string]struct{}, histogramStates map[string]*histogramVariantState, unused map[string]struct{}, ctx context.Context) {
	for _, name := range usedFromCache {
		i.updateHistogramState(name, histogramBases, histogramStates, false)
	}

	for _, name := range unusedFromCache {
		if _, isVariant := i.isHistogramVariant(name, histogramBases); isVariant {
			i.updateHistogramState(name, histogramBases, histogramStates, true)
		} else {
			unused[name] = struct{}{}
			slog.DebugContext(ctx, "ingester.unused_metric.found", "metric_name", name, "source", "cache")
		}
	}
}

// updateHistogramState updates the histogram variant state based on the metric name and unused status.
func (i *OtlpIngester) updateHistogramState(name string, histogramBases map[string]struct{}, histogramStates map[string]*histogramVariantState, isUnused bool) {
	baseName, isVariant := i.isHistogramVariant(name, histogramBases)
	if !isVariant {
		return
	}

	state, ok := histogramStates[baseName]
	if !ok {
		return
	}

	switch name {
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
}

// processDBMisses queries the database for cache misses and updates states.
func (i *OtlpIngester) processDBMisses(ctx context.Context, misses []string, histogramBases map[string]struct{}, histogramStates map[string]*histogramVariantState, unused map[string]struct{}) bool {
	t0 := time.Now()
	metas, err := i.db.GetSeriesMetadataByNames(ctx, misses, "")
	if err != nil {
		slog.ErrorContext(ctx, "ingester.lookup.failed_skipping_drops", "err", err)
		processorLookupErrorsTotal.Inc()
		return false
	}
	processorLookupDurationSeconds.Observe(time.Since(t0).Seconds())

	cacheWriteBack := i.processMetadata(metas, histogramBases, histogramStates, unused, ctx)
	i.writeBackToCache(ctx, cacheWriteBack)
	return true
}

// processMetadata processes database metadata and updates histogram states and unused map.
func (i *OtlpIngester) processMetadata(metas []models.MetricMetadata, histogramBases map[string]struct{}, histogramStates map[string]*histogramVariantState, unused map[string]struct{}, ctx context.Context) map[string]MetricUsageState {
	cacheWriteBack := make(map[string]MetricUsageState)

	for _, mm := range metas {
		isUnused := metricMetadataUnused(mm)

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
			if isUnused {
				unused[mm.Name] = struct{}{}
				slog.DebugContext(ctx, "ingester.unused_metric.found", "metric_name", mm.Name, "source", "db")
			}
		}

		if isUnused {
			cacheWriteBack[mm.Name] = StateUnused
		} else {
			cacheWriteBack[mm.Name] = StateUsed
		}
	}

	return cacheWriteBack
}

// writeBackToCache writes cache states back to Redis (best-effort).
func (i *OtlpIngester) writeBackToCache(ctx context.Context, cacheWriteBack map[string]MetricUsageState) {
	if i.metricCache == nil || len(cacheWriteBack) == 0 {
		return
	}

	cacheTimeout := i.computeCacheTimeout(ctx)
	cacheCtx, cancel := context.WithTimeout(ctx, cacheTimeout)
	defer cancel()

	cacheWriteStart := time.Now()
	if err := i.metricCache.SetStates(cacheCtx, cacheWriteBack); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			slog.DebugContext(ctx, "ingester.cache.set.timeout", "timeout", cacheTimeout)
		} else {
			slog.DebugContext(ctx, "ingester.cache.set.failed", "err", err)
		}
		ingesterMetricCacheErrorsTotal.With(prometheus.Labels{"operation": "set"}).Inc()
	}
	ingesterMetricCacheWriteSeconds.Observe(time.Since(cacheWriteStart).Seconds())
}

// reconcileHistogramBases determines which histogram base metrics are unused based on variant states.
func (i *OtlpIngester) reconcileHistogramBases(histogramStates map[string]*histogramVariantState, unused map[string]struct{}, ctx context.Context) {
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
			delete(unused, baseName)
		}
	}
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

func (i *OtlpIngester) runCatalogFlusher(ctx context.Context) {
	ticker := time.NewTicker(i.catalogFlushIv)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			i.flushCatalog(ctx)
		}
	}
}

// FlushCatalog snapshots the catalog buffer and writes any pending metrics to the DB.
// It is called automatically by the background flusher, but can also be called directly
// in tests or on shutdown to ensure no buffered entries are lost.
func (i *OtlpIngester) FlushCatalog(ctx context.Context) {
	i.flushCatalog(ctx)
}

func (i *OtlpIngester) flushCatalog(ctx context.Context) {
	items := i.catalogBuf.snapshot()
	if len(items) == 0 {
		return
	}

	start := time.Now()
	if err := i.db.UpsertMetricsCatalog(ctx, items); err != nil {
		slog.ErrorContext(ctx, "ingester.catalog.flush.failed",
			"err", err,
			"metrics_count", len(items))
		catalogFlushErrorsTotal.Inc()
		return
	}

	elapsed := time.Since(start)
	catalogFlushDurationSeconds.Observe(elapsed.Seconds())
	catalogFlushMetricsTotal.Add(float64(len(items)))
	slog.DebugContext(ctx, "ingester.catalog.flush.success",
		"metrics_count", len(items),
		"duration_ms", elapsed.Milliseconds())

	// Best-effort: propagate seen state to Redis so restarts don't cause a re-flush burst.
	if i.catalogBuf.seenCache != nil {
		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name)
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if err := i.catalogBuf.seenCache.MarkMany(cacheCtx, names, i.catalogSeenTTL); err != nil {
			slog.DebugContext(ctx, "ingester.catalog.seen_cache.mark.failed", "err", err)
		}
	}
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
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectMinTimeout, "otlp-downstream-connect-min-timeout", config.DefaultConfig.Ingester.OTLP.DownstreamConnectMinTimeout, "Minimum connect timeout for downstream OTLP client dial attempts")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectBaseDelay, "otlp-downstream-connect-base-delay", config.DefaultConfig.Ingester.OTLP.DownstreamConnectBaseDelay, "Base delay for downstream OTLP client dial backoff")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectMaxDelay, "otlp-downstream-connect-max-delay", config.DefaultConfig.Ingester.OTLP.DownstreamConnectMaxDelay, "Max delay for downstream OTLP client dial backoff")
	flagSet.Float64Var(&config.DefaultConfig.Ingester.OTLP.DownstreamConnectBackoffMultiplier, "otlp-downstream-connect-backoff-multiplier", config.DefaultConfig.Ingester.OTLP.DownstreamConnectBackoffMultiplier, "Multiplier applied to downstream OTLP client dial backoff")
	flagSet.IntVar(&config.DefaultConfig.Ingester.OTLP.LookupChunkSize, "otlp-lookup-chunk-size", config.DefaultConfig.Ingester.OTLP.LookupChunkSize, "Batch size for database lookups when checking metric usage (default 500, SQLite max 999)")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.Redis.Enabled, "ingester-cache-enabled", config.DefaultConfig.Ingester.Redis.Enabled, "Enable metric usage caching")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Addr, "ingester-cache-addr", config.DefaultConfig.Ingester.Redis.Addr, "Cache server address (host:port)")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Username, "ingester-cache-username", config.DefaultConfig.Ingester.Redis.Username, "Cache username (optional)")
	flagSet.StringVar(&config.DefaultConfig.Ingester.Redis.Password, "ingester-cache-password", config.DefaultConfig.Ingester.Redis.Password, "Cache password (optional)")
	flagSet.IntVar(&config.DefaultConfig.Ingester.Redis.DB, "ingester-cache-db", config.DefaultConfig.Ingester.Redis.DB, "Cache database number")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.UsedTTL, "ingester-cache-used-ttl", config.DefaultConfig.Ingester.Redis.UsedTTL, "TTL for caching 'used' metric states")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.Redis.UnusedTTL, "ingester-cache-unused-ttl", config.DefaultConfig.Ingester.Redis.UnusedTTL, "TTL for caching 'unused' metric states")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.Redis.UsedOnly, "ingester-cache-used-only", config.DefaultConfig.Ingester.Redis.UsedOnly, "Only cache 'used' states, never cache 'unused' states")
	flagSet.BoolVar(&config.DefaultConfig.Ingester.CatalogSync.Enabled, "ingester-catalog-sync-enabled", config.DefaultConfig.Ingester.CatalogSync.Enabled, "Enable catalog population from OTLP traffic (disable inventory.metadata_sync_enabled on API server when using this)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.CatalogSync.FlushInterval, "ingester-catalog-sync-flush-interval", config.DefaultConfig.Ingester.CatalogSync.FlushInterval, "How often to flush buffered metrics to the catalog DB")
	flagSet.IntVar(&config.DefaultConfig.Ingester.CatalogSync.BufferSize, "ingester-catalog-sync-buffer-size", config.DefaultConfig.Ingester.CatalogSync.BufferSize, "Maximum number of unique metrics to buffer before dropping (per flush interval)")
	flagSet.DurationVar(&config.DefaultConfig.Ingester.CatalogSync.SeenTTL, "ingester-catalog-sync-seen-ttl", config.DefaultConfig.Ingester.CatalogSync.SeenTTL, "How long a metric is suppressed from re-flushing after first write (reduces duplicate DB upserts)")
}
