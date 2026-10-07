package db

import (
	"context"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHistogramSummaryMetricsCatalog verifies histogram and summary
// component series are stored with their types and filtered by type.
func testHistogramSummaryMetricsCatalog(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	histogramItems := []MetricCatalogItem{
		{Name: "access_evaluation_duration_bucket", Type: "histogram_bucket", Help: "Access evaluation duration (histogram buckets)", Unit: "seconds"},
		{Name: "access_evaluation_duration_count", Type: "histogram_count", Help: "Access evaluation duration (histogram count)", Unit: ""},
		{Name: "access_evaluation_duration_sum", Type: "histogram_sum", Help: "Access evaluation duration (histogram sum)", Unit: "seconds"},
	}

	summaryItems := []MetricCatalogItem{
		{Name: "request_latency", Type: "summary", Help: "Request latency", Unit: "seconds"},
		{Name: "request_latency_count", Type: "summary_count", Help: "Request latency (summary count)", Unit: ""},
		{Name: "request_latency_sum", Type: "summary_sum", Help: "Request latency (summary sum)", Unit: "seconds"},
	}

	mustUpsertCatalog(t, p, append(histogramItems, summaryItems...))

	typesOf := func(items []MetricCatalogItem) map[string]string {
		out := map[string]string{}
		for _, m := range items {
			out[m.Name] = m.Type
		}
		return out
	}
	listed := func(typ string) map[string]string {
		res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: typ})
		require.NoError(t, err, "GetSeriesMetadata %s", typ)
		rows := rowsOf[models.MetricMetadata](t, res)
		assert.Equal(t, len(rows), res.Total, "%s: Total", typ)
		out := map[string]string{}
		for _, m := range rows {
			out[m.Name] = m.Type
		}
		return out
	}
	assert.Equal(t, typesOf(append(histogramItems, summaryItems...)), listed("all"))
	assert.Equal(t, typesOf(histogramItems), listed("histogram"))
	assert.Equal(t, typesOf(summaryItems), listed("summary"))
}

func TestPostgreSQL_HistogramSummaryMetricsCatalog(t *testing.T) {
	t.Parallel()
	testHistogramSummaryMetricsCatalog(t, postgreSQLTestBackend)
}

func TestSQLite_HistogramSummaryMetricsCatalog(t *testing.T) {
	t.Parallel()
	testHistogramSummaryMetricsCatalog(t, sqliteTestBackend)
}

func testMetricsJobIndexAndListJobs(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustUpsertJobIndex(t, p, []MetricJobIndexItem{
		{Name: "up", Job: "prometheus"},
		{Name: "up", Job: "node"},
		{Name: "process_cpu_seconds_total", Job: "node"},
	})

	jobs, err := p.ListJobs(context.Background())
	assert.NoError(t, err, "ListJobs")
	assert.ElementsMatch(t, []string{"node", "prometheus"}, jobs)
}

func TestPostgreSQL_MetricsJobIndex_And_ListJobs(t *testing.T) {
	t.Parallel()
	testMetricsJobIndexAndListJobs(t, postgreSQLTestBackend)
}

func TestSQLite_MetricsJobIndex_And_ListJobs(t *testing.T) {
	t.Parallel()
	testMetricsJobIndexAndListJobs(t, sqliteTestBackend)
}

// testUpsertMetricsCatalogCreatesDefaultUnusedSummaryRow verifies every
// catalog row has a zero-count metrics_usage_summary row before any refresh,
// and that this placeholder is not is_unused: never evaluated is distinct from
// confirmed unused. See
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/570.
func testUpsertMetricsCatalogCreatesDefaultUnusedSummaryRow(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "metric_a", Type: "gauge", Help: "a"},
		{Name: "metric_b", Type: "counter", Help: "b"},
	})

	for _, name := range []string{"metric_a", "metric_b"} {
		assert.Equal(t, summaryRow{}, mustSummaryRow(t, b, p, name), name)
	}
}

func TestPostgreSQL_UpsertMetricsCatalog_CreatesDefaultUnusedSummaryRow(t *testing.T) {
	t.Parallel()
	testUpsertMetricsCatalogCreatesDefaultUnusedSummaryRow(t, postgreSQLTestBackend)
}

func TestSQLite_UpsertMetricsCatalog_CreatesDefaultUnusedSummaryRow(t *testing.T) {
	t.Parallel()
	testUpsertMetricsCatalogCreatesDefaultUnusedSummaryRow(t, sqliteTestBackend)
}

