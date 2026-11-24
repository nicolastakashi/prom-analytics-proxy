package otlp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
)

// usageResolver resolves which metrics are unused from the cache, then the
// database. It holds what a usageLookup uses: the cache (nil: none), the
// store, the lookup chunk size, the cache operation timeout and the lookup
// timeout.
type usageResolver struct {
	cache            MetricUsageCache
	store            Store
	chunkSize        int
	operationTimeout time.Duration
	timeout          time.Duration
}

// usageLookup is the usage lookup of one Export request. It asks the cache,
// then the database, which series are unused, and decides which of the
// request's metrics are. A histogram is unused only when its _bucket, _count
// and _sum series all are; a summary only when its own name, _count and _sum
// all are. A used series and an unknown one count alike: neither confirms a
// metric unused.
type usageLookup struct {
	r              *usageResolver
	histogramBases map[string]struct{}
	summaryBases   map[string]struct{}
	unusedSeries   map[string]struct{}
	databaseFailed bool // a database call failed; skip the database
}

func (r *usageResolver) newLookup(histogramBases, summaryBases map[string]struct{}) *usageLookup {
	return &usageLookup{
		r:              r,
		histogramBases: histogramBases,
		summaryBases:   summaryBases,
		unusedSeries:   make(map[string]struct{}),
	}
}

// resolve returns the metrics among names confirmed unused, looking each
// chunk of names up in the cache first and the rest in the database. The
// whole lookup, cache calls included, runs under the lookup timeout, capped
// to leave forwardReserve of ctx's time.
func (l *usageLookup) resolve(ctx context.Context, names map[string]struct{}) map[string]struct{} {
	ctx, cancel := context.WithTimeout(ctx, l.r.lookupTimeout(ctx))
	defer cancel()

	chunkSize := l.r.chunkSize
	batch := make([]string, 0, chunkSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		unusedFromCache, misses := l.lookupCache(ctx, batch)
		l.confirmUnused(ctx, "cache", unusedFromCache)
		if len(misses) > 0 {
			// A DB error for this chunk fails open (see lookupDatabase); the
			// remaining chunks still read the cache.
			l.confirmUnused(ctx, "db", l.lookupDatabase(ctx, misses))
		}
		batch = batch[:0]
	}
	for n := range names {
		batch = append(batch, n)
		if len(batch) == chunkSize {
			flush()
		}
	}
	flush()
	return l.unusedMetrics(ctx, names)
}

// confirmUnused records series that source confirmed unused.
func (l *usageLookup) confirmUnused(ctx context.Context, source string, series []string) {
	for _, name := range series {
		l.unusedSeries[name] = struct{}{}
		if !l.isHistogramVariant(name) && !l.isSummaryPart(name) {
			slog.DebugContext(ctx, "ingester.unused_metric.found", "metric_name", name, "source", source)
		}
	}
}

// unusedMetrics returns the names among names that are confirmed unused. A
// histogram or summary is judged by all its series; a name that is one of its
// series is judged with it and never returned on its own.
func (l *usageLookup) unusedMetrics(ctx context.Context, names map[string]struct{}) map[string]struct{} {
	unused := make(map[string]struct{})
	for name := range names {
		switch {
		case l.isHistogramBase(name):
			if l.allUnused(name+"_bucket", name+"_count", name+"_sum") {
				unused[name] = struct{}{}
				slog.DebugContext(ctx, "ingester.unused_histogram.found", "metric_name", name)
			}
		case l.isHistogramVariant(name):
		case l.isSummaryBase(name):
			if l.allUnused(name, name+"_count", name+"_sum") {
				unused[name] = struct{}{}
				slog.DebugContext(ctx, "ingester.unused_summary.found", "metric_name", name)
			}
		case l.isSummaryPart(name):
		default:
			if _, ok := l.unusedSeries[name]; ok {
				unused[name] = struct{}{}
			}
		}
	}
	return unused
}

func (l *usageLookup) allUnused(series ...string) bool {
	for _, name := range series {
		if _, ok := l.unusedSeries[name]; !ok {
			return false
		}
	}
	return true
}

func (l *usageLookup) isHistogramBase(name string) bool {
	_, ok := l.histogramBases[name]
	return ok
}

func (l *usageLookup) isSummaryBase(name string) bool {
	_, ok := l.summaryBases[name]
	return ok
}

// isHistogramVariant reports whether name is the _bucket, _count or _sum
// series of a histogram in the request.
func (l *usageLookup) isHistogramVariant(name string) bool {
	return hasBaseIn(l.histogramBases, name, "_bucket", "_count", "_sum")
}

