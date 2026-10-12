package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testInsertRulesUsageGetRulesUsage(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	base := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	rules := []RulesUsage{
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l2", "l1"}, CreatedAt: base},
		// Same rule, labels in another order: de-duplicated.
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1", Kind: string(RuleUsageKindAlert), Labels: []string{"l1", "l2"}, CreatedAt: base},
		{Serie: "up", GroupName: "g2", Name: "r2", Expression: "expr2", Kind: string(RuleUsageKindRecord), Labels: []string{"lbl"}, CreatedAt: base},
	}
	mustInsertRules(t, p, rules)

	out, err := p.GetRulesUsage(context.Background(), RulesUsageParams{
		Serie:     "up",
		Kind:      string(RuleUsageKindAlert),
		TimeRange: presenceWindow(time.Now().UTC().Add(-1 * time.Hour)),
		Page:      1,
		PageSize:  10,
	})
	require.NoError(t, err, "GetRulesUsage")
	assert.Len(t, rowsOf[RulesUsage](t, out), 1, "one alert rule for serie=up after de-duplication")
}

func TestPostgreSQL_InsertRulesUsage_GetRulesUsage(t *testing.T) {
	t.Parallel()
	testInsertRulesUsageGetRulesUsage(t, postgreSQLTestBackend)
}

func TestSQLite_InsertRulesUsage_GetRulesUsage(t *testing.T) {
	t.Parallel()
	testInsertRulesUsageGetRulesUsage(t, sqliteTestBackend)
}

// testGetRulesUsageMaliciousSortOrderDoesNotBreakQuery verifies GetRulesUsage
// normalizes SortOrder through ValidateSortField, the whitelist every
// paginated method uses, so an arbitrary value can neither reach the SQL text
// nor break the query.
func testGetRulesUsageMaliciousSortOrderDoesNotBreakQuery(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	mustInsertRules(t, p, []RulesUsage{
		{Serie: "up", GroupName: "g1", Name: "r1", Expression: "e1", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}},
		{Serie: "up", GroupName: "g2", Name: "r2", Expression: "e2", Kind: string(RuleUsageKindAlert), Labels: []string{"l"}},
	})

	out, err := p.GetRulesUsage(context.Background(), RulesUsageParams{
		Serie:     "up",
		Kind:      string(RuleUsageKindAlert),
		TimeRange: presenceWindow(time.Now().UTC().Add(-1 * time.Hour)),
		Page:      1,
		PageSize:  10,
		SortBy:    "name",
		SortOrder: "asc; DROP TABLE RulesUsage; --",
	})
	require.NoError(t, err, "GetRulesUsage must not error on an invalid SortOrder")
	assert.Len(t, rowsOf[RulesUsage](t, out), 2,
		"both rules must still be returned - an invalid SortOrder must fall back to a safe default order, not corrupt the result set")
}

func TestPostgreSQL_GetRulesUsage_MaliciousSortOrderDoesNotBreakQuery(t *testing.T) {
	t.Parallel()
	testGetRulesUsageMaliciousSortOrderDoesNotBreakQuery(t, postgreSQLTestBackend)
}

func TestSQLite_GetRulesUsage_MaliciousSortOrderDoesNotBreakQuery(t *testing.T) {
	t.Parallel()
	testGetRulesUsageMaliciousSortOrderDoesNotBreakQuery(t, sqliteTestBackend)
}