func testRefreshMetricsUsageSummaryAndGetSeriesMetadata(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustUpsertCatalog(t, p, []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric"}})
	mustUpsertJobIndex(t, p, []MetricJobIndexItem{{Name: "up", Job: "prometheus"}})

	now := time.Now().UTC()
	mustInsertQueries(t, p, []Query{instantQuery(now.Add(-5*time.Minute), "up")})
	mustInsertRules(t, p, []RulesUsage{{
		Serie:      "up",
		GroupName:  "default",
		Name:       "up_alert",
		Expression: "up == 0",
		Kind:       string(RuleUsageKindAlert),
		Labels:     []string{"severity"},
		CreatedAt:  now.Add(-10 * time.Minute),
	}})
	mustInsertDashboards(t, p, []DashboardUsage{{
		Id:        "dash1",
		Serie:     "up",
		Name:      "Up Overview",
		URL:       "http://example/d/dash1",
		CreatedAt: now.Add(-15 * time.Minute),
	}})

	require.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), presenceWindow(now.Add(-1*time.Hour))))
	assert.Equal(t, summaryRow{Alert: 1, Dashboard: 1, Query: 1}, mustSummaryRow(t, b, p, "up"))

	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "name",
		SortOrder: "asc",
		Filter:    "up",
		Type:      "all",
		Job:       "prometheus",
	})
	require.NoError(t, err, "GetSeriesMetadata")
	assert.Equal(t, 1, res.Total)
	data := rowsOf[models.MetricMetadata](t, res)
	require.Len(t, data, 1)
	assert.Equal(t, [4]int{1, 0, 1, 1}, [4]int{data[0].AlertCount, data[0].RecordCount, data[0].DashboardCount, data[0].QueryCount},
		"alert, record, dashboard, query counts")
	lastQueried, err := time.Parse(time.RFC3339, data[0].LastQueriedAt)
	require.NoError(t, err, "LastQueriedAt %q", data[0].LastQueriedAt)
	assert.WithinDuration(t, now.Add(-5*time.Minute), lastQueried, time.Second)
}

func TestPostgreSQL_RefreshMetricsUsageSummary_And_GetSeriesMetadata(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryAndGetSeriesMetadata(t, postgreSQLTestBackend)
}

func TestSQLite_RefreshMetricsUsageSummary_And_GetSeriesMetadata(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryAndGetSeriesMetadata(t, sqliteTestBackend)
}

// testRefreshMetricsUsageSummaryExcludesStaleCatalogRows verifies a refresh
// skips catalog rows whose last_synced_at is outside the window, even when
// usage evidence falls inside it: they keep their previous summary. See
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/579.
func testRefreshMetricsUsageSummaryExcludesStaleCatalogRows(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "fresh_metric", Type: "gauge", Help: "still scraped"},
		{Name: "stale_metric", Type: "gauge", Help: "no longer scraped"},
	})

	b.markCatalogStale(t, p, "stale_metric")

	now := time.Now().UTC()
	// Usage evidence for BOTH metrics, inside the refresh window below - if
	// the catalog-freshness filter didn't exist, both would come out used.
	mustInsertQueries(t, p, []Query{
		instantQuery(now.Add(-time.Minute), "fresh_metric"),
		instantQuery(now.Add(-time.Minute), "stale_metric"),
	})

	require.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-time.Hour), To: now}))

	fresh := mustSummaryRow(t, b, p, "fresh_metric")
	assert.Greater(t, fresh.Query, 0, "fresh_metric should have been recomputed with real usage")
	assert.False(t, fresh.Unused, "fresh_metric should be marked used")

	stale := mustSummaryRow(t, b, p, "stale_metric")
	assert.Equal(t, summaryRow{Alert: 0, Record: 0, Dashboard: 0, Query: 0, Unused: false}, stale,
		"stale_metric's summary should remain untouched at its placeholder values - got %+v", stale)
}

func TestPostgreSQL_RefreshMetricsUsageSummary_ExcludesStaleCatalogRows(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryExcludesStaleCatalogRows(t, postgreSQLTestBackend)
}

func TestSQLite_RefreshMetricsUsageSummary_ExcludesStaleCatalogRows(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryExcludesStaleCatalogRows(t, sqliteTestBackend)
}

// testRefreshMetricsUsageSummaryExcludesOutOfWindowRulesUsage verifies a rule
// whose presence window falls outside the refresh's TimeRange does not count
// toward alert_count/record_count.
func testRefreshMetricsUsageSummaryExcludesOutOfWindowRulesUsage(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "in_window_metric", Type: "gauge", Help: "actively alerting"},
		{Name: "out_of_window_metric", Type: "gauge", Help: "alert retired long ago"},
	})
	mustInsertRules(t, p, []RulesUsage{
		{Serie: "in_window_metric", GroupName: "g", Name: "a1", Expression: "e", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}},
		{Serie: "out_of_window_metric", GroupName: "g", Name: "a2", Expression: "e", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}},
	})

	b.retireUsage(t, p, "RulesUsage", "serie", "out_of_window_metric")

	require.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), presenceWindow(time.Now().UTC().AddDate(0, 0, -30))))

	inWindow := mustSummaryRow(t, b, p, "in_window_metric")
	assert.Equal(t, summaryRow{Alert: 1, Record: 0, Dashboard: 0, Query: 0, Unused: false}, inWindow,
		"in_window_metric's alert rule falls inside the refresh window and must be counted - got %+v", inWindow)

	outOfWindow := mustSummaryRow(t, b, p, "out_of_window_metric")
	assert.Equal(t, summaryRow{Alert: 0, Record: 0, Dashboard: 0, Query: 0, Unused: true}, outOfWindow,
		"out_of_window_metric's rule presence ended before the refresh window started, so it must not be counted as used - got %+v", outOfWindow)
}

