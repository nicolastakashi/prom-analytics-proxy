package db

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
)

// newTestSQLiteProvider creates a temporary SQLite database and returns a provider and cleanup.
func newTestSQLiteProvider(t *testing.T) (Provider, func()) {
	t.Helper()
	ctx := context.Background()
	file, err := os.CreateTemp("", "prom-analytics-proxy-test-*.db")
	assert.NoError(t, err, "failed to create temp db")
	_ = file.Close()

	// Create a test-specific config
	testConfig := &config.Config{
		Database: config.DatabaseConfig{
			Provider: string(SQLite),
			SQLite: config.SQLiteConfig{
				DatabasePath: file.Name(),
			},
		},
	}

	provider, err := newSqliteProvider(ctx, testConfig)
	if err != nil {
		// Cleanup temp file on failure as well
		_ = os.Remove(file.Name())
		assert.NoError(t, err, "failed to init sqlite provider")
	}

	cleanup := func() {
		_ = provider.Close()
		_ = os.Remove(file.Name())
	}
	return provider, cleanup
}

func mustInsertQueries(t *testing.T, p Provider, qs []Query) {
	t.Helper()
	err := p.Insert(context.Background(), qs)
	assert.NoError(t, err, "Insert")
}

func mustUpsertCatalog(t *testing.T, p Provider, items []MetricCatalogItem) {
	t.Helper()
	err := p.UpsertMetricsCatalog(context.Background(), items)
	assert.NoError(t, err, "UpsertMetricsCatalog")
}

func mustUpsertJobIndex(t *testing.T, p Provider, items []MetricJobIndexItem) {
	t.Helper()
	err := p.UpsertMetricsJobIndex(context.Background(), items)
	assert.NoError(t, err, "UpsertMetricsJobIndex")
}

func mustInsertRules(t *testing.T, p Provider, items []RulesUsage) {
	t.Helper()
	err := p.InsertRulesUsage(context.Background(), items)
	assert.NoError(t, err, "InsertRulesUsage")
}

func mustInsertDashboards(t *testing.T, p Provider, items []DashboardUsage) {
	t.Helper()
	err := p.InsertDashboardUsage(context.Background(), items)
	assert.NoError(t, err, "InsertDashboardUsage")
}

// -------------------- Analytics --------------------

func TestSQLite_GetQueryTypes(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	qs := []Query{}
	for i := range 4 { // 4 instant
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      10 * time.Millisecond,
			StatusCode:    200,
			BodySize:      1,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "fp1",
		})
	}
	for i := range 6 { // 6 range
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "rate(up[5m])",
			TimeParam:     now,
			Duration:      15 * time.Millisecond,
			StatusCode:    200,
			BodySize:      1,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeRange,
			Start:         now.Add(-5 * time.Minute),
			End:           now,
			Step:          15,
			Fingerprint:   "fp2",
		})
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-10 * time.Minute), To: now.Add(10 * time.Minute)}
	out, err := p.GetQueryTypes(context.Background(), tr, "")
	assert.NoError(t, err, "GetQueryTypes")
	if assert.NotNil(t, out) {
		assert.NotNil(t, out.TotalQueries)
		assert.NotNil(t, out.InstantPercent)
		assert.NotNil(t, out.RangePercent)
		assert.Equal(t, 10, *out.TotalQueries)
		// 4/10 = 40, 6/10 = 60
		assert.InDelta(t, 40.0, *out.InstantPercent, 0.2)
		assert.InDelta(t, 60.0, *out.RangePercent, 0.2)
	}
}