// testDashboardUsage verifies GetDashboardUsage lists a series' dashboards
// present in the range and matching the name filter.
func testDashboardUsage(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	baseTime := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	// InsertDashboardUsage stamps presence at call time, so each window is
	// seeded directly. dash1's ends before the narrower range used below.
	for _, d := range []struct {
		id, serie, name string
		first, last     time.Time
	}{
		{"dash1", "metric1", "Dashboard 1", baseTime.Add(-2 * time.Hour), baseTime.Add(-2 * time.Hour)},
		{"dash2", "metric1", "Dashboard 2", baseTime.Add(-30 * time.Minute), baseTime},
		{"dash3", "metric2", "Dashboard 3", baseTime.Add(-30 * time.Minute), baseTime},
	} {
		b.mustExec(t, p, `INSERT INTO DashboardUsage (id, serie, name, url, created_at, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			d.id, d.serie, d.name, "http://grafana/d/"+d.id, baseTime, d.first, d.last)
	}

	wide := TimeRange{From: baseTime.Add(-3 * time.Hour), To: baseTime}
	tests := []struct {
		name    string
		params  DashboardUsageParams
		wantIDs []string
	}{
		{"all dashboards of a series", DashboardUsageParams{Serie: "metric1", TimeRange: wide}, []string{"dash1", "dash2"}},
		{"range excludes ended presence", DashboardUsageParams{Serie: "metric1", TimeRange: TimeRange{From: baseTime.Add(-90 * time.Minute), To: baseTime}}, []string{"dash2"}},
		{"name filter", DashboardUsageParams{Serie: "metric1", TimeRange: wide, Filter: "Dashboard 1"}, []string{"dash1"}},
		{"unknown series", DashboardUsageParams{Serie: "non-existent", TimeRange: wide}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params.Page, tt.params.PageSize = 1, 10
			got, err := p.GetDashboardUsage(context.Background(), tt.params)
			require.NoError(t, err)
			ids := keysOf(rowsOf[DashboardUsage](t, got), func(d DashboardUsage) string { return d.Id })
			assert.ElementsMatch(t, tt.wantIDs, ids)
		})
	}
}

func TestPostgreSQL_DashboardUsage(t *testing.T) {
	t.Parallel()
	testDashboardUsage(t, postgreSQLTestBackend)
}

func TestSQLite_DashboardUsage(t *testing.T) {
	t.Parallel()
	testDashboardUsage(t, sqliteTestBackend)
}

func testInsertDashboardUsageUpsertBehavior(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	base := time.Now().UTC().Truncate(time.Minute)
	mustInsertDashboards(t, p, []DashboardUsage{{Id: "d1", Serie: "m1", Name: "Dash 1", URL: "http://d/1", CreatedAt: base}})
	// Re-inserting updates name and url.
	mustInsertDashboards(t, p, []DashboardUsage{{Id: "d1", Serie: "m1", Name: "Dash 1 Renamed", URL: "http://d/1r", CreatedAt: base}})

	out, err := p.GetDashboardUsage(context.Background(), DashboardUsageParams{
		Serie:     "m1",
		TimeRange: TimeRange{From: base.Add(-1 * time.Hour), To: base.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
	})
	require.NoError(t, err, "GetDashboardUsage")
	rows := rowsOf[DashboardUsage](t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, "Dash 1 Renamed", rows[0].Name)
	assert.Equal(t, "http://d/1r", rows[0].URL)
}

func TestPostgreSQL_InsertDashboardUsage_UpsertBehavior(t *testing.T) {
	t.Parallel()
	testInsertDashboardUsageUpsertBehavior(t, postgreSQLTestBackend)
}

func TestSQLite_InsertDashboardUsage_UpsertBehavior(t *testing.T) {
	t.Parallel()
	testInsertDashboardUsageUpsertBehavior(t, sqliteTestBackend)
}

// testGetDashboardUsageMaliciousSortOrderDoesNotBreakQuery is
// testGetRulesUsageMaliciousSortOrderDoesNotBreakQuery for GetDashboardUsage.
func testGetDashboardUsageMaliciousSortOrderDoesNotBreakQuery(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	base := time.Now().UTC().Truncate(time.Minute)
	mustInsertDashboards(t, p, []DashboardUsage{
		{Id: "d1", Serie: "m1", Name: "Dash 1", URL: "http://d/1", CreatedAt: base},
		{Id: "d2", Serie: "m1", Name: "Dash 2", URL: "http://d/2", CreatedAt: base},
	})

	out, err := p.GetDashboardUsage(context.Background(), DashboardUsageParams{
		Serie:     "m1",
		TimeRange: TimeRange{From: base.Add(-1 * time.Hour), To: base.Add(1 * time.Hour)},
		Page:      1,
		PageSize:  10,
		SortBy:    "name",
		SortOrder: "asc; DROP TABLE DashboardUsage; --",
	})
	require.NoError(t, err, "GetDashboardUsage must not error on an invalid SortOrder")
	assert.Len(t, rowsOf[DashboardUsage](t, out), 2,
		"both dashboards must still be returned - an invalid SortOrder must fall back to a safe default order, not corrupt the result set")
}

func TestPostgreSQL_GetDashboardUsage_MaliciousSortOrderDoesNotBreakQuery(t *testing.T) {
	t.Parallel()
	testGetDashboardUsageMaliciousSortOrderDoesNotBreakQuery(t, postgreSQLTestBackend)
}

func TestSQLite_GetDashboardUsage_MaliciousSortOrderDoesNotBreakQuery(t *testing.T) {
	t.Parallel()
	testGetDashboardUsageMaliciousSortOrderDoesNotBreakQuery(t, sqliteTestBackend)
}

func testGetMetricStatisticsAndQueryPerformanceStats(t *testing.T, b testBackend) {
	p := b.newProvider(t)

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

	tr := presenceWindow(now.Add(-1 * time.Hour))

	stats, err := p.GetMetricStatistics(context.Background(), metric, tr)
	require.NoError(t, err, "GetMetricStatistics")
	assert.Equal(t, [3]int{1, 1, 1}, [3]int{stats.AlertCount, stats.RecordCount, stats.DashboardCount},
		"alerts, records, dashboards using the metric")
	assert.Equal(t, [3]int{2, 1, 2}, [3]int{stats.TotalAlerts, stats.TotalRecords, stats.TotalDashboards},
		"alerts, records, dashboards across all metrics")

	perf, err := p.GetMetricQueryPerformanceStatistics(context.Background(), metric, tr)
	require.NoError(t, err, "GetMetricQueryPerformanceStatistics")
	require.NotNil(t, perf.TotalQueries)
	require.NotNil(t, perf.AverageDuration)
	require.NotNil(t, perf.PeakSamples)
	assert.Equal(t, 2, *perf.TotalQueries)
	assert.InDelta(t, 15.0, *perf.AverageDuration, 0.2)
	assert.Equal(t, 200, *perf.PeakSamples)
}

func TestPostgreSQL_GetMetricStatistics_And_QueryPerformanceStats(t *testing.T) {
	t.Parallel()
	testGetMetricStatisticsAndQueryPerformanceStats(t, postgreSQLTestBackend)
}

func TestSQLite_GetMetricStatistics_And_QueryPerformanceStats(t *testing.T) {
	t.Parallel()
	testGetMetricStatisticsAndQueryPerformanceStats(t, sqliteTestBackend)
}
