package ingester

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
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

// TestExport_DBError_PartialChunkFailure_StillDropsResolvedUnused verifies
// that a DB error on one chunk fails open only for that chunk's names: a
// metric an earlier, successful chunk confirmed unused is still dropped.
func TestExport_DBError_PartialChunkFailure_StillDropsResolvedUnused(t *testing.T) {
	store := &failAfterStore{ok: 1}
	cfg := &config.Config{Ingester: config.IngesterConfig{
		OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
	}}
	ing, err := NewOtlpIngester(cfg, store)
	require.NoError(t, err)
	exp := &captureExporter{}
	ing.SetExporter(exp)

	_, err = ing.Export(context.Background(), buildExportRequest(
		buildGaugeMetric("first", 1),
		buildGaugeMetric("second", 1),
	))
	require.NoError(t, err)

	assert.Equal(t, 2, store.calls)
	assert.Len(t, forwardedNames(exp), 1, "the failed chunk's metric is kept, the resolved one dropped")
}

// failAfterStore confirms every name of its first ok lookups unused and
// fails every later one. It counts lookups.
type failAfterStore struct {
	ok    int
	calls int
}

func (s *failAfterStore) GetSeriesMetadataByNames(_ context.Context, names []string, _ string) ([]models.MetricMetadata, error) {
	s.calls++
	if s.calls > s.ok {
		return nil, assert.AnError
	}
	metas := make([]models.MetricMetadata, 0, len(names))
	for _, name := range names {
		metas = append(metas, models.MetricMetadata{Name: name, IsUnused: true})
	}
	return metas, nil
}

