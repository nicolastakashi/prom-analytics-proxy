package ingester

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	otlppkg "github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// TestExport_DropsHistogramOrSummaryOnlyWhenEveryPartIsUnused verifies a
// histogram or summary is dropped only when every series it is queried by is
// confirmed unused, whether the cache or the database confirms each one, and
// is kept when any of them is used or unknown. A histogram is judged by its
// _bucket, _count and _sum series; a summary by its own name, _count and
// _sum.
func TestExport_DropsHistogramOrSummaryOnlyWhenEveryPartIsUnused(t *testing.T) {
	const (
		used    = "used"
		unused  = "unused"
		unknown = "" // neither the cache nor the database knows it
	)
	for _, tc := range []struct {
		name   string
		metric *metricspb.Metric
		cache  map[string]string // series -> used/unused
		db     map[string]string // series -> used/unused
		drop   bool
	}{
		{
			name:   "summary unused in the database",
			metric: buildSummaryMetric("rpc_latency", 1),
			db:     map[string]string{"rpc_latency": unused, "rpc_latency_count": unused, "rpc_latency_sum": unused},
			drop:   true,
		},
		{
			name:   "summary used by its own name",
			metric: buildSummaryMetric("rpc_latency", 1),
			db:     map[string]string{"rpc_latency": used, "rpc_latency_count": unused, "rpc_latency_sum": unused},
		},
		{
			name:   "summary used by _count",
			metric: buildSummaryMetric("rpc_latency", 1),
			db:     map[string]string{"rpc_latency": unused, "rpc_latency_count": used, "rpc_latency_sum": unused},
		},
		{
			name:   "summary with _sum unknown",
			metric: buildSummaryMetric("rpc_latency", 1),
			db:     map[string]string{"rpc_latency": unused, "rpc_latency_count": unused, "rpc_latency_sum": unknown},
		},
		{
			name:   "summary with its own name unknown",
			metric: buildSummaryMetric("rpc_latency", 1),
			db:     map[string]string{"rpc_latency": unknown, "rpc_latency_count": unused, "rpc_latency_sum": unused},
		},
		{
			name:   "summary unused in the cache",
			metric: buildSummaryMetric("rpc_latency", 1),
			cache:  map[string]string{"rpc_latency": unused, "rpc_latency_count": unused, "rpc_latency_sum": unused},
			drop:   true,
		},
		{
			name:   "summary used in the cache",
			metric: buildSummaryMetric("rpc_latency", 1),
			cache:  map[string]string{"rpc_latency": used, "rpc_latency_count": unused, "rpc_latency_sum": unused},
		},
		{
			name:   "summary unused across the cache and the database",
			metric: buildSummaryMetric("rpc_latency", 1),
			cache:  map[string]string{"rpc_latency": unused},
			db:     map[string]string{"rpc_latency_count": unused, "rpc_latency_sum": unused},
			drop:   true,
		},
		{
			name:   "metric named like a series of no histogram or summary",
			metric: buildGaugeMetric("jobs_count", 1),
			db:     map[string]string{"jobs_count": unused},
			drop:   true,
		},
		{
			name:   "histogram unused in the database",
			metric: buildHistogramMetric("req_duration", 1),
			db:     map[string]string{"req_duration_bucket": unused, "req_duration_count": unused, "req_duration_sum": unused},
			drop:   true,
		},
		{
			name:   "histogram used by _bucket in the database",
			metric: buildHistogramMetric("req_duration", 1),
			db:     map[string]string{"req_duration_bucket": used, "req_duration_count": unused, "req_duration_sum": unused},
		},
		{
			name:   "histogram used by _count in the database",
			metric: buildHistogramMetric("req_duration", 1),
			db:     map[string]string{"req_duration_bucket": unused, "req_duration_count": used, "req_duration_sum": unused},
		},
		{
			name:   "histogram with _sum unknown",
			metric: buildHistogramMetric("req_duration", 1),
			db:     map[string]string{"req_duration_bucket": unused, "req_duration_count": unused, "req_duration_sum": unknown},
		},
		{
			name:   "histogram unused in the cache",
			metric: buildHistogramMetric("req_duration", 1),
			cache:  map[string]string{"req_duration_bucket": unused, "req_duration_count": unused, "req_duration_sum": unused},
			drop:   true,
		},
		{
			name:   "histogram used by _bucket in the cache",
			metric: buildHistogramMetric("req_duration", 1),
			cache:  map[string]string{"req_duration_bucket": used, "req_duration_count": unused, "req_duration_sum": unused},
		},
		{
			name:   "histogram unused across the cache and the database",
			metric: buildHistogramMetric("req_duration", 1),
			cache:  map[string]string{"req_duration_bucket": unused},
			db:     map[string]string{"req_duration_count": unused, "req_duration_sum": unused},
			drop:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []models.MetricMetadata
			for series, state := range tc.db {
				if state != unknown {
					rows = append(rows, models.MetricMetadata{Name: series, IsUnused: state == unused})
				}
			}
			mp := &mockUsageProvider{}
			mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(rows, nil).Maybe()
			ing, err := NewOtlpIngester(&config.Config{}, mp)
			require.NoError(t, err)
			cache := newTestCache()
			for series, state := range tc.cache {
				cache.states[series] = otlppkg.StateUsed
				if state == unused {
					cache.states[series] = otlppkg.StateUnused
				}
			}
			ing.SetMetricCache(cache)
			exp := &captureExporter{}
			ing.SetExporter(exp)

			req := buildExportRequest(tc.metric, buildGaugeMetric("unrelated", 1))
			_, err = ing.Export(context.Background(), req)
			require.NoError(t, err)

			forwarded := forwardedNames(exp)
			if tc.drop {
				assert.Equal(t, []string{"unrelated"}, forwarded)
			} else {
				assert.Equal(t, []string{tc.metric.GetName(), "unrelated"}, forwarded)
			}
		})
	}
}