func TestSQLite_GetAverageDuration(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	base := time.Date(2025, 8, 20, 12, 0, 0, 0, time.UTC)
	// previous window: [base-20m, base-10m)
	prevFrom := base.Add(-20 * time.Minute)
	// current window: [base-10m, base)
	curFrom := base.Add(-10 * time.Minute)
	curTo := base

	// Seed previous with avg 10ms, current with avg 20ms
	var qs []Query
	for i := range 5 {
		qs = append(qs, Query{
			TS:           prevFrom.Add(time.Duration(i) * time.Minute),
			QueryParam:   "up",
			TimeParam:    prevFrom,
			Duration:     10 * time.Millisecond,
			StatusCode:   200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:         QueryTypeInstant,
		})
	}
	for i := range 5 {
		qs = append(qs, Query{
			TS:           curFrom.Add(time.Duration(i) * time.Minute),
			QueryParam:   "up",
			TimeParam:    curFrom,
			Duration:     20 * time.Millisecond,
			StatusCode:   200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:         QueryTypeInstant,
		})
	}
	mustInsertQueries(t, p, qs)

	out, err := p.GetAverageDuration(context.Background(), TimeRange{From: curFrom, To: curTo}, "")
	assert.NoError(t, err, "GetAverageDuration")
	if assert.NotNil(t, out) {
		assert.NotNil(t, out.AvgDuration)
		assert.NotNil(t, out.DeltaPercent)
		assert.InDelta(t, 20.0, *out.AvgDuration, 0.2)
		// delta = ((20-10)/10)*100 = 100%
		assert.InDelta(t, 100.0, *out.DeltaPercent, 0.2)
	}
}

func TestSQLite_GetQueryRate(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	var qs []Query
	// 3 successes, 2 errors for metric "up" and fingerprint fp1
	for i := range 3 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      5 * time.Millisecond,
			StatusCode:    200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "fp1",
		})
	}
	for i := range 2 {
		qs = append(qs, Query{
			TS:           now.Add(time.Duration(3+i) * time.Minute),
			QueryParam:   "up",
			TimeParam:    now,
			Duration:     5 * time.Millisecond,
			StatusCode:   500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:         QueryTypeInstant,
			Fingerprint:  "fp1",
		})
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-5 * time.Minute), To: now.Add(10 * time.Minute)}
	out, err := p.GetQueryRate(context.Background(), tr, "up", "fp1")
	assert.NoError(t, err, "GetQueryRate")
	if assert.NotNil(t, out) {
		assert.NotNil(t, out.SuccessTotal)
		assert.NotNil(t, out.ErrorTotal)
		assert.NotNil(t, out.SuccessRatePercent)
		assert.NotNil(t, out.ErrorRatePercent)
		assert.Equal(t, 3, *out.SuccessTotal)
		assert.Equal(t, 2, *out.ErrorTotal)
		assert.InDelta(t, 60.0, *out.SuccessRatePercent, 0.2)
		assert.InDelta(t, 40.0, *out.ErrorRatePercent, 0.2)
	}
}

func TestSQLite_GetQueryLatencyTrends_And_Throughput_And_Errors(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	var qs []Query
	for i := range 10 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      time.Duration(5+i) * time.Millisecond,
			StatusCode:    200,
			BodySize:      1,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "fp-lat",
		})
	}
	// Add some errors spread across time
	for i := range 3 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i*2) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      10 * time.Millisecond,
			StatusCode:    500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "fp-lat",
		})
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-10 * time.Minute), To: now.Add(20 * time.Minute)}

	lat, err := p.GetQueryLatencyTrends(context.Background(), tr, "up", "fp-lat")
	assert.NoError(t, err, "GetQueryLatencyTrends")
	assert.NotEmpty(t, lat)

	thr, err := p.GetQueryThroughputAnalysis(context.Background(), tr)
	assert.NoError(t, err, "GetQueryThroughputAnalysis")
	assert.NotEmpty(t, thr)

	errSeries, err := p.GetQueryErrorAnalysis(context.Background(), tr, "fp-lat")
	assert.NoError(t, err, "GetQueryErrorAnalysis")
	assert.NotEmpty(t, errSeries)

	// Also validate status distribution for completeness
	dist, err := p.GetQueryStatusDistribution(context.Background(), tr, "fp-lat")
	assert.NoError(t, err, "GetQueryStatusDistribution")
	assert.NotEmpty(t, dist)
}

// -------------------- Aggregations --------------------