func (s *failAfterStore) UpsertMetricsCatalog(context.Context, []db.MetricCatalogItem) error {
	return nil
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
	lookups := metricValue(t, "ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "ok"})

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, misses+2, metricValue(t, "ingester_metric_cache_misses_total", map[string]string{}))
	assert.Equal(t, lookups+1, metricValue(t, "ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "ok"}))

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
	ing.SetMetricCache(&scriptedCache{err: assert.AnError, setErr: assert.AnError})

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
// usage lookup has left (the cap is at least 5ms), whatever the downstream
// timeout.
func TestExport_CacheTimeout(t *testing.T) {
	for _, tc := range []struct {
		name             string
		operationTimeout time.Duration
		lookupTimeout    time.Duration
		incoming         time.Duration // 0: no incoming deadline
		want             time.Duration
	}{
		{name: "no incoming deadline", want: 200 * time.Millisecond},
		{name: "configured operation timeout", operationTimeout: 220 * time.Millisecond, want: 220 * time.Millisecond},
		{name: "operation timeout beyond a tenth of the lookup", operationTimeout: time.Second, want: 250 * time.Millisecond},
		{name: "incoming deadline", incoming: 2 * time.Second, want: 100 * time.Millisecond},
		{name: "nearly expired", lookupTimeout: 40 * time.Millisecond, want: 5 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mp := &mockUsageProvider{}
				mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").
					Return([]models.MetricMetadata{{Name: "some_metric"}}, nil)
				cfg := &config.Config{Ingester: config.IngesterConfig{
					OTLP:  config.OtlpIngesterConfig{DownstreamTimeout: time.Second, LookupTimeout: tc.lookupTimeout},
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
// write runs under, answers every name as unknown, and records whether it
// was closed.
type deadlineCache struct {
	reads  []time.Time
	writes []time.Time
	closed bool
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
func (c *deadlineCache) Close() error {
	c.closed = true
	return nil
}

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

// TestExport_NoWriteBackWhenDatabaseReturnsNothing verifies nothing is
// written back to the cache when the database has no record of any name.
func TestExport_NoWriteBackWhenDatabaseReturnsNothing(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{}, nil)
	ing, err := NewOtlpIngester(&config.Config{}, mp)
	require.NoError(t, err)
	cache := &deadlineCache{}
	ing.SetMetricCache(cache)
	ing.SetExporter(&captureExporter{})

	_, err = ing.Export(context.Background(), buildExportRequest(buildGaugeMetric("some_metric", 1)))
	require.NoError(t, err)

	assert.Len(t, cache.reads, 1)
	assert.Empty(t, cache.writes)
}

// TestExport_SetMetricCacheNil_DisablesCache verifies setting a nil cache
// stops the lookup from using the cache set before it.
func TestExport_SetMetricCacheNil_DisablesCache(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{{Name: "some_metric"}}, nil)
	ing, err := NewOtlpIngester(&config.Config{}, mp)
	require.NoError(t, err)
	cache := &deadlineCache{}
	ing.SetMetricCache(cache)
	ing.SetMetricCache(nil)
	ing.SetExporter(&captureExporter{})

	_, err = ing.Export(context.Background(), buildExportRequest(buildGaugeMetric("some_metric", 1)))
	require.NoError(t, err)

	assert.Empty(t, cache.reads)
	assert.Empty(t, cache.writes)
}

// scriptedCache answers reads with states and err, as a cache read that
// failed part way does, and fails writes with setErr.
type scriptedCache struct {
	states map[string]otlppkg.MetricUsageState
	err    error
	setErr error
}

func (c *scriptedCache) GetStates(context.Context, []string) (map[string]otlppkg.MetricUsageState, error) {
	return c.states, c.err
}

func (c *scriptedCache) SetStates(context.Context, map[string]otlppkg.MetricUsageState) error {
	return c.setErr
}

func (c *scriptedCache) Close() error { return nil }

// TestExport_CountsUsageResolutionsBySource verifies each looked-up name is
// counted once by the source that resolved it: the cache, whether it holds
// the name as used or unused, or the database, including names it has no
// record of.
func TestExport_CountsUsageResolutionsBySource(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").
		Return([]models.MetricMetadata{{Name: "db_known"}}, nil)
	ing, err := NewOtlpIngester(&config.Config{}, mp)
	require.NoError(t, err)
	cache := newTestCache()
	cache.states["cached_used"] = otlppkg.StateUsed
	cache.states["cached_unused"] = otlppkg.StateUnused
	ing.SetMetricCache(cache)
	ing.SetExporter(&captureExporter{})

	got := seriesDeltas(t, []series{
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "cache"}},
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "database"}},
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
		{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "ok"}},
		{"ingester_metric_cache_hits_total", map[string]string{"state": "used"}},
		{"ingester_metric_cache_hits_total", map[string]string{"state": "unused"}},
		{"ingester_metric_cache_misses_total", map[string]string{}},
	}, func() {
		_, err := ing.Export(context.Background(), buildExportRequest(
			buildGaugeMetric("cached_used", 1),
			buildGaugeMetric("cached_unused", 1),
			buildGaugeMetric("db_known", 1),
			buildGaugeMetric("db_unknown", 1),
		))
		require.NoError(t, err)
	})
	assert.Equal(t, []float64{2, 2, 0, 1, 1, 1, 2}, got)
}

// TestExport_DBLookupFailure_CountsUnresolved verifies a failed database
// lookup counts each of its names as unresolved, by whether it timed out or
// failed, and counts nothing when the request was canceled, whatever the
// error.
func TestExport_DBLookupFailure_CountsUnresolved(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		ctx    func() (context.Context, context.CancelFunc)
		reason string // "": nothing counted
	}{
		{name: "error", err: assert.AnError, ctx: liveContext, reason: "error"},
		{name: "deadline", err: context.DeadlineExceeded, ctx: liveContext, reason: "timeout"},
		{name: "statement timeout", err: db.QueryError(&pq.Error{Code: "57014"}, "executing query", ""), ctx: liveContext, reason: "timeout"},
		{name: "expired request", err: assert.AnError, ctx: expiredContext, reason: "timeout"},
		{name: "canceled request", err: assert.AnError, ctx: canceledContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &mockUsageProvider{}
			mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, tc.err)
			ing, err := NewOtlpIngester(&config.Config{}, mp)
			require.NoError(t, err)
			ing.SetExporter(&captureExporter{})
			ctx, cancel := tc.ctx()
			defer cancel()

			got := seriesDeltas(t, []series{
				{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
				{"ingester_processor_unresolved_total", map[string]string{"reason": "db_timeout"}},
				{"ingester_processor_unresolved_total", map[string]string{"reason": "db_error"}},
				{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "timeout"}},
				{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "error"}},
				{"ingester_processor_lookup_errors_total", map[string]string{}},
			}, func() {
				_, _ = ing.Export(ctx, buildExportRequest(buildGaugeMetric("first", 1), buildGaugeMetric("second", 1)))
			})

			want := []float64{0, 0, 0, 0, 0, 0}
			switch tc.reason {
			case "timeout":
				want = []float64{2, 2, 0, 1, 0, 1}
			case "error":
				want = []float64{2, 0, 2, 0, 1, 1}
			}
			assert.Equal(t, want, got)
		})
	}
}