// TestExport_KeepsMetricNamedLikeAHistogramOrSummarySeries verifies a metric
// whose name is a series of a histogram or summary in the same request is
// judged with that histogram or summary, not on its own: it is kept while the
// histogram or summary is used, even when its own name is confirmed unused.
func TestExport_KeepsMetricNamedLikeAHistogramOrSummarySeries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		metric *metricspb.Metric
		rows   []models.MetricMetadata
		gauge  string
	}{
		{
			name:   "histogram",
			metric: buildHistogramMetric("req_duration", 1),
			rows: []models.MetricMetadata{
				{Name: "req_duration_bucket"},
				{Name: "req_duration_count", IsUnused: true},
				{Name: "req_duration_sum", IsUnused: true},
			},
			gauge: "req_duration_sum",
		},
		{
			name:   "histogram _count",
			metric: buildHistogramMetric("req_duration", 1),
			rows: []models.MetricMetadata{
				{Name: "req_duration_bucket"},
				{Name: "req_duration_count", IsUnused: true},
				{Name: "req_duration_sum", IsUnused: true},
			},
			gauge: "req_duration_count",
		},
		{
			name:   "histogram _bucket",
			metric: buildHistogramMetric("req_duration", 1),
			rows: []models.MetricMetadata{
				{Name: "req_duration_bucket", IsUnused: true},
				{Name: "req_duration_count"},
				{Name: "req_duration_sum", IsUnused: true},
			},
			gauge: "req_duration_bucket",
		},
		{
			name:   "summary",
			metric: buildSummaryMetric("rpc_latency", 1),
			rows: []models.MetricMetadata{
				{Name: "rpc_latency"},
				{Name: "rpc_latency_count", IsUnused: true},
				{Name: "rpc_latency_sum", IsUnused: true},
			},
			gauge: "rpc_latency_sum",
		},
		{
			name:   "summary _count",
			metric: buildSummaryMetric("rpc_latency", 1),
			rows: []models.MetricMetadata{
				{Name: "rpc_latency"},
				{Name: "rpc_latency_count", IsUnused: true},
				{Name: "rpc_latency_sum", IsUnused: true},
			},
			gauge: "rpc_latency_count",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &mockUsageProvider{}
			mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(tc.rows, nil)
			ing, err := NewOtlpIngester(&config.Config{}, mp)
			require.NoError(t, err)
			exp := &captureExporter{}
			ing.SetExporter(exp)

			_, err = ing.Export(context.Background(), buildExportRequest(tc.metric, buildGaugeMetric(tc.gauge, 1)))
			require.NoError(t, err)

			forwarded := forwardedNames(exp)
			assert.Equal(t, []string{tc.metric.GetName(), tc.gauge}, forwarded)
		})
	}
}