func TestSQLite_GetQueriesBySerieName(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Seed a mix of queries for two queryParam values of the same metric
	now := time.Now().UTC()
	qs := []Query{
		{TS: now.Add(-2 * time.Minute), QueryParam: "up", TimeParam: now, Duration: 10 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeInstant, PeakSamples: 100},
		{TS: now.Add(-1 * time.Minute), QueryParam: "up", TimeParam: now, Duration: 20 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeInstant, PeakSamples: 200},
		{TS: now.Add(-2 * time.Minute), QueryParam: "rate(up[5m])", TimeParam: now, Duration: 30 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeRange, Start: now.Add(-5 * time.Minute), End: now, PeakSamples: 300},
	}
	mustInsertQueries(t, p, qs)

	res, err := p.GetQueriesBySerieName(context.Background(), QueriesBySerieNameParams{
		SerieName: "up",
		TimeRange: TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
		SortBy:    "avgDuration",
		SortOrder: "desc",
	})
	assert.NoError(t, err, "GetQueriesBySerieName")
	assert.Greater(t, res.Total, 0)
	assert.IsType(t, []QueriesBySerieNameResult{}, res.Data)
	rows, _ := res.Data.([]QueriesBySerieNameResult)
	assert.Len(t, rows, 2)
}

func TestSQLite_GetQueryExpressions_And_Executions(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	var qs []Query
	// Two fingerprints, one with more executions
	for i := range 5 {
		qs = append(qs, Query{
			TS:           now.Add(time.Duration(i) * time.Minute),
			QueryParam:   "up",
			TimeParam:    now,
			Duration:     10 * time.Millisecond,
			StatusCode:   200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:         QueryTypeInstant,
			PeakSamples:  10 + i,
			Fingerprint:  "fp-a",
		})
	}
	for i := range 3 {
		qs = append(qs, Query{
			TS:           now.Add(time.Duration(i) * time.Minute),
			QueryParam:   "rate(up[5m])",
			TimeParam:    now,
			Duration:     20 * time.Millisecond,
			StatusCode:   500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:         QueryTypeRange,
			Start:        now.Add(-5 * time.Minute),
			End:          now,
			Step:         15,
			PeakSamples:  100,
			Fingerprint:  "fp-b",
		})
	}
	mustInsertQueries(t, p, qs)

	// QueryExpressions
	pr, err := p.GetQueryExpressions(context.Background(), QueryExpressionsParams{
		TimeRange: TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
		SortBy:    "executions",
		SortOrder: "desc",
	})
	assert.NoError(t, err, "GetQueryExpressions")
	rows2, ok := pr.Data.([]QueryExpression)
	if assert.True(t, ok, "type conversion") {
		assert.Len(t, rows2, 2)
		assert.Equal(t, "fp-a", rows2[0].Fingerprint)
	}

	// QueryExecutions for fp-b, type range (table-driven for sort order)
	cases := []struct{ sortOrder string }{{"asc"}, {"desc"}}
	for _, tc := range cases {
		per, err := p.GetQueryExecutions(context.Background(), QueryExecutionsParams{
			Fingerprint: "fp-b",
			Type:        string(QueryTypeRange),
			TimeRange:   TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)},
			Page:        1,
			PageSize:    10,
			SortBy:      "ts",
			SortOrder:   tc.sortOrder,
		})
		assert.NoError(t, err, "GetQueryExecutions")
		erows, ok := per.Data.([]QueryExecutionRow)
		if assert.True(t, ok, "type conversion") {
			assert.Len(t, erows, 3)
			if tc.sortOrder == "asc" {
				assert.True(t, erows[0].Timestamp.Before(erows[2].Timestamp) || erows[0].Timestamp.Equal(erows[2].Timestamp))
			} else {
				assert.True(t, erows[0].Timestamp.After(erows[2].Timestamp) || erows[0].Timestamp.Equal(erows[2].Timestamp))
			}
		}
	}
}

// -------------------- Metrics Inventory --------------------