// TestExport_DatabaseUnavailable_ForwardsUnfilteredUntilConnected verifies
// that before the database gate connects every metric is forwarded and its
// name counted unresolved as db_unavailable, with no lookup observed or
// counted as failed, unless the request was canceled; once connected, unused
// metrics are dropped.
func TestExport_DatabaseUnavailable_ForwardsUnfilteredUntilConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mp := &mockUsageProvider{}
		release := make(chan struct{})
		gate := otlppkg.NewDatabaseGate(func(ctx context.Context) (db.Provider, error) {
			select {
			case <-release:
				return mp, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- gate.Run(ctx) }()
		defer func() {
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
		}()
		ing, err := NewOtlpIngester(&config.Config{}, gate)
		require.NoError(t, err)
		exp := &captureExporter{}
		ing.SetExporter(exp)
		export := func(ctx context.Context) {
			_, err := ing.Export(ctx, buildExportRequest(buildGaugeMetric("used_metric", 1), buildGaugeMetric("unused_metric", 1)))
			require.NoError(t, err)
		}
		unavailable := []series{
			{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
			{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "timeout"}},
			{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "error"}},
			{"ingester_processor_lookup_errors_total", map[string]string{}},
		}

		got := seriesDeltas(t, unavailable, func() { export(t.Context()) })
		assert.Equal(t, []float64{2, 2, 0, 0, 0}, got)
		assert.Equal(t, []string{"used_metric", "unused_metric"}, forwardedNames(exp))

		canceled, cancelRequest := canceledContext()
		defer cancelRequest()
		got = seriesDeltas(t, unavailable, func() { export(canceled) })
		assert.Equal(t, []float64{0, 0, 0, 0, 0}, got)

		mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
			{Name: "unused_metric", IsUnused: true},
		}, nil)
		close(release)
		synctest.Wait()

		export(t.Context())
		assert.Equal(t, []string{"used_metric"}, forwardedNames(exp))
	})
}

// TestExport_LookupTimeout verifies a request's usage lookup runs under the
// configured lookup timeout, 2.5s when 0 or negative, shortened to leave 1s
// of the incoming request's deadline for forwarding.
func TestExport_LookupTimeout(t *testing.T) {
	for _, tc := range []struct {
		name          string
		lookupTimeout time.Duration
		incoming      time.Duration // 0: no incoming deadline
		want          time.Duration
	}{
		{name: "default", want: 2500 * time.Millisecond},
		{name: "configured", lookupTimeout: time.Second, want: time.Second},
		{name: "negative uses default", lookupTimeout: -time.Second, want: 2500 * time.Millisecond},
		{name: "short incoming deadline", incoming: 3 * time.Second, want: 2 * time.Second},
		{name: "shorter incoming deadline", incoming: 1500 * time.Millisecond, want: 500 * time.Millisecond},
		{name: "long incoming deadline", incoming: 10 * time.Second, want: 2500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &slowStore{}
				cfg := &config.Config{Ingester: config.IngesterConfig{
					OTLP: config.OtlpIngesterConfig{LookupTimeout: tc.lookupTimeout},
				}}
				ing, err := NewOtlpIngester(cfg, store)
				require.NoError(t, err)
				ing.SetExporter(&captureExporter{})

				ctx := t.Context()
				if tc.incoming > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.incoming)
					defer cancel()
				}
				start := time.Now()
				_, err = ing.Export(ctx, buildExportRequest(buildGaugeMetric("some_metric", 1)))
				require.NoError(t, err)
				require.Len(t, store.deadlines, 1)
				assert.Equal(t, tc.want, store.deadlines[0].Sub(start))
			})
		})
	}
}