// testCache is a simple in-memory cache for testing
type testCache struct {
	states map[string]otlppkg.MetricUsageState
}

func newTestCache() *testCache {
	return &testCache{
		states: make(map[string]otlppkg.MetricUsageState),
	}
}

func (c *testCache) GetStates(ctx context.Context, names []string) (map[string]otlppkg.MetricUsageState, error) {
	result := make(map[string]otlppkg.MetricUsageState, len(names))
	for _, name := range names {
		if state, ok := c.states[name]; ok {
			result[name] = state
		} else {
			result[name] = otlppkg.StateUnknown
		}
	}
	return result, nil
}

func (c *testCache) SetStates(ctx context.Context, states map[string]otlppkg.MetricUsageState) error {
	for name, state := range states {
		c.states[name] = state
	}
	return nil
}

func (c *testCache) Close() error {
	return nil
}

// errorCache is a cache that always returns errors (for testing error handling)
type errorCache struct{}

func (c *errorCache) GetStates(ctx context.Context, names []string) (map[string]otlppkg.MetricUsageState, error) {
	return nil, assert.AnError
}

func (c *errorCache) SetStates(ctx context.Context, states map[string]otlppkg.MetricUsageState) error {
	return assert.AnError
}

func (c *errorCache) Close() error {
	return nil
}

func buildHistogramMetric(name string, n int) *metricspb.Metric {
	dps := make([]*metricspb.HistogramDataPoint, 0, n)
	for i := 0; i < n; i++ {
		dps = append(dps, &metricspb.HistogramDataPoint{Attributes: []*commonpb.KeyValue{}})
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{DataPoints: dps}},
	}
}

func buildSummaryMetric(name string, n int) *metricspb.Metric {
	dps := make([]*metricspb.SummaryDataPoint, 0, n)
	for i := 0; i < n; i++ {
		dps = append(dps, &metricspb.SummaryDataPoint{Attributes: []*commonpb.KeyValue{}})
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: dps}},
	}
}

func TestExport_DBError_FailOpen(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	exp := &captureExporter{}
	ing.SetExporter(exp)

	req := buildExportRequest(
		buildGaugeMetric("unused_metric", 1),
	)

	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, assert.AnError).Once()
	lookupErrors := metricValue(t, "ingester_processor_lookup_errors_total", map[string]string{})

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, lookupErrors+1, metricValue(t, "ingester_processor_lookup_errors_total", map[string]string{}))

	// No drops due to fail-open
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics[0].Metrics, 1)

	// Regression guard for https://github.com/nicolastakashi/prom-analytics-proxy/issues/569:
	// a DB lookup error must not make Export() bail out before ever reaching the
	// downstream exporter. "Fail open" has to mean the batch is actually forwarded,
	// not just that the (never-reached) filtering step didn't mutate it in place.
	assert.NotNil(t, exp.captured, "batch should have been forwarded to the downstream exporter despite the DB lookup error")
	if exp.captured != nil {
		assert.Len(t, exp.captured.ResourceMetrics, 1)
		assert.Len(t, exp.captured.ResourceMetrics[0].ScopeMetrics[0].Metrics, 1)
	}

	mp.AssertExpectations(t)
}