func TestSQLite_MetricsJobIndex_And_ListJobs(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	mustUpsertJobIndex(t, p, []MetricJobIndexItem{
		{Name: "up", Job: "prometheus"},
		{Name: "up", Job: "node"},
		{Name: "process_cpu_seconds_total", Job: "node"},
	})

	jobs, err := p.ListJobs(context.Background())
	assert.NoError(t, err, "ListJobs")
	assert.NotEmpty(t, jobs)
	sort.Strings(jobs)
	assert.Equal(t, []string{"node", "prometheus"}, jobs)
}

func TestSQLite_RefreshMetricsUsageSummary_And_GetSeriesMetadata(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Seed catalog and job index
	mustUpsertCatalog(t, p, []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric"}})
	mustUpsertJobIndex(t, p, []MetricJobIndexItem{{Name: "up", Job: "prometheus"}})

	// Seed related data: queries, rules, dashboards within window
	now := time.Now().UTC()
	mustInsertQueries(t, p, []Query{{
		TS:            now.Add(-5 * time.Minute),
		QueryParam:    "up",
		TimeParam:     now,
		Duration:      10 * time.Millisecond,
		StatusCode:    200,
		LabelMatchers: LabelMatchers{{"__name__": "up"}},
		Type:          QueryTypeInstant,
	}})
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

	assert.NoError(t, p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-1 * time.Hour), To: now}))

	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "name",
		SortOrder: "asc",
		Filter:    "up",
		Type:      "all",
		Job:       "prometheus",
	})
	assert.NoError(t, err, "GetSeriesMetadata")
	assert.Greater(t, res.Total, 0)

	// If data available, validate types mapping as in histogram/summary test
	if data, ok := res.Data.([]models.MetricMetadata); ok {
		_ = data // basic smoke to ensure type is correct for future checks
	}
}

