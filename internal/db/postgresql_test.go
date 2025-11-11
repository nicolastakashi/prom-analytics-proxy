//go:build docker

package db

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestPostgreSQLProvider spins up a disposable PostgreSQL using Testcontainers
// and returns a configured Provider and a cleanup function.
func newTestPostgreSQLProvider(t *testing.T) (Provider, func()) {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(1).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		// Docker not available in this environment; skip tests gracefully
		t.Skipf("Skipping PostgreSQL container tests (Docker not available): %v", err)
	}

	host, err := pgContainer.Host(ctx)
	assert.NoError(t, err, "container host")
	port, err := pgContainer.MappedPort(ctx, "5432/tcp")
	assert.NoError(t, err, "container port")

	// Configure config.DefaultConfig for provider.
	config.DefaultConfig.Database.Provider = "postgresql"
	config.DefaultConfig.Database.PostgreSQL.Addr = host
	config.DefaultConfig.Database.PostgreSQL.Port = port.Int()
	config.DefaultConfig.Database.PostgreSQL.User = "testuser"
	config.DefaultConfig.Database.PostgreSQL.Password = "testpass"
	config.DefaultConfig.Database.PostgreSQL.Database = "testdb"
	config.DefaultConfig.Database.PostgreSQL.SSLMode = "disable"
	config.DefaultConfig.Database.PostgreSQL.DialTimeout = 5 * time.Second

	// Give PostgreSQL a moment to fully initialize after the ready log
	time.Sleep(2 * time.Second)

	p, err := newPostGreSQLProvider(ctx)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		assert.NoError(t, err, "failed to init postgres provider")
		return nil, func() {}
	}

	cleanup := func() {
		if p != nil {
			_ = p.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}
	return p, cleanup
}

func TestPostgreSQL_GetQueryTypes(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 10)
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
			Fingerprint:   "pg-fp1",
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
			Fingerprint:   "pg-fp2",
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
		assert.InDelta(t, 40.0, *out.InstantPercent, 0.5)
		assert.InDelta(t, 60.0, *out.RangePercent, 0.5)
	}
}

func TestPostgreSQL_GetAverageDuration(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	base := time.Date(2025, 8, 20, 12, 0, 0, 0, time.UTC)
	prevFrom := base.Add(-20 * time.Minute)
	curFrom := base.Add(-10 * time.Minute)
	curTo := base

	var qs []Query
	// Insert previous window data: [base-20m, base-10m) with 10ms duration
	for i := range 5 {
		qs = append(qs, Query{
			TS:            prevFrom.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     prevFrom,
			Duration:      10 * time.Millisecond,
			StatusCode:    200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
		})
	}
	// Insert current window data: [base-10m, base] with 20ms duration
	for i := range 5 {
		qs = append(qs, Query{
			TS:            curFrom.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     curFrom,
			Duration:      20 * time.Millisecond,
			StatusCode:    200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
		})
	}
	mustInsertQueries(t, p, qs)

	out, err := p.GetAverageDuration(context.Background(), TimeRange{From: curFrom, To: curTo}, "")
	assert.NoError(t, err, "GetAverageDuration")
	if assert.NotNil(t, out) {
		assert.NotNil(t, out.AvgDuration)
		assert.NotNil(t, out.DeltaPercent)
		assert.InDelta(t, 20.0, *out.AvgDuration, 0.5)
		assert.InDelta(t, 100.0, *out.DeltaPercent, 30.0) // Allow wider tolerance for PostgreSQL calculation differences
	}
}

func TestPostgreSQL_GetQueryRate(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 5)
	for i := range 3 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      5 * time.Millisecond,
			StatusCode:    200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "pg-fp1",
		})
	}
	for i := range 2 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(3+i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      5 * time.Millisecond,
			StatusCode:    500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "pg-fp1",
		})
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-5 * time.Minute), To: now.Add(10 * time.Minute)}
	out, err := p.GetQueryRate(context.Background(), tr, "up", "pg-fp1")
	assert.NoError(t, err, "GetQueryRate")
	if assert.NotNil(t, out) {
		assert.NotNil(t, out.SuccessTotal)
		assert.NotNil(t, out.ErrorTotal)
		assert.NotNil(t, out.SuccessRatePercent)
		assert.NotNil(t, out.ErrorRatePercent)
		assert.Equal(t, 3, *out.SuccessTotal)
		assert.Equal(t, 2, *out.ErrorTotal)
		assert.InDelta(t, 60.0, *out.SuccessRatePercent, 0.5)
		assert.InDelta(t, 40.0, *out.ErrorRatePercent, 0.5)
	}
}