// TestExport_DBError_PartialChunkFailure_StillDropsResolvedUnused verifies that when
// usage lookups are split across multiple DB-lookup chunks, a DB error on one chunk
// only fails open for that chunk's names -- metrics whose usage was already resolved
// by a different, successful chunk must still be dropped normally.
func TestExport_DBError_PartialChunkFailure_StillDropsResolvedUnused(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			OTLP: config.OtlpIngesterConfig{
				LookupChunkSize: 1, // force one name per DB lookup chunk
			},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	exp := &captureExporter{}
	ing.SetExporter(exp)

	req := buildExportRequest(
		buildGaugeMetric("resolved_unused_metric", 1),
		buildGaugeMetric("unresolved_metric", 1),
	)

	// One chunk resolves cleanly and confirms the metric is unused. IsUnused
	// is the field metricMetadataUnused actually checks (see its doc comment
	// on models.MetricMetadata) - zero counts alone no longer mean unused,
	// since they're also what an unevaluated metric looks like.
	mp.On("GetSeriesMetadataByNames", mock.Anything, []string{"resolved_unused_metric"}, "").Return([]models.MetricMetadata{
		{Name: "resolved_unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Maybe()
	// The other chunk's DB lookup fails.
	mp.On("GetSeriesMetadataByNames", mock.Anything, []string{"unresolved_metric"}, "").Return(nil, assert.AnError).Maybe()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	assert.NotNil(t, exp.captured, "batch should still reach the downstream exporter")
	if exp.captured != nil {
		got := exp.captured.ResourceMetrics[0].ScopeMetrics[0].Metrics
		names := make([]string, 0, len(got))
		for _, m := range got {
			names = append(names, m.GetName())
		}
		// resolved_unused_metric was confirmed unused by a successful chunk and must be
		// dropped; unresolved_metric's chunk failed and must fail open (kept).
		assert.ElementsMatch(t, []string{"unresolved_metric"}, names)
	}
}

// TestExport_UnevaluatedMetric_NotConfirmedUnused_IsKept guards the fix for
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/570 and
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/571: a metric
// with all-zero usage counts is not necessarily unused - it may simply be a
// freshly-catalogued metric that has not yet had a chance to be evaluated by
// RefreshMetricsUsageSummary (see UpsertMetricsCatalog's placeholder insert,
// which creates exactly this all-zero-counts shape for every newly-seen
// metric). Only IsUnused=true, sourced from metrics_usage_summary.is_unused,
// may be trusted as "confirmed unused" - zero counts alone must not cause a
// drop.
func TestExport_UnevaluatedMetric_NotConfirmedUnused_IsKept(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	req := buildExportRequest(
		buildGaugeMetric("brand_new_metric", 1),
	)

	// Zero counts, but IsUnused is explicitly false: this is what a
	// freshly-catalogued, not-yet-evaluated metric looks like. It must not be
	// treated as confirmed unused merely because its counts happen to be zero.
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "brand_new_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: false},
	}, nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// Must be kept, not dropped, despite all-zero counts.
	rms := req.ResourceMetrics
	if assert.Len(t, rms, 1) && assert.Len(t, rms[0].ScopeMetrics[0].Metrics, 1) {
		assert.Equal(t, "brand_new_metric", rms[0].ScopeMetrics[0].Metrics[0].GetName())
	}

	mp.AssertExpectations(t)
}

func TestExport_WithCacheEnabled_AllMisses_HitsDB(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			Redis: config.RedisCacheConfig{
				Enabled: false, // We'll inject cache manually
			},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	cache := newTestCache()
	ing.SetMetricCache(cache)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
	)

	// First call should hit DB (cache miss)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Once()
	misses := metricValue(t, "ingester_metric_cache_misses_total", map[string]string{})
	lookups := metricValue(t, "ingester_processor_lookup_duration_seconds", map[string]string{})

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, misses+2, metricValue(t, "ingester_metric_cache_misses_total", map[string]string{}))
	assert.Equal(t, lookups+1, metricValue(t, "ingester_processor_lookup_duration_seconds", map[string]string{}))

	// Verify cache was populated
	states, err := cache.GetStates(context.Background(), []string{"used_metric", "unused_metric"})
	assert.NoError(t, err)
	assert.Equal(t, otlppkg.StateUsed, states["used_metric"])
	assert.Equal(t, otlppkg.StateUnused, states["unused_metric"])

	mp.AssertExpectations(t)
}