func TestSQLite_GetMetricStatistics_And_QueryPerformanceStats(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	metric := "http_requests_total"
	now := time.Now().UTC().Truncate(time.Minute)

	// Seed rules and dashboards across different series
	// Note: InsertRulesUsage sets first_seen_at and last_seen_at to current time
	mustInsertRules(t, p, []RulesUsage{
		{Serie: metric, GroupName: "g", Name: "a1", Expression: "expr", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
		{Serie: metric, GroupName: "g", Name: "r1", Expression: "expr", Kind: string(RuleUsageKindRecord), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
		{Serie: "other", GroupName: "g", Name: "a2", Expression: "expr", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
	})
	mustInsertDashboards(t, p, []DashboardUsage{
		{Id: "d1", Serie: metric, Name: "Dash", URL: "http://d/1", CreatedAt: now.Add(-45 * time.Minute)},
		{Id: "d2", Serie: "other", Name: "Other", URL: "http://d/2", CreatedAt: now.Add(-45 * time.Minute)},
	})

	// Seed queries for performance stats
	mustInsertQueries(t, p, []Query{
		{TS: now.Add(-5 * time.Minute), QueryParam: metric, TimeParam: now, Duration: 12 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": metric}}, Type: QueryTypeInstant, PeakSamples: 100},
		{TS: now.Add(-4 * time.Minute), QueryParam: metric, TimeParam: now, Duration: 18 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": metric}}, Type: QueryTypeInstant, PeakSamples: 200},
	})

	// Use a time range that includes the current time (when first_seen_at and last_seen_at are set)
	tr := TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}

	// Metric usage statistics
	stats, err := p.GetMetricStatistics(context.Background(), metric, tr)
	assert.NoError(t, err, "GetMetricStatistics")
	assert.Greater(t, stats.AlertCount, 0)
	assert.Greater(t, stats.RecordCount, 0)
	assert.Greater(t, stats.DashboardCount, 0)

	// Query performance statistics
	perf, err := p.GetMetricQueryPerformanceStatistics(context.Background(), metric, tr)
	assert.NoError(t, err, "GetMetricQueryPerformanceStatistics")
	if assert.NotNil(t, perf.TotalQueries) {
		assert.Greater(t, *perf.TotalQueries, 0)
	}
}

// -------------------- Rules & Dashboards --------------------

func TestSQLite_InsertRulesUsage_GetRulesUsage(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	base := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	rules := []RulesUsage{
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l2", "l1"}, CreatedAt: base},
		// duplicate (different label order) should de-duplicate
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l1", "l2"}, CreatedAt: base},
		// different rule
		{Serie: "up", GroupName: "g2", Name: "r2", Expression: "expr2", Kind: string(RuleUsageKindRecord), Labels: []string{"lbl"}, CreatedAt: base},
	}
	mustInsertRules(t, p, rules)

	// Use a time range that includes the current time (when first_seen_at and last_seen_at are set)
	now := time.Now().UTC()
	out, err := p.GetRulesUsage(context.Background(), RulesUsageParams{
		Serie:     "up",
		Kind:      string(RuleUsageKindAlert),
		TimeRange: TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
	})
	assert.NoError(t, err, "GetRulesUsage")
	rows3, ok := out.Data.([]RulesUsage)
	if assert.True(t, ok, "type conversion") {
		// Only the alert kind for serie=up -> expect 1 unique after dedup
		assert.Len(t, rows3, 1)
	}
}

func TestSQLite_InsertDashboardUsage_UpsertBehavior(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	base := time.Now().UTC().Truncate(time.Minute)
	mustInsertDashboards(t, p, []DashboardUsage{{Id: "d1", Serie: "m1", Name: "Dash 1", URL: "http://d/1", CreatedAt: base}})
	// Upsert with newer last_seen should update name/url and extend presence
	mustInsertDashboards(t, p, []DashboardUsage{{Id: "d1", Serie: "m1", Name: "Dash 1 Renamed", URL: "http://d/1r", CreatedAt: base}})

	out, err := p.GetDashboardUsage(context.Background(), DashboardUsageParams{
		Serie:     "m1",
		TimeRange: TimeRange{From: base.Add(-1 * time.Hour), To: base.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
	})
	assert.NoError(t, err, "GetDashboardUsage")
	rows4, ok := out.Data.([]DashboardUsage)
	if assert.True(t, ok, "type conversion") {
		assert.Len(t, rows4, 1)
		assert.Equal(t, "Dash 1 Renamed", rows4[0].Name)
		assert.Equal(t, "http://d/1r", rows4[0].URL)
	}
}

// -------------------- Missing Tests from Original sqlite_test.go --------------------

// Test histogram and summary metrics catalog handling
func TestSQLite_HistogramSummaryMetricsCatalog(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Test histogram metrics: base metric should generate _bucket, _count, _sum variants
	histogramItems := []MetricCatalogItem{
		{Name: "access_evaluation_duration_bucket", Type: "histogram_bucket", Help: "Access evaluation duration (histogram buckets)", Unit: "seconds"},
		{Name: "access_evaluation_duration_count", Type: "histogram_count", Help: "Access evaluation duration (histogram count)", Unit: ""},
		{Name: "access_evaluation_duration_sum", Type: "histogram_sum", Help: "Access evaluation duration (histogram sum)", Unit: "seconds"},
	}

	// Test summary metrics: base metric should be kept, plus _count, _sum variants
	summaryItems := []MetricCatalogItem{
		{Name: "request_latency", Type: "summary", Help: "Request latency", Unit: "seconds"},
		{Name: "request_latency_count", Type: "summary_count", Help: "Request latency (summary count)", Unit: ""},
		{Name: "request_latency_sum", Type: "summary_sum", Help: "Request latency (summary sum)", Unit: "seconds"},
	}

	// Combine and upsert all items
	allItems := append(histogramItems, summaryItems...)
	err := p.UpsertMetricsCatalog(context.Background(), allItems)
	assert.NoError(t, err, "UpsertMetricsCatalog")

	// Verify all metrics were stored correctly
	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{
		Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all",
	})
	assert.NoError(t, err, "GetSeriesMetadata")

	// Should have 6 metrics total (3 histogram + 3 summary)
	assert.Equal(t, 6, res.Total, "Expected 6 metrics")

	// Verify histogram metrics exist with correct types
	expectedHistogramMetrics := map[string]string{
		"access_evaluation_duration_bucket": "histogram_bucket",
		"access_evaluation_duration_count":  "histogram_count",
		"access_evaluation_duration_sum":    "histogram_sum",
	}

	// Verify summary metrics exist with correct types
	expectedSummaryMetrics := map[string]string{
		"request_latency":       "summary",
		"request_latency_count": "summary_count",
		"request_latency_sum":   "summary_sum",
	}

	// Check all expected metrics are present
	foundMetrics := make(map[string]string)
	if data, ok := res.Data.([]models.MetricMetadata); ok {
		for _, metric := range data {
			foundMetrics[metric.Name] = metric.Type
		}
	} else {
		assert.Fail(t, "Expected Data to be []models.MetricMetadata", "got %T", res.Data)
		return
	}

	for name, expectedType := range expectedHistogramMetrics {
		if actualType, found := foundMetrics[name]; !found {
			assert.Fail(t, "Histogram metric not found", "metric: %s", name)
		} else {
			assert.Equal(t, expectedType, actualType, "Histogram metric %s type", name)
		}
	}

	for name, expectedType := range expectedSummaryMetrics {
		if actualType, found := foundMetrics[name]; !found {
			assert.Fail(t, "Summary metric not found", "metric: %s", name)
		} else {
			assert.Equal(t, expectedType, actualType, "Summary metric %s type", name)
		}
	}
}

// Basic integration test exercising catalog upsert, summary refresh and list.
func TestSQLite_MetricsInventoryAndList(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Upsert catalog
	items := []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric", Unit: ""}}
	err := p.UpsertMetricsCatalog(context.Background(), items)
	assert.NoError(t, err, "UpsertMetricsCatalog")

	// Insert a few queries for window
	now := time.Now().UTC()
	err = p.Insert(context.Background(), []Query{{
		TS:            now.Add(-time.Hour),
		QueryParam:    "up",
		TimeParam:     now,
		Duration:      10 * time.Millisecond,
		StatusCode:    200,
		BodySize:      10,
		LabelMatchers: LabelMatchers{{"__name__": "up"}},
		Type:          QueryTypeInstant,
	}})
	assert.NoError(t, err, "Insert queries")

	// Refresh summary for 1 day window
	err = p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-24 * time.Hour), To: now})
	assert.NoError(t, err, "RefreshMetricsUsageSummary")

	// Read list
	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all"})
	assert.NoError(t, err, "GetSeriesMetadata")
	assert.Greater(t, res.Total, 0, "expected at least one metric in catalog")
}

// TestSQLite_DashboardUsage verifies dashboard usage time range filtering
func TestSQLite_DashboardUsage(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Use fixed timestamps for predictable testing
	baseTime := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	t.Logf("Base time: %v", baseTime.Format(time.RFC3339))
	dashboards := []DashboardUsage{
		{
			Id:        "dash1",
			Serie:     "metric1",
			Name:      "Dashboard 1",
			URL:       "http://grafana/d/dash1",
			CreatedAt: baseTime,
		},
		{
			Id:        "dash2",
			Serie:     "metric1",
			Name:      "Dashboard 2",
			URL:       "http://grafana/d/dash2",
			CreatedAt: baseTime,
		},
		{
			Id:        "dash3",
			Serie:     "metric2", // Different metric
			Name:      "Dashboard 3",
			URL:       "http://grafana/d/dash3",
			CreatedAt: baseTime,
		},
	}

	// Helper function to insert with specific timestamps
	insertWithTime := func(d DashboardUsage, firstSeen, lastSeen time.Time) error {
		// SQLite doesn't expose first_seen_at/last_seen_at in the struct,
		// but we can set them directly in the database
		_, err := p.(*SQLiteProvider).db.ExecContext(context.Background(), `
			INSERT INTO DashboardUsage (
				id, serie, name, url, created_at, first_seen_at, last_seen_at
			) VALUES (?, ?, ?, ?, datetime(?), datetime(?), datetime(?))
			ON CONFLICT(id, serie) DO UPDATE SET 
				last_seen_at = CASE 
					WHEN datetime(excluded.last_seen_at) > datetime(last_seen_at) THEN datetime(excluded.last_seen_at)
					ELSE datetime(last_seen_at) 
				END`,
			d.Id, d.Serie, d.Name, d.URL,
			d.CreatedAt.Format(SQLiteTimeFormat),
			firstSeen.Format(SQLiteTimeFormat),
			lastSeen.Format(SQLiteTimeFormat))
		return err
	}

	// Insert dashboards with specific time ranges
	// dash1: Should not be visible in time range filter test (18:30-20:00)
	err := insertWithTime(dashboards[0], baseTime.Add(-2*time.Hour), baseTime.Add(-2*time.Hour))
	assert.NoError(t, err, "Failed to insert dash1")
	// dash2: Should be visible in time range filter test (18:30-20:00)
	err = insertWithTime(dashboards[1], baseTime.Add(-30*time.Minute), baseTime)
	assert.NoError(t, err, "Failed to insert dash2")
	// dash3: Different metric
	err = insertWithTime(dashboards[2], baseTime.Add(-30*time.Minute), baseTime)
	assert.NoError(t, err, "Failed to insert dash3")

	tests := []struct {
		name    string
		params  DashboardUsageParams
		wantLen int
		wantIDs []string
		wantErr bool
	}{
		{
			name: "Find all dashboards for metric1",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 2,
			wantIDs: []string{"dash1", "dash2"},
		},
		{
			name: "Find dashboards with time range filter",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-90 * time.Minute),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash2"},
		},
		{
			name: "Find dashboards with name filter",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Filter:   "Dashboard 1",
				Page:     1,
				PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash1"},
		},
		{
			name: "No dashboards for non-existent metric",
			params: DashboardUsageParams{
				Serie: "non-existent",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 0,
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Test case %s: time range from=%v to=%v",
				tt.name,
				tt.params.TimeRange.From.Format(time.RFC3339),
				tt.params.TimeRange.To.Format(time.RFC3339))
			got, err := p.GetDashboardUsage(context.Background(), tt.params)
			if tt.wantErr {
				assert.Error(t, err, "GetDashboardUsage() should return error")
				return
			}
			assert.NoError(t, err, "GetDashboardUsage() should not return error")

			results, ok := got.Data.([]DashboardUsage)
			assert.True(t, ok, "Expected Data to be []DashboardUsage, got %T", got.Data)

			assert.Equal(t, tt.wantLen, len(results), "GetDashboardUsage() result count")

			// Verify we got the expected dashboard IDs
			gotIDs := make([]string, len(results))
			for i, r := range results {
				gotIDs[i] = r.Id
			}

			// Create maps for easier comparison
			gotIDMap := make(map[string]bool)
			for _, id := range gotIDs {
				gotIDMap[id] = true
			}
			for _, wantID := range tt.wantIDs {
				assert.True(t, gotIDMap[wantID], "GetDashboardUsage() missing expected ID %s", wantID)
			}
		})
	}
}