// isSummaryPart reports whether name is a summary in the request, or its
// _count or _sum series.
func (l *usageLookup) isSummaryPart(name string) bool {
	return l.isSummaryBase(name) || hasBaseIn(l.summaryBases, name, "_count", "_sum")
}

// hasBaseIn reports whether name is a base in bases followed by one of
// suffixes.
func hasBaseIn(bases map[string]struct{}, name string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if base, ok := strings.CutSuffix(name, suffix); ok {
			if _, found := bases[base]; found {
				return true
			}
		}
	}
	return false
}

// lookupCache returns the names of batch the cache confirms unused, and the
// names it does not resolve, which go to the database: misses, and names a
// failed read did not answer. Once ctx's deadline has expired, the cache is
// not read and every name goes to the database.
func (l *usageLookup) lookupCache(ctx context.Context, batch []string) (unused, toDB []string) {
	if l.r.cache == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, batch
	}

	cacheTimeout := l.r.cacheTimeout(ctx)
	cacheCtx, cancel := context.WithTimeout(ctx, cacheTimeout)
	defer cancel()

	cacheLookupStart := time.Now()
	cacheStates, err := l.r.cache.GetStates(cacheCtx, batch)
	ingesterMetricCacheLookupSeconds.Observe(time.Since(cacheLookupStart).Seconds())
	if err != nil {
		recordCacheFailure(cacheCtx, cacheGet, err, cacheTimeout)
	}
	return partitionCacheResults(batch, cacheStates)
}

// lookupDatabase returns the names the database confirms unused, and writes
// every state it reads back to the cache.
//
// A DB error fails open for these names: none is confirmed unused, and the
// surrounding lookup and Export carry on, so the batch is still forwarded
// downstream instead of being silently discarded.
// See https://github.com/nicolastakashi/prom-analytics-proxy/issues/569.
// A lookup the database gate refuses fails open the same way, but is neither
// observed nor counted as a failed lookup. After any failed call, the lookup
// does not call the database again: later names fail open like a refused
// lookup.
func (l *usageLookup) lookupDatabase(ctx context.Context, names []string) []string {
	if l.databaseFailed {
		slog.DebugContext(ctx, "ingester.lookup.database_skipped", "names_count", len(names))
		countUnresolved(ctx, len(names), reasonDatabaseUnavailable)
		return nil
	}
	t0 := time.Now()
	metas, err := l.r.store.GetSeriesMetadataByNames(ctx, names, "")
	if err != nil {
		l.databaseFailed = true
		if errors.Is(err, errDatabaseUnavailable) {
			slog.DebugContext(ctx, "ingester.lookup.database_unavailable", "names_count", len(names))
			countUnresolved(ctx, len(names), reasonDatabaseUnavailable)
			return nil
		}
		slog.ErrorContext(ctx, "ingester.lookup.failed_treating_as_used", "err", err, "names_count", len(names))
		if reason := failureReason(ctx, err); reason != "" {
			processorLookupErrorsTotal.Inc()
			processorLookupDurationSeconds.WithLabelValues(reason).Observe(time.Since(t0).Seconds())
			countUnresolved(ctx, len(names), unresolvedReason(reason))
		}
		return nil
	}
	processorLookupDurationSeconds.WithLabelValues(outcomeOK).Observe(time.Since(t0).Seconds())
	usageResolutionsTotal.WithLabelValues(sourceDatabase).Add(float64(len(names)))

	var unused []string
	cacheWriteBack := make(map[string]MetricUsageState, len(metas))
	for _, mm := range metas {
		if metricMetadataUnused(mm) {
			unused = append(unused, mm.Name)
			cacheWriteBack[mm.Name] = StateUnused
		} else {
			cacheWriteBack[mm.Name] = StateUsed
		}
	}
	l.r.writeBack(ctx, cacheWriteBack)
	return unused
}

// setCache replaces the cache; nil disables it.
func (r *usageResolver) setCache(cache MetricUsageCache) { r.cache = cache }

// close closes the cache, if any.
func (r *usageResolver) close() {
	if r.cache != nil {
		_ = r.cache.Close()
	}
}

// lookupTimeout returns the lookup timeout, capped at the time ctx has left
// less forwardReserve; zero or negative when ctx has no more than that left.
func (r *usageResolver) lookupTimeout(ctx context.Context) time.Duration {
	timeout := r.timeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline)-forwardReserve)
	}
	return timeout
}