// TestExport_LookupTimeout_RequestWithinForwardReserve verifies that when the
// incoming request has no more than 1s left, the lookup's time limit has
// expired before it starts: the cache is not read, the first chunk's
// database call fails at once as a timeout, the rest skip the database, and
// every name is forwarded.
func TestExport_LookupTimeout_RequestWithinForwardReserve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &slowStore{}
		cfg := &config.Config{Ingester: config.IngesterConfig{
			OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
		}}
		ing, err := NewOtlpIngester(cfg, store)
		require.NoError(t, err)
		cache := &deadlineCache{}
		ing.SetMetricCache(cache)
		exp := &captureExporter{}
		ing.SetExporter(exp)
		ctx, cancel := context.WithTimeout(t.Context(), 800*time.Millisecond)
		defer cancel()

		start := time.Now()
		got := seriesDeltas(t, []series{
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_timeout"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
			{"ingester_processor_lookup_errors_total", map[string]string{}},
			{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": "timeout"}},
		}, func() {
			_, err := ing.Export(ctx, buildExportRequest(buildGaugeMetric("a", 1), buildGaugeMetric("b", 1)))
			require.NoError(t, err)
		})

		assert.Equal(t, []float64{1, 1, 1, 0}, got)
		assert.Empty(t, cache.reads)
		assert.Equal(t, []time.Time{start.Add(-200 * time.Millisecond)}, store.deadlines)
		assert.Equal(t, []string{"a", "b"}, forwardedNames(exp))
	})
}

// TestExport_LookupTimeoutSpansEveryChunk verifies the lookup timeout bounds
// a request's whole lookup, not each chunk, and no chunk after it expires is
// sent to the database.
func TestExport_LookupTimeoutSpansEveryChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &slowStore{delay: time.Second}
		cfg := &config.Config{Ingester: config.IngesterConfig{
			OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
		}}
		ing, err := NewOtlpIngester(cfg, store)
		require.NoError(t, err)
		ing.SetExporter(&captureExporter{})

		start := time.Now()
		got := seriesDeltas(t, []series{
			{"ingester_processor_usage_resolutions_total", map[string]string{"source": "database"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_timeout"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
		}, func() {
			_, err := ing.Export(t.Context(), buildExportRequest(
				buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1), buildGaugeMetric("d", 1),
			))
			require.NoError(t, err)
		})

		assert.Equal(t, []float64{2, 1, 1}, got)
		want := start.Add(2500 * time.Millisecond)
		assert.Equal(t, []time.Time{want, want, want}, store.deadlines)
	})
}

// TestExport_DatabaseHangs_ForwardsEveryNameAfterOneCall verifies that once
// the database outlasts the lookup timeout, the remaining chunks call neither
// the cache nor the database: their names are forwarded and counted
// unresolved as db_unavailable, each chunk logged as skipped.
// Not parallel: swaps the default logger.
func TestExport_DatabaseHangs_ForwardsEveryNameAfterOneCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := captureLogs(t)
		store := &slowStore{}
		cfg := &config.Config{Ingester: config.IngesterConfig{
			OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
		}}
		ing, err := NewOtlpIngester(cfg, store)
		require.NoError(t, err)
		cache := &deadlineCache{}
		ing.SetMetricCache(cache)
		exp := &captureExporter{}
		ing.SetExporter(exp)

		got := seriesDeltas(t, []series{
			{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_timeout"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_error"}},
			{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
			{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "timeout"}},
			{"ingester_processor_lookup_duration_seconds", map[string]string{"outcome": "error"}},
			{"ingester_processor_lookup_errors_total", map[string]string{}},
			{"ingester_metric_cache_misses_total", map[string]string{}},
			{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": "timeout"}},
			{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": "error"}},
		}, func() {
			_, err := ing.Export(t.Context(), buildExportRequest(
				buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1),
			))
			require.NoError(t, err)
		})

		assert.Equal(t, []float64{3, 1, 0, 2, 1, 0, 1, 1, 0, 0}, got)
		assert.Len(t, store.deadlines, 1)
		assert.Len(t, cache.reads, 1)
		assert.Equal(t, []string{"a", "b", "c"}, forwardedNames(exp))
		assert.Equal(t, 2, strings.Count(logs.String(), `"msg":"ingester.lookup.database_skipped","names_count":1}`))
	})
}