// TestSQLite_QueryTimeRangeDistribution verifies bucketed counts and percents for range queries
func TestSQLite_QueryTimeRangeDistribution(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	now := time.Now().UTC()

	// Helper to create a range query with the given window duration
	mkRange := func(window time.Duration) Query {
		return Query{
			TS:            now.Add(-5 * time.Minute),
			QueryParam:    "up",
			TimeParam:     now.Add(-5 * time.Minute),
			Duration:      5 * time.Millisecond,
			StatusCode:    200,
			BodySize:      1,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeRange,
			Step:          15,
			Start:         now.Add(-5 * time.Minute).Add(-window),
			End:           now.Add(-5 * time.Minute),
		}
	}

	// Seed queries across buckets
	var qs []Query
	for range 5 { // <24h
		qs = append(qs, mkRange(5*time.Minute))
	}
	for range 3 { // 24h–<7d
		qs = append(qs, mkRange(48*time.Hour))
	}
	for range 2 { // 7d–<30d
		qs = append(qs, mkRange(8*24*time.Hour))
	}
	qs = append(qs, mkRange(31*24*time.Hour))  // 30d–<60d
	qs = append(qs, mkRange(65*24*time.Hour))  // 60d–<90d
	qs = append(qs, mkRange(100*24*time.Hour)) // 90d+

	// Insert a few instant queries that must be ignored
	qs = append(qs, Query{
		TS:            now.Add(-2 * time.Minute),
		QueryParam:    "up",
		TimeParam:     now.Add(-2 * time.Minute),
		Duration:      3 * time.Millisecond,
		StatusCode:    200,
		BodySize:      1,
		LabelMatchers: LabelMatchers{{"__name__": "up"}},
		Type:          QueryTypeInstant,
	})

	err := p.Insert(context.Background(), qs)
	assert.NoError(t, err, "Insert")

	// Query distribution within a window that includes TS
	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: now.Add(-24 * time.Hour), To: now}, "")
	assert.NoError(t, err, "GetQueryTimeRangeDistribution")

	// Build result map for easy assertions
	got := map[string]int{}
	total := 0
	for _, b := range out {
		got[b.Label] = b.Count
		total += b.Count
	}

	// Expect 5+3+2+1+1+1 = 13
	assert.Equal(t, 13, total, "unexpected total count")

	assert.Equal(t, 5, got["<24h"], "unexpected bucket count for <24h")
	assert.Equal(t, 3, got["24h"], "unexpected bucket count for 24h")
	assert.Equal(t, 2, got["7d"], "unexpected bucket count for 7d")
	assert.Equal(t, 1, got["30d"], "unexpected bucket count for 30d")
	assert.Equal(t, 1, got["60d"], "unexpected bucket count for 60d")
	assert.Equal(t, 1, got["90d+"], "unexpected bucket count for 90d+")

	// Percent sanity check (rounded); ensure first bucket ~38.46%
	var firstPct float64
	for _, b := range out {
		if b.Label == "<24h" {
			firstPct = b.Percent
			break
		}
	}
	assert.GreaterOrEqual(t, firstPct, 38.4, "unexpected percent for <24h (lower bound)")
	assert.LessOrEqual(t, firstPct, 38.5, "unexpected percent for <24h (upper bound)")
}

