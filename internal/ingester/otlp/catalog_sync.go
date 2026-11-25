package otlp

import (
	"context"
	"log/slog"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	metricsv1pb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// catalogSync populates the metric catalog from OTLP traffic: it buffers the
// metrics of each request that were not written recently, and writes them to
// the database every flush interval. Which metrics were written recently is
// tracked in the seen cache when there is one, in memory otherwise.
//
// Every method of a nil catalogSync does nothing, so a disabled catalog sync
// needs no checks by its callers.
type catalogSync struct {
	buf           *catalogBuffer
	seenCache     CatalogSeenCache // nil: tracked in memory
	store         Store
	flushInterval time.Duration
}

// newCatalogSync returns the catalog sync cfg configures, writing to store,
// or nil when catalog sync is disabled.
func newCatalogSync(cfg *config.Config, store Store) *catalogSync {
	if !cfg.Ingester.CatalogSync.Enabled {
		return nil
	}
	bufSize := cfg.Ingester.CatalogSync.BufferSize
	if bufSize <= 0 {
		bufSize = defaultCatalogBufferSize
	}
	flushInterval := cfg.Ingester.CatalogSync.FlushInterval
	if flushInterval <= 0 {
		flushInterval = defaultCatalogFlushInterval
	}
	seenTTL := cfg.Ingester.CatalogSync.SeenTTL
	if seenTTL <= 0 {
		seenTTL = defaultCatalogSeenTTL
	}

	// Wire up Redis seen cache when Redis is already configured, reusing the same
	// connection settings. Uses a separate key prefix ("catalog_seen:") to avoid
	// collisions with the metric usage cache ("metric_usage:").
	var seenCache CatalogSeenCache
	if cfg.Ingester.Redis.Enabled {
		rc, err := newRedisCatalogSeenCache(
			cfg.Ingester.Redis.Addr,
			cfg.Ingester.Redis.Username,
			cfg.Ingester.Redis.Password,
			cfg.Ingester.Redis.DB,
			cfg.Ingester.Redis.DialTimeout,
			cfg.Ingester.Redis.ConnWriteTimeout,
			cfg.Ingester.Redis.BatchSize,
		)
		if err != nil {
			slog.Error("ingester.catalog.seen_cache.init.failed", "err", err)
		} else {
			seenCache = rc
			slog.Info("ingester.catalog.seen_cache.enabled", "addr", cfg.Ingester.Redis.Addr)
		}
	}

	s := newCatalogSyncWith(store, seenCache, bufSize, flushInterval, seenTTL)
	slog.Info("ingester.catalog.enabled",
		"flush_interval", flushInterval,
		"buffer_size", bufSize,
		"seen_ttl", seenTTL,
		"redis_seen_cache", seenCache != nil)
	return s
}

// newCatalogSyncWith returns a catalog sync writing to store, tracking written
// metrics in seenCache, or in memory when seenCache is nil.
func newCatalogSyncWith(store Store, seenCache CatalogSeenCache, bufSize int, flushInterval, seenTTL time.Duration) *catalogSync {
	return &catalogSync{
		buf:           newCatalogBuffer(bufSize, seenTTL, seenCache == nil),
		seenCache:     seenCache,
		store:         store,
		flushInterval: flushInterval,
	}
}

// add queues the metrics not written recently for the next flush. Metrics
// already in the catalog are not queued again: the in-memory seen map
// (without a seen cache) or the seen cache (with one) tells which.
func (s *catalogSync) add(ctx context.Context, metrics []*metricsv1pb.Metric) {
	if s == nil || len(metrics) == 0 {
		return
	}

	candidates := s.buf.candidatesForBuffer(metrics)
	if len(candidates) == 0 {
		return
	}

	// With a seen cache, every candidate not already pending is checked in it.
	remotelySeenNames := make(map[string]bool)
	if s.seenCache != nil {
		names := make([]string, 0, len(candidates))
		names = append(names, prometheusCatalogNames(candidates)...)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		seen, err := s.seenCache.HasMany(cacheCtx, names)
		if err != nil {
			slog.DebugContext(ctx, "ingester.catalog.seen_cache.lookup.failed", "err", err)
		} else {
			remotelySeenNames = seen
		}
	}

	s.buf.addBatch(candidates, remotelySeenNames)
}

// run flushes every flush interval until ctx is done.
func (s *catalogSync) run(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flush(ctx)
		}
	}
}

// flushOnShutdown flushes once, within timeout, or within the default
// graceful shutdown timeout when timeout is not positive.
func (s *catalogSync) flushOnShutdown(timeout time.Duration) {
	if s == nil {
		return
	}
	if timeout <= 0 {
		timeout = defaultGracefulShutdownTimeout
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.flush(flushCtx)
}

// flush writes the buffered metrics to the database. Metrics a failed write
// leaves behind are buffered again for the next flush.
func (s *catalogSync) flush(ctx context.Context) {
	if s == nil {
		return
	}
	items := s.buf.drain()
	if len(items) == 0 {
		return
	}

	start := time.Now()
	if err := s.store.UpsertMetricsCatalog(ctx, items); err != nil {
		// Keep items in memory for a future retry on transient DB errors.
		s.buf.requeue(items)
		slog.ErrorContext(ctx, "ingester.catalog.flush.failed",
			"err", err,
			"metrics_count", len(items))
		catalogFlushErrorsTotal.Inc()
		return
	}
	s.buf.markFlushed(items, time.Now())

	elapsed := time.Since(start)
	catalogFlushDurationSeconds.Observe(elapsed.Seconds())
	catalogFlushMetricsTotal.Add(float64(len(items)))
	slog.DebugContext(ctx, "ingester.catalog.flush.success",
		"metrics_count", len(items),
		"duration_ms", elapsed.Milliseconds())

	// Best-effort: propagate seen state to the seen cache so restarts don't cause a re-flush burst.
	if s.seenCache != nil {
		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name)
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if err := s.seenCache.MarkMany(cacheCtx, names, s.buf.seenTTL); err != nil {
			slog.DebugContext(ctx, "ingester.catalog.seen_cache.mark.failed", "err", err)
		}
	}
}

// close closes the seen cache, if any.
func (s *catalogSync) close() {
	if s == nil || s.seenCache == nil {
		return
	}
	_ = s.seenCache.Close()
}