// TestExport_DatabaseError_LaterChunksSkipDatabaseNotCache verifies that
// after a database call fails, the remaining chunks still read the cache,
// whose unused names are dropped, but send their misses to the database no
// more: they are forwarded and counted unresolved as db_unavailable.
func TestExport_DatabaseError_LaterChunksSkipDatabaseNotCache(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, assert.AnError).Once()
	cfg := &config.Config{Ingester: config.IngesterConfig{
		OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
	}}
	ing, err := NewOtlpIngester(cfg, mp)
	require.NoError(t, err)
	cache := &sequenceCache{states: []otlppkg.MetricUsageState{otlppkg.StateUnknown, otlppkg.StateUnknown, otlppkg.StateUnused}}
	ing.SetMetricCache(cache)
	exp := &captureExporter{}
	ing.SetExporter(exp)

	got := seriesDeltas(t, []series{
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "cache"}},
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_error"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
		{"ingester_processor_lookup_errors_total", map[string]string{}},
	}, func() {
		_, err := ing.Export(context.Background(), buildExportRequest(
			buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1),
		))
		require.NoError(t, err)
	})

	assert.Equal(t, []float64{1, 2, 1, 1, 1}, got)
	assert.Equal(t, 3, cache.reads)
	assert.Len(t, forwardedNames(exp), 2)
	mp.AssertNumberOfCalls(t, "GetSeriesMetadataByNames", 1)
}

// TestExport_CacheTimeout_LaterChunksStillReadCache verifies a cache read
// that times out does not stop the remaining chunks from reading the cache.
func TestExport_CacheTimeout_LaterChunksStillReadCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mp := &mockUsageProvider{}
		mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{}, nil)
		cfg := &config.Config{Ingester: config.IngesterConfig{
			OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
		}}
		ing, err := NewOtlpIngester(cfg, mp)
		require.NoError(t, err)
		cache := &hangingCache{}
		ing.SetMetricCache(cache)
		ing.SetExporter(&captureExporter{})

		_, err = ing.Export(t.Context(), buildExportRequest(
			buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1),
		))
		require.NoError(t, err)

		assert.Equal(t, 3, cache.reads)
		mp.AssertNumberOfCalls(t, "GetSeriesMetadataByNames", 3)
	})
}

// TestExport_CanceledRequest_SkippedChunksNotCounted verifies a canceled
// request's lookup reads the cache for every chunk, calls the database once
// and counts none of its names unresolved.
func TestExport_CanceledRequest_SkippedChunksNotCounted(t *testing.T) {
	mp := &mockUsageProvider{}
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, assert.AnError)
	cfg := &config.Config{Ingester: config.IngesterConfig{
		OTLP: config.OtlpIngesterConfig{LookupChunkSize: 1},
	}}
	ing, err := NewOtlpIngester(cfg, mp)
	require.NoError(t, err)
	cache := &sequenceCache{}
	ing.SetMetricCache(cache)
	ing.SetExporter(&captureExporter{})
	ctx, cancel := canceledContext()
	defer cancel()

	got := seriesDeltas(t, []series{
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_timeout"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_error"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
	}, func() {
		_, _ = ing.Export(ctx, buildExportRequest(
			buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1),
		))
	})

	assert.Equal(t, []float64{0, 0, 0, 0}, got)
	assert.Equal(t, 3, cache.reads)
	mp.AssertNumberOfCalls(t, "GetSeriesMetadataByNames", 1)
}