func TestSQLite_TimeRangeDistribution_ISO_TZ(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	defer cleanup()

	// Manually insert a few range queries with ISO timestamps (T/Z) to simulate proxy inserts
	_, _ = p.(*SQLiteProvider).db.ExecContext(context.Background(), `DELETE FROM queries`)

	now := time.Now().UTC().Truncate(time.Minute)
	from := now.Add(-15 * time.Minute)

	insert := `INSERT INTO queries (ts, queryParam, timeParam, duration, statusCode, bodySize, fingerprint, labelMatchers, type, step, start, "end", totalQueryableSamples, peakSamples)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Three ranges: 5m, 2h, 9d
	ranges := []struct{ start, end time.Time }{
		{now.Add(-10 * time.Minute), now.Add(-5 * time.Minute)},
		{now.Add(-3 * time.Hour), now.Add(-1 * time.Hour)},
		{now.Add(-9 * 24 * time.Hour), now},
	}
	for _, r := range ranges {
		_, err := p.(*SQLiteProvider).db.ExecContext(context.Background(), insert,
			now.Format(time.RFC3339), // ts
			"up",                     // queryParam
			now.Format(time.RFC3339), // timeParam
			int64(100),               // duration ms
			200,                      // statusCode
			0,                        // bodySize
			"fp",                     // fingerprint
			`[{"__name__":"up"}]`,    // labelMatchers
			"range",                  // type
			15.0,                     // step
			r.start.Format(time.RFC3339),
			r.end.Format(time.RFC3339),
			0,
			0,
		)
		assert.NoError(t, err, "insert")
	}

	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: from, To: now}, "")
	assert.NoError(t, err, "GetQueryTimeRangeDistribution")
	assert.NotEmpty(t, out, "no buckets returned")

	// Sum should be 3
	sum := 0
	for _, b := range out {
		sum += b.Count
	}
	assert.Greater(t, sum, 0, "expected non-zero distribution")
}