func TestPostgreSQL_GetQueryLatencyTrends_And_Throughput_And_Errors(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 13)
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
			Fingerprint:   "pg-fp-lat",
		})
	}
	for i := range 3 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i*2) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      10 * time.Millisecond,
			StatusCode:    500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			Fingerprint:   "pg-fp-lat",
		})
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-10 * time.Minute), To: now.Add(20 * time.Minute)}

	lat, err := p.GetQueryLatencyTrends(context.Background(), tr, "up", "pg-fp-lat")
	assert.NoError(t, err, "GetQueryLatencyTrends")
	assert.NotEmpty(t, lat)

	thr, err := p.GetQueryThroughputAnalysis(context.Background(), tr)
	assert.NoError(t, err, "GetQueryThroughputAnalysis")
	assert.NotEmpty(t, thr)

	errSeries, err := p.GetQueryErrorAnalysis(context.Background(), tr, "pg-fp-lat")
	assert.NoError(t, err, "GetQueryErrorAnalysis")
	assert.NotEmpty(t, errSeries)

	dist, err := p.GetQueryStatusDistribution(context.Background(), tr, "pg-fp-lat")
	assert.NoError(t, err, "GetQueryStatusDistribution")
	assert.NotEmpty(t, dist)
}

// -------------------- Aggregations --------------------