// TestExport_DatabaseUnavailable_LaterChunksSkipDatabase verifies that once
// the database gate refuses a lookup, the remaining chunks do not call the
// database even if it would now answer: every name is forwarded and counted
// unresolved as db_unavailable.
func TestExport_DatabaseUnavailable_LaterChunksSkipDatabase(t *testing.T) {
	gate := otlppkg.NewDatabaseGate(func(context.Context) (db.Provider, error) {
		return nil, assert.AnError // never run: the gate stays unconnected
	})
	answering := &failAfterStore{ok: 3}
	store := &switchingStore{first: gate, rest: answering}
	cfg := &config.Config{Ingester: config.IngesterConfig{
		OTLP: config.OtlpIngesterConfig{LookupChunkSize: 2},
	}}
	ing, err := NewOtlpIngester(cfg, store)
	require.NoError(t, err)
	exp := &captureExporter{}
	ing.SetExporter(exp)

	got := seriesDeltas(t, []series{
		{"ingester_processor_usage_resolutions_total", map[string]string{"source": "unresolved"}},
		{"ingester_processor_unresolved_total", map[string]string{"reason": "db_unavailable"}},
	}, func() {
		_, err := ing.Export(context.Background(), buildExportRequest(
			buildGaugeMetric("a", 1), buildGaugeMetric("b", 1), buildGaugeMetric("c", 1), buildGaugeMetric("d", 1),
		))
		require.NoError(t, err)
	})

	assert.Equal(t, []float64{4, 4}, got)
	assert.Equal(t, 1, store.calls)
	assert.Equal(t, 0, answering.calls)
	assert.Equal(t, []string{"a", "b", "c", "d"}, forwardedNames(exp))
}

// switchingStore sends its first usage lookup to first and every later one
// to rest, counting lookups.
type switchingStore struct {
	first, rest otlppkg.Store
	calls       int
}

func (s *switchingStore) GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error) {
	s.calls++
	if s.calls == 1 {
		return s.first.GetSeriesMetadataByNames(ctx, names, job)
	}
	return s.rest.GetSeriesMetadataByNames(ctx, names, job)
}

func (s *switchingStore) UpsertMetricsCatalog(context.Context, []db.MetricCatalogItem) error {
	return nil
}

// slowStore answers each usage lookup after delay with no record of any
// name, or fails with its context's error once that is done first. It
// records the deadline of each lookup's context.
type slowStore struct {
	delay     time.Duration // 0: until the context is done
	deadlines []time.Time
}