// cacheTimeout returns the cache operation timeout, capped at a tenth of the
// time the usage lookup's ctx has left; the cap is at least minCacheTimeout.
// That ctx always has a deadline.
func (r *usageResolver) cacheTimeout(ctx context.Context) time.Duration {
	deadline, _ := ctx.Deadline()
	return min(r.operationTimeout, max(time.Until(deadline)/10, minCacheTimeout))
}

// recordCacheFailure logs and counts a cache call that failed with err under
// ctx, once per call. A call for a canceled request is neither logged nor
// counted.
func recordCacheFailure(ctx context.Context, operation string, err error, timeout time.Duration) {
	reason := failureReason(ctx, err)
	switch {
	case reason == "":
		return
	case operation == cacheGet && reason == reasonTimeout:
		slog.DebugContext(ctx, "ingester.cache.get.timeout", "timeout", timeout)
	case operation == cacheGet:
		slog.DebugContext(ctx, "ingester.cache.get.failed", "err", err)
	case reason == reasonTimeout:
		slog.DebugContext(ctx, "ingester.cache.set.timeout", "timeout", timeout)
	default:
		slog.DebugContext(ctx, "ingester.cache.set.failed", "err", err)
	}
	ingesterMetricCacheErrorsTotal.WithLabelValues(operation, reason).Inc()
}

// countUnresolved counts n names the lookup left unresolved for reason, in
// both the resolutions and the unresolved counters. A canceled request is
// not counted.
func countUnresolved(ctx context.Context, n int, reason string) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	usageResolutionsTotal.WithLabelValues(sourceUnresolved).Add(float64(n))
	processorUnresolvedTotal.WithLabelValues(reason).Add(float64(n))
}

// failureReason classifies a cache or database call that failed with err
// under ctx: reasonTimeout when ctx's deadline expired or the database timed
// the statement out, reasonError otherwise, and "" when the request was
// canceled, which says nothing about the cache or the database and is not
// counted.
func failureReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || db.IsTimeout(err):
		return reasonTimeout
	default:
		return reasonError
	}
}

// partitionCacheResults splits batch into the names the cache confirms
// unused and the names it does not resolve, counting hits, misses and cache
// resolutions. A name the cache did not read, absent from cacheStates, goes
// to the database like a miss but is not counted as one.
func partitionCacheResults(batch []string, cacheStates map[string]MetricUsageState) (unused, toDB []string) {
	hits := 0
	for _, name := range batch {
		state, read := cacheStates[name]
		switch {
		case !read:
			toDB = append(toDB, name)
		case state == StateUsed:
			hits++
			ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "used"}).Inc()
		case state == StateUnused:
			hits++
			unused = append(unused, name)
			ingesterMetricCacheHitsTotal.With(prometheus.Labels{"state": "unused"}).Inc()
		default:
			toDB = append(toDB, name)
			ingesterMetricCacheMissesTotal.Inc()
		}
	}
	usageResolutionsTotal.WithLabelValues(sourceCache).Add(float64(hits))
	return unused, toDB
}

// writeBack writes states read from the database back to the cache
// (best-effort).
func (r *usageResolver) writeBack(ctx context.Context, cacheWriteBack map[string]MetricUsageState) {
	if r.cache == nil || len(cacheWriteBack) == 0 {
		return
	}

	cacheTimeout := r.cacheTimeout(ctx)
	cacheCtx, cancel := context.WithTimeout(ctx, cacheTimeout)
	defer cancel()

	cacheWriteStart := time.Now()
	if err := r.cache.SetStates(cacheCtx, cacheWriteBack); err != nil {
		recordCacheFailure(cacheCtx, cacheSet, err, cacheTimeout)
	}
	ingesterMetricCacheWriteSeconds.Observe(time.Since(cacheWriteStart).Seconds())
}

// metricMetadataUnused reports whether mm is confirmed unused. This must be
// sourced from metrics_usage_summary.is_unused (via mm.IsUnused), not
// recomputed from the four usage counts - a metric that has never been
// evaluated by RefreshMetricsUsageSummary also has all-zero counts, and
// counts alone cannot distinguish "confirmed unused" from "not evaluated
// yet". See https://github.com/nicolastakashi/prom-analytics-proxy/issues/570
// and https://github.com/nicolastakashi/prom-analytics-proxy/issues/571.
func metricMetadataUnused(mm models.MetricMetadata) bool {
	return mm.IsUnused
}
