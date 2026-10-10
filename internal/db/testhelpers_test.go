package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testBackend is what a scenario shared by both backends needs from one of
// them: a provider, plus the dialect details its raw SQL depends on.
type testBackend struct {
	newProvider func(testing.TB) Provider
	// rebind rewrites "?" placeholders into the backend's syntax.
	rebind func(query string) string
}

// mustExec runs query, written with ? placeholders, on p in b's dialect.
// time.Time arguments are bound as the providers bind them, so seeded rows
// are stored in the format production writes.
func (b testBackend) mustExec(t testing.TB, p Provider, query string, args ...any) {
	t.Helper()
	_, err := rawDB(p).ExecContext(context.Background(), b.rebind(query), args...)
	require.NoError(t, err, query)
}

// markCatalogStale moves name's last sync 90 days back, as for a metric
// Prometheus stopped reporting long ago. SQLite writes last_synced_at with
// datetime('now'), so the time is bound in that text format, which
// PostgreSQL also parses into its timestamp column.
func (b testBackend) markCatalogStale(t testing.TB, p Provider, name string) {
	t.Helper()
	b.mustExec(t, p, `UPDATE metrics_catalog SET last_synced_at = ? WHERE name = ?`,
		time.Now().UTC().AddDate(0, 0, -90).Format(SQLiteTimeFormat), name)
}

// retireUsage moves the presence of table's rows whose column equals value
// to 100..99 days ago. Insert*Usage stamps presence at call time, so usage
// that ended long ago can only be seeded this way.
func (b testBackend) retireUsage(t testing.TB, p Provider, table, column, value string) {
	t.Helper()
	now := time.Now().UTC()
	b.mustExec(t, p, `UPDATE `+table+` SET first_seen_at = ?, last_seen_at = ? WHERE `+column+` = ?`,
		now.AddDate(0, 0, -100), now.AddDate(0, 0, -99), value)
}

// presenceWindow spans from to a minute past now, covering usage presence
// stamped by any earlier Insert*Usage call: stamps take the wall clock at
// insert, after a test's own now, and bounds are truncated to whole seconds.
func presenceWindow(from time.Time) TimeRange {
	return TimeRange{From: from, To: time.Now().UTC().Add(time.Minute)}
}

// instantQuery is a successful 1ms instant query on metric at ts. QueryParam
// is metric and LabelMatchers selects it, so every metric-scoped read
// matches it; callers set only the fields their guarantee depends on.
func instantQuery(ts time.Time, metric string) Query {
	return Query{
		TS: ts, QueryParam: metric, TimeParam: ts, Duration: time.Millisecond, StatusCode: 200,
		LabelMatchers: LabelMatchers{{"__name__": metric}}, Type: QueryTypeInstant,
	}
}

// rowsOf returns res's rows, failing t when res lists rows of another type.
func rowsOf[T any](t testing.TB, res PagedResult) []T {
	t.Helper()
	rows, ok := res.Data.([]T)
	require.True(t, ok, "got %T", res.Data)
	return rows
}

// keysOf returns key(row) for each row, in order.
func keysOf[T any](rows []T, key func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, key(r))
	}
	return out
}

// statusTotals sums rows' 2xx, 4xx and 5xx counts over every bucket.
func statusTotals(rows []QueryStatusDistributionResult) [3]int {
	var total [3]int
	for _, r := range rows {
		total[0] += r.Status2xx
		total[1] += r.Status4xx
		total[2] += r.Status5xx
	}
	return total
}

// rawDB returns p's underlying connection pool, for state no Provider method
// can seed or expose.
func rawDB(p Provider) *sql.DB {
	var db *sql.DB
	p.WithDB(func(d *sql.DB) { db = d })
	return db
}

func mustInsertQueries(t *testing.T, p Provider, qs []Query) {
	t.Helper()
	require.NoError(t, p.Insert(context.Background(), qs), "Insert")
}

func mustUpsertCatalog(t *testing.T, p Provider, items []MetricCatalogItem) {
	t.Helper()
	require.NoError(t, p.UpsertMetricsCatalog(context.Background(), items), "UpsertMetricsCatalog")
}

func mustUpsertJobIndex(t *testing.T, p Provider, items []MetricJobIndexItem) {
	t.Helper()
	require.NoError(t, p.UpsertMetricsJobIndex(context.Background(), items), "UpsertMetricsJobIndex")
}

func mustInsertRules(t *testing.T, p Provider, items []RulesUsage) {
	t.Helper()
	require.NoError(t, p.InsertRulesUsage(context.Background(), items), "InsertRulesUsage")
}

func mustInsertDashboards(t *testing.T, p Provider, items []DashboardUsage) {
	t.Helper()
	require.NoError(t, p.InsertDashboardUsage(context.Background(), items), "InsertDashboardUsage")
}

type summaryRow struct {
	Alert, Record, Dashboard, Query int
	Unused                          bool
}

// mustSummaryRow reads back one metrics_usage_summary row. Asserting on the
// whole row shows which count is off when is_unused, derived from all four,
// is wrong.
func mustSummaryRow(t *testing.T, b testBackend, p Provider, name string) summaryRow {
	t.Helper()
	var r summaryRow
	row := rawDB(p).QueryRowContext(context.Background(), b.rebind(
		`SELECT alert_count, record_count, dashboard_count, query_count, is_unused FROM metrics_usage_summary WHERE name = ?`), name)
	require.NoError(t, row.Scan(&r.Alert, &r.Record, &r.Dashboard, &r.Query, &r.Unused))
	return r
}