func (s *slowStore) GetSeriesMetadataByNames(ctx context.Context, _ []string, _ string) ([]models.MetricMetadata, error) {
	deadline, _ := ctx.Deadline()
	s.deadlines = append(s.deadlines, deadline)
	var answered <-chan time.Time // nil: never
	if s.delay > 0 {
		answered = time.After(s.delay)
	}
	select {
	case <-answered:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *slowStore) UpsertMetricsCatalog(context.Context, []db.MetricCatalogItem) error { return nil }

// sequenceCache answers its nth read with states[n] for every name, and
// unknown once states run out. It counts reads and ignores writes.
type sequenceCache struct {
	states []otlppkg.MetricUsageState
	reads  int
}

func (c *sequenceCache) GetStates(_ context.Context, names []string) (map[string]otlppkg.MetricUsageState, error) {
	state := otlppkg.StateUnknown
	if c.reads < len(c.states) {
		state = c.states[c.reads]
	}
	c.reads++
	out := make(map[string]otlppkg.MetricUsageState, len(names))
	for _, name := range names {
		out[name] = state
	}
	return out, nil
}

func (c *sequenceCache) SetStates(context.Context, map[string]otlppkg.MetricUsageState) error {
	return nil
}

func (c *sequenceCache) Close() error { return nil }

// hangingCache fails each read with its context's error once that is done,
// counting reads. It ignores writes.
type hangingCache struct{ reads int }

func (c *hangingCache) GetStates(ctx context.Context, _ []string) (map[string]otlppkg.MetricUsageState, error) {
	c.reads++
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *hangingCache) SetStates(context.Context, map[string]otlppkg.MetricUsageState) error {
	return nil
}

func (c *hangingCache) Close() error { return nil }

// captureLogs sends the default logger's records, at debug level and up, as
// JSON without time to the returned buffer until t ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestExport_CacheReadFailure verifies a cache read that fails part way
// keeps the names it answered, sends only the rest to the database without
// counting them as misses, and counts the failed call once by whether it
// timed out, unless the request was canceled, whatever the error.
func TestExport_CacheReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		ctx    func() (context.Context, context.CancelFunc)
		reason string // "": not counted
	}{
		{name: "timeout", err: context.DeadlineExceeded, ctx: liveContext, reason: "timeout"},
		{name: "error", err: assert.AnError, ctx: liveContext, reason: "error"},
		{name: "canceled request", err: assert.AnError, ctx: canceledContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &mockUsageProvider{}
			mp.On("GetSeriesMetadataByNames", mock.Anything, []string{"unread"}, "").Return(nil, nil).Once()
			ing, err := NewOtlpIngester(&config.Config{}, mp)
			require.NoError(t, err)
			ing.SetMetricCache(&scriptedCache{
				states: map[string]otlppkg.MetricUsageState{"answered": otlppkg.StateUsed},
				err:    tc.err,
			})
			ing.SetExporter(&captureExporter{})
			ctx, cancel := tc.ctx()
			defer cancel()

			got := seriesDeltas(t, []series{
				{"ingester_metric_cache_hits_total", map[string]string{"state": "used"}},
				{"ingester_metric_cache_misses_total", map[string]string{}},
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": "timeout"}},
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": "error"}},
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "get", "reason": ""}},
				{"ingester_metric_cache_lookup_seconds", map[string]string{}},
				{"ingester_processor_usage_resolutions_total", map[string]string{"source": "cache"}},
				{"ingester_processor_usage_resolutions_total", map[string]string{"source": "database"}},
			}, func() {
				_, _ = ing.Export(ctx, buildExportRequest(buildGaugeMetric("answered", 1), buildGaugeMetric("unread", 1)))
			})

			want := []float64{1, 0, 0, 0, 0, 1, 1, 1}
			switch tc.reason {
			case "timeout":
				want[2] = 1
			case "error":
				want[3] = 1
			}
			assert.Equal(t, want, got)
			mp.AssertExpectations(t)
		})
	}
}

// TestExport_CacheWriteFailure verifies a failed cache write-back is counted
// once per call, by whether it timed out, however many names it carried, and
// not at all when the request was canceled.
func TestExport_CacheWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		ctx  func() (context.Context, context.CancelFunc)
		want []float64 // timeout, error, none
	}{
		{name: "timeout", err: context.DeadlineExceeded, ctx: liveContext, want: []float64{1, 0, 0}},
		{name: "error", err: assert.AnError, ctx: liveContext, want: []float64{0, 1, 0}},
		{name: "canceled request", err: assert.AnError, ctx: canceledContext, want: []float64{0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &mockUsageProvider{}
			mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").
				Return([]models.MetricMetadata{{Name: "first"}, {Name: "second"}}, nil)
			ing, err := NewOtlpIngester(&config.Config{}, mp)
			require.NoError(t, err)
			ing.SetMetricCache(&scriptedCache{states: map[string]otlppkg.MetricUsageState{}, setErr: tc.err})
			ing.SetExporter(&captureExporter{})
			ctx, cancel := tc.ctx()
			defer cancel()

			got := seriesDeltas(t, []series{
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "set", "reason": "timeout"}},
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "set", "reason": "error"}},
				{"ingester_metric_cache_errors_total", map[string]string{"operation": "set", "reason": ""}},
			}, func() {
				_, _ = ing.Export(ctx, buildExportRequest(buildGaugeMetric("first", 1), buildGaugeMetric("second", 1)))
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

// series names one metric series by its name and exact labels.
type series struct {
	name   string
	labels map[string]string
}

// seriesDeltas returns how much each of ss changed while fn ran, in order.
func seriesDeltas(t *testing.T, ss []series, fn func()) []float64 {
	t.Helper()
	before := make([]float64, len(ss))
	for i, s := range ss {
		before[i] = metricValue(t, s.name, s.labels)
	}
	fn()
	got := make([]float64, len(ss))
	for i, s := range ss {
		got[i] = metricValue(t, s.name, s.labels) - before[i]
	}
	return got
}

func liveContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func canceledContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

func expiredContext() (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
}