func TestExport_WithCacheEnabled_UsedHit_SkipsDB(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			Redis: config.RedisCacheConfig{
				Enabled: false,
			},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	cache := newTestCache()
	// Pre-populate cache with used state
	err = cache.SetStates(context.Background(), map[string]otlppkg.MetricUsageState{
		"used_metric": otlppkg.StateUsed,
	})
	assert.NoError(t, err)
	ing.SetMetricCache(cache)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
	)

	// Should only query DB for unused_metric (cache miss)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.MatchedBy(func(names []string) bool {
		// Should only contain unused_metric
		return len(names) == 1 && names[0] == "unused_metric"
	}), "").Return([]models.MetricMetadata{
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Once()
	usedHits := metricValue(t, "ingester_metric_cache_hits_total", map[string]string{"state": "used"})

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, usedHits+1, metricValue(t, "ingester_metric_cache_hits_total", map[string]string{"state": "used"}))

	// used_metric should remain (not dropped because it's used)
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	got := rms[0].ScopeMetrics[0].Metrics
	assert.Len(t, got, 1)
	assert.Equal(t, "used_metric", got[0].GetName())

	mp.AssertExpectations(t)
}

func TestExport_WithCacheEnabled_UnusedHit_SkipsDB(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			Redis: config.RedisCacheConfig{
				Enabled: false,
			},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	cache := newTestCache()
	// Pre-populate cache with unused state
	err = cache.SetStates(context.Background(), map[string]otlppkg.MetricUsageState{
		"unused_metric": otlppkg.StateUnused,
	})
	assert.NoError(t, err)
	ing.SetMetricCache(cache)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
	)

	// Should only query DB for used_metric (cache miss)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.MatchedBy(func(names []string) bool {
		return len(names) == 1 && names[0] == "used_metric"
	}), "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
	}, nil).Once()
	unusedHits := metricValue(t, "ingester_metric_cache_hits_total", map[string]string{"state": "unused"})

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, unusedHits+1, metricValue(t, "ingester_metric_cache_hits_total", map[string]string{"state": "unused"}))

	// unused_metric should be dropped (from cache)
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	got := rms[0].ScopeMetrics[0].Metrics
	assert.Len(t, got, 1)
	assert.Equal(t, "used_metric", got[0].GetName())

	mp.AssertExpectations(t)
}

func TestExport_WithCacheError_FallsBackToDB(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			Redis: config.RedisCacheConfig{
				Enabled: false,
			},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	// Create a cache that returns errors
	errorCache := &errorCache{}
	ing.SetMetricCache(errorCache)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
	)

	// Should fall back to DB when cache errors
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// Should still work correctly despite cache error
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	got := rms[0].ScopeMetrics[0].Metrics
	assert.Len(t, got, 1)
	assert.Equal(t, "used_metric", got[0].GetName())

	mp.AssertExpectations(t)
}

// TestExport_CacheTimeout verifies a cache read and a cache write-back each
// run under the cache operation timeout, capped at a tenth of the time the
// incoming request has left (the cap is at least 5ms), whatever the
// downstream timeout.
func TestExport_CacheTimeout(t *testing.T) {
	for _, tc := range []struct {
		name             string
		operationTimeout time.Duration
		incoming         time.Duration // 0: no incoming deadline
		want             time.Duration
	}{
		{name: "no incoming deadline", want: 200 * time.Millisecond},
		{name: "configured operation timeout", operationTimeout: time.Second, want: time.Second},
		{name: "incoming deadline", incoming: time.Second, want: 100 * time.Millisecond},
		{name: "long incoming deadline", incoming: 5 * time.Second, want: 200 * time.Millisecond},
		{name: "nearly expired", incoming: 40 * time.Millisecond, want: 5 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mp := &mockUsageProvider{}
				mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").
					Return([]models.MetricMetadata{{Name: "some_metric"}}, nil)
				cfg := &config.Config{Ingester: config.IngesterConfig{
					OTLP:  config.OtlpIngesterConfig{DownstreamTimeout: time.Second},
					Redis: config.RedisCacheConfig{OperationTimeout: tc.operationTimeout},
				}}
				ing, err := NewOtlpIngester(cfg, mp)
				require.NoError(t, err)
				cache := &deadlineCache{}
				ing.SetMetricCache(cache)
				ing.SetExporter(&captureExporter{})

				ctx := context.Background()
				if tc.incoming > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.incoming)
					defer cancel()
				}
				start := time.Now()
				_, err = ing.Export(ctx, buildExportRequest(buildGaugeMetric("some_metric", 1)))
				require.NoError(t, err)
				require.Len(t, cache.reads, 1)
				assert.Equal(t, tc.want, cache.reads[0].Sub(start))
				require.Len(t, cache.writes, 1)
				assert.Equal(t, tc.want, cache.writes[0].Sub(start))
			})
		})
	}
}