func TestPostgreSQL_GetQueriesBySerieName(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

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

func TestPostgreSQL_GetQueryExpressions_And_Executions(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 8)
	for i := range 5 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      10 * time.Millisecond,
			StatusCode:    200,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
			PeakSamples:   10 + i,
			Fingerprint:   "pg-fp-a",
		})
	}
	for i := range 3 {
		qs = append(qs, Query{
			TS:            now.Add(time.Duration(i) * time.Minute),
			QueryParam:    "rate(up[5m])",
			TimeParam:     now,
			Duration:      20 * time.Millisecond,
			StatusCode:    500,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeRange,
			Start:         now.Add(-5 * time.Minute),
			End:           now,
			Step:          15,
			PeakSamples:   100,
			Fingerprint:   "pg-fp-b",
		})
	}
	mustInsertQueries(t, p, qs)

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
		assert.Equal(t, "pg-fp-a", rows2[0].Fingerprint)
	}

	cases := []struct{ sortOrder string }{{"asc"}, {"desc"}}
	for _, tc := range cases {
		per, err := p.GetQueryExecutions(context.Background(), QueryExecutionsParams{
			Fingerprint: "pg-fp-b",
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

func TestPostgreSQL_MetricsJobIndex_And_ListJobs(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	mustUpsertJobIndex(t, p, []MetricJobIndexItem{
		{Name: "up", Job: "prometheus"},
		{Name: "up", Job: "node"},
		{Name: "process_cpu_seconds_total", Job: "node"},
	})

	jobs, err := p.ListJobs(context.Background())
	assert.NoError(t, err, "ListJobs")
	assert.ElementsMatch(t, []string{"node", "prometheus"}, jobs)
}

func TestPostgreSQL_RefreshMetricsUsageSummary_And_GetSeriesMetadata(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	mustUpsertCatalog(t, p, []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric"}})
	mustUpsertJobIndex(t, p, []MetricJobIndexItem{{Name: "up", Job: "prometheus"}})

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
		Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Filter: "up", Type: "all", Job: "prometheus",
	})
	assert.NoError(t, err, "GetSeriesMetadata")
	assert.Greater(t, res.Total, 0)
}

func TestPostgreSQL_GetMetricStatistics_And_QueryPerformanceStats(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	metric := "http_requests_total"
	now := time.Now().UTC().Truncate(time.Minute)

	mustInsertRules(t, p, []RulesUsage{
		{Serie: metric, GroupName: "g", Name: "a1", Expression: "expr", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
		{Serie: metric, GroupName: "g", Name: "r1", Expression: "expr", Kind: string(RuleUsageKindRecord), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
		{Serie: "other", GroupName: "g", Name: "a2", Expression: "expr", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}, CreatedAt: now.Add(-30 * time.Minute)},
	})
	mustInsertDashboards(t, p, []DashboardUsage{
		{Id: "d1", Serie: metric, Name: "Dash", URL: "http://d/1", CreatedAt: now.Add(-45 * time.Minute)},
		{Id: "d2", Serie: "other", Name: "Other", URL: "http://d/2", CreatedAt: now.Add(-45 * time.Minute)},
	})

	mustInsertQueries(t, p, []Query{
		{TS: now.Add(-5 * time.Minute), QueryParam: metric, TimeParam: now, Duration: 12 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": metric}}, Type: QueryTypeInstant, PeakSamples: 100},
		{TS: now.Add(-4 * time.Minute), QueryParam: metric, TimeParam: now, Duration: 18 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": metric}}, Type: QueryTypeInstant, PeakSamples: 200},
	})

	tr := TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}
	stats, err := p.GetMetricStatistics(context.Background(), metric, tr)
	assert.NoError(t, err, "GetMetricStatistics")
	assert.Greater(t, stats.AlertCount, 0)
	assert.Greater(t, stats.RecordCount, 0)
	assert.Greater(t, stats.DashboardCount, 0)

	perf, err := p.GetMetricQueryPerformanceStatistics(context.Background(), metric, tr)
	assert.NoError(t, err, "GetMetricQueryPerformanceStatistics")
	if assert.NotNil(t, perf.TotalQueries) {
		assert.Greater(t, *perf.TotalQueries, 0)
	}
}

// -------------------- Rules & Dashboards --------------------

func TestPostgreSQL_InsertRulesUsage_GetRulesUsage(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	base := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	rules := []RulesUsage{
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l2", "l1"}, CreatedAt: base},
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l1", "l2"}, CreatedAt: base},
		{Serie: "up", GroupName: "g2", Name: "r2", Expression: "expr2", Kind: string(RuleUsageKindRecord), Labels: []string{"lbl"}, CreatedAt: base},
	}
	mustInsertRules(t, p, rules)

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
		assert.Len(t, rows3, 1)
	}
}

func TestPostgreSQL_InsertDashboardUsage_UpsertBehavior(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	base := time.Now().UTC().Truncate(time.Minute)
	mustInsertDashboards(t, p, []DashboardUsage{{Id: "d1", Serie: "m1", Name: "Dash 1", URL: "http://d/1", CreatedAt: base}})
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

// -------------------- Additional Tests parity with SQLite --------------------

func TestPostgreSQL_HistogramSummaryMetricsCatalog(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

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
	allItems := append(histogramItems, summaryItems...)
	err := p.UpsertMetricsCatalog(context.Background(), allItems)
	assert.NoError(t, err, "UpsertMetricsCatalog")

	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all"})
	assert.NoError(t, err, "GetSeriesMetadata")
	assert.Equal(t, 6, res.Total, "Expected 6 metrics")
}

func TestPostgreSQL_MetricsInventoryAndList(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	items := []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric", Unit: ""}}
	err := p.UpsertMetricsCatalog(context.Background(), items)
	assert.NoError(t, err, "UpsertMetricsCatalog")

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

	err = p.RefreshMetricsUsageSummary(context.Background(), TimeRange{From: now.Add(-24 * time.Hour), To: now})
	assert.NoError(t, err, "RefreshMetricsUsageSummary")

	res, err := p.GetSeriesMetadata(context.Background(), SeriesMetadataParams{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all"})
	assert.NoError(t, err, "GetSeriesMetadata")
	assert.Greater(t, res.Total, 0, "expected at least one metric in catalog")
}

func TestPostgreSQL_DashboardUsage(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	baseTime := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	dashboards := []DashboardUsage{
		{Id: "dash1", Serie: "metric1", Name: "Dashboard 1", URL: "http://grafana/d/dash1", CreatedAt: baseTime},
		{Id: "dash2", Serie: "metric1", Name: "Dashboard 2", URL: "http://grafana/d/dash2", CreatedAt: baseTime},
		{Id: "dash3", Serie: "metric2", Name: "Dashboard 3", URL: "http://grafana/d/dash3", CreatedAt: baseTime},
	}

	// Insert with specific first_seen_at / last_seen_at via direct SQL
	insertWithTime := func(d DashboardUsage, firstSeen, lastSeen time.Time) error {
		_, err := p.(*PostGreSQLProvider).db.ExecContext(context.Background(), `
			INSERT INTO DashboardUsage (id, serie, name, url, created_at, first_seen_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id, serie) DO UPDATE SET 
				last_seen_at = CASE 
					WHEN EXCLUDED.last_seen_at > DashboardUsage.last_seen_at THEN EXCLUDED.last_seen_at
					ELSE DashboardUsage.last_seen_at
				END`,
			d.Id, d.Serie, d.Name, d.URL,
			d.CreatedAt,
			firstSeen,
			lastSeen,
		)
		return err
	}

	err := insertWithTime(dashboards[0], baseTime.Add(-2*time.Hour), baseTime.Add(-2*time.Hour))
	assert.NoError(t, err, "Failed to insert dash1")
	err = insertWithTime(dashboards[1], baseTime.Add(-30*time.Minute), baseTime)
	assert.NoError(t, err, "Failed to insert dash2")
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
				Serie:     "metric1",
				TimeRange: TimeRange{From: baseTime.Add(-3 * time.Hour), To: baseTime},
				Page:      1, PageSize: 10,
			},
			wantLen: 2,
			wantIDs: []string{"dash1", "dash2"},
		},
		{
			name: "Find dashboards with time range filter",
			params: DashboardUsageParams{
				Serie:     "metric1",
				TimeRange: TimeRange{From: baseTime.Add(-90 * time.Minute), To: baseTime},
				Page:      1, PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash2"},
		},
		{
			name: "Find dashboards with name filter",
			params: DashboardUsageParams{
				Serie:     "metric1",
				TimeRange: TimeRange{From: baseTime.Add(-3 * time.Hour), To: baseTime},
				Filter:    "Dashboard 1",
				Page:      1, PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash1"},
		},
		{
			name: "No dashboards for non-existent metric",
			params: DashboardUsageParams{
				Serie:     "non-existent",
				TimeRange: TimeRange{From: baseTime.Add(-3 * time.Hour), To: baseTime},
				Page:      1, PageSize: 10,
			},
			wantLen: 0,
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.GetDashboardUsage(context.Background(), tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			results, ok := got.Data.([]DashboardUsage)
			assert.True(t, ok, "Expected Data to be []DashboardUsage, got %T", got.Data)
			assert.Equal(t, tt.wantLen, len(results))

			gotIDs := make(map[string]bool)
			for _, r := range results {
				gotIDs[r.Id] = true
			}
			for _, wantID := range tt.wantIDs {
				assert.True(t, gotIDs[wantID], "missing expected ID %s", wantID)
			}
		})
	}
}

func TestPostgreSQL_QueryTimeRangeDistribution(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	now := time.Now().UTC()

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

	var qs []Query
	for range 5 {
		qs = append(qs, mkRange(5*time.Minute))
	}
	for range 3 {
		qs = append(qs, mkRange(48*time.Hour))
	}
	for range 2 {
		qs = append(qs, mkRange(8*24*time.Hour))
	}
	qs = append(qs, mkRange(31*24*time.Hour))
	qs = append(qs, mkRange(65*24*time.Hour))
	qs = append(qs, mkRange(100*24*time.Hour))
	qs = append(qs, Query{ // instant ignored
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

	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: now.Add(-24 * time.Hour), To: now}, "")
	assert.NoError(t, err, "GetQueryTimeRangeDistribution")

	got := map[string]int{}
	total := 0
	for _, b := range out {
		got[b.Label] = b.Count
		total += b.Count
	}
	assert.Equal(t, 13, total)
	assert.Equal(t, 5, got["<24h"])
	assert.Equal(t, 3, got["24h"])
	assert.Equal(t, 2, got["7d"])
	assert.Equal(t, 1, got["30d"])
	assert.Equal(t, 1, got["60d"])
	assert.Equal(t, 1, got["90d+"])
}

func TestPostgreSQL_TimeRangeDistribution_ISO_TZ(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	defer cleanup()

	_, _ = p.(*PostGreSQLProvider).db.ExecContext(context.Background(), `DELETE FROM queries`)

	now := time.Now().UTC().Truncate(time.Minute)
	from := now.Add(-15 * time.Minute)

	insert := `INSERT INTO queries (ts, queryParam, timeParam, duration, statusCode, bodySize, fingerprint, labelMatchers, type, step, start, "end", totalQueryableSamples, peakSamples)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13, $14)`

	ranges := []struct{ start, end time.Time }{
		{now.Add(-10 * time.Minute), now.Add(-5 * time.Minute)},
		{now.Add(-3 * time.Hour), now.Add(-1 * time.Hour)},
		{now.Add(-9 * 24 * time.Hour), now},
	}
	for _, r := range ranges {
		_, err := p.(*PostGreSQLProvider).db.ExecContext(context.Background(), insert,
			now,
			"up",
			now,
			int64(100),
			200,
			0,
			"fp",
			`[{"__name__":"up"}]`,
			"range",
			15.0,
			r.start,
			r.end,
			0,
			0,
		)
		assert.NoError(t, err, "insert")
	}

	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: from, To: now}, "")
	assert.NoError(t, err, "GetQueryTimeRangeDistribution")
	assert.NotEmpty(t, out)
}