func TestPostgreSQL_RefreshMetricsUsageSummary_ExcludesOutOfWindowRulesUsage(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryExcludesOutOfWindowRulesUsage(t, postgreSQLTestBackend)
}

func TestSQLite_RefreshMetricsUsageSummary_ExcludesOutOfWindowRulesUsage(t *testing.T) {
	t.Parallel()
	testRefreshMetricsUsageSummaryExcludesOutOfWindowRulesUsage(t, sqliteTestBackend)
}

func testGetSeriesMetadataUsageFilters(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC()
	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "used_metric", Type: "gauge", Help: "used metric"},
		{Name: "unused_metric", Type: "gauge", Help: "unused metric"},
	})
	mustInsertQueries(t, p, []Query{instantQuery(now.Add(-5*time.Minute), "used_metric")})

	assert.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-1 * time.Hour), To: now}))
	// Catalogued after the only refresh run: never evaluated, so not unused.
	mustUpsertCatalog(t, p, []MetricCatalogItem{{Name: "never_evaluated_metric", Type: "gauge", Help: "never evaluated metric"}})

	tests := []struct {
		name      string
		usage     string
		wantNames []string
	}{
		{name: "all metrics", usage: SeriesMetadataUsageAll, wantNames: []string{"never_evaluated_metric", "unused_metric", "used_metric"}},
		{name: "used metrics", usage: SeriesMetadataUsageUsed, wantNames: []string{"used_metric"}},
		{name: "unused metrics", usage: SeriesMetadataUsageUnused, wantNames: []string{"unused_metric"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{
				Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all", Usage: tt.usage,
			})
			require.NoError(t, err, "GetSeriesMetadata")
			names := keysOf(rowsOf[models.MetricMetadata](t, res), func(m models.MetricMetadata) string { return m.Name })
			assert.ElementsMatch(t, tt.wantNames, names)
		})
	}
}

func TestPostgreSQL_GetSeriesMetadata_UsageFilters(t *testing.T) {
	t.Parallel()
	testGetSeriesMetadataUsageFilters(t, postgreSQLTestBackend)
}

func TestSQLite_GetSeriesMetadata_UsageFilters(t *testing.T) {
	t.Parallel()
	testGetSeriesMetadataUsageFilters(t, sqliteTestBackend)
}

// testGetSeriesMetadataByNamesPopulatesIsUnused verifies IsUnused reflects
// metrics_usage_summary.is_unused rather than the four usage counts, which
// cannot distinguish confirmed zero usage from never evaluated.
func testGetSeriesMetadataByNamesPopulatesIsUnused(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC()
	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "evaluated_unused_metric", Type: "gauge", Help: "evaluated, confirmed unused"},
	})
	// No usage anywhere: the refresh confirms it unused.
	assert.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-1 * time.Hour), To: now}))

	// Catalogued after the only refresh run: never evaluated.
	mustUpsertCatalog(t, p, []MetricCatalogItem{
		{Name: "fresh_metric", Type: "gauge", Help: "never evaluated"},
	})

	results, err := p.GetSeriesMetadataByNames(context.Background(), []string{"evaluated_unused_metric", "fresh_metric"}, "")
	assert.NoError(t, err, "GetSeriesMetadataByNames")

	byName := make(map[string]models.MetricMetadata, len(results))
	for _, mm := range results {
		byName[mm.Name] = mm
	}

	if assert.Contains(t, byName, "evaluated_unused_metric") {
		assert.True(t, byName["evaluated_unused_metric"].IsUnused, "a metric confirmed unused by RefreshMetricsUsageSummary must report IsUnused=true")
	}
	if assert.Contains(t, byName, "fresh_metric") {
		assert.False(t, byName["fresh_metric"].IsUnused, "a never-evaluated metric must not report IsUnused=true merely because its counts are zero")
	}
}

func TestPostgreSQL_GetSeriesMetadataByNames_PopulatesIsUnused(t *testing.T) {
	t.Parallel()
	testGetSeriesMetadataByNamesPopulatesIsUnused(t, postgreSQLTestBackend)
}

func TestSQLite_GetSeriesMetadataByNames_PopulatesIsUnused(t *testing.T) {
	t.Parallel()
	testGetSeriesMetadataByNamesPopulatesIsUnused(t, sqliteTestBackend)
}