// deadlineCache records the deadline of the context each cache read and
// write runs under, and answers every name as unknown.
type deadlineCache struct {
	reads  []time.Time
	writes []time.Time
}

func (c *deadlineCache) GetStates(ctx context.Context, names []string) (map[string]otlppkg.MetricUsageState, error) {
	deadline, _ := ctx.Deadline()
	c.reads = append(c.reads, deadline)
	states := make(map[string]otlppkg.MetricUsageState, len(names))
	for _, name := range names {
		states[name] = otlppkg.StateUnknown
	}
	return states, nil
}
func (c *deadlineCache) SetStates(ctx context.Context, _ map[string]otlppkg.MetricUsageState) error {
	deadline, _ := ctx.Deadline()
	c.writes = append(c.writes, deadline)
	return nil
}
func (c *deadlineCache) Close() error { return nil }

// forwardedNames returns the names of the metrics exp forwarded, in order.
func forwardedNames(exp *captureExporter) []string {
	var names []string
	for _, m := range exp.captured.ResourceMetrics[0].ScopeMetrics[0].Metrics {
		names = append(names, m.GetName())
	}
	return names
}

// TestExport_CacheAnswersEveryName_SkipsDB verifies a chunk the cache
// answers in full makes no database call.
func TestExport_CacheAnswersEveryName_SkipsDB(t *testing.T) {
	mp := &mockUsageProvider{}
	ing, err := NewOtlpIngester(&config.Config{}, mp)
	require.NoError(t, err)
	cache := newTestCache()
	cache.states["used_metric"] = otlppkg.StateUsed
	cache.states["unused_metric"] = otlppkg.StateUnused
	ing.SetMetricCache(cache)
	ing.SetExporter(&captureExporter{})

	_, err = ing.Export(context.Background(), buildExportRequest(
		buildGaugeMetric("used_metric", 1),
		buildGaugeMetric("unused_metric", 1),
	))
	require.NoError(t, err)

	mp.AssertNotCalled(t, "GetSeriesMetadataByNames", mock.Anything, mock.Anything, mock.Anything)
}

// TestExport_CountsCacheFailuresByOperation verifies a failed cache read and
// a failed cache write-back are each counted under their own operation.
func TestExport_CountsCacheFailuresByOperation(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").
		Return([]models.MetricMetadata{{Name: "some_metric"}}, nil)
	ing, err := NewOtlpIngester(&config.Config{}, mp)
	require.NoError(t, err)
	ing.SetMetricCache(&errorCache{})
	ing.SetExporter(&captureExporter{})

	get := metricValue(t, "ingester_metric_cache_errors_total", map[string]string{"operation": "get"})
	set := metricValue(t, "ingester_metric_cache_errors_total", map[string]string{"operation": "set"})
	_, err = ing.Export(context.Background(), buildExportRequest(buildGaugeMetric("some_metric", 1)))
	require.NoError(t, err)

	assert.Equal(t, get+1, metricValue(t, "ingester_metric_cache_errors_total", map[string]string{"operation": "get"}))
	assert.Equal(t, set+1, metricValue(t, "ingester_metric_cache_errors_total", map[string]string{"operation": "set"}))
}
