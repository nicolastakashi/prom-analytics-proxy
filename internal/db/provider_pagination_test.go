package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertPages verifies a listing of three items paged two at a time: the
// pages are disjoint, together hold every item once, and both report
// Total 3 and TotalPages 2. fetch returns one page and its item keys.
func assertPages(t *testing.T, fetch func(page int) (PagedResult, []string)) {
	t.Helper()
	seen := map[string]bool{}
	for page, wantLen := range map[int]int{1: 2, 2: 1} {
		res, ids := fetch(page)
		assert.Equal(t, [2]int{3, 2}, [2]int{res.Total, res.TotalPages}, "page %d: Total, TotalPages", page)
		assert.Len(t, ids, wantLen, "page %d", page)
		for _, id := range ids {
			assert.False(t, seen[id], "page %d repeats %s", page, id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, 3, "items across both pages")
}

// testPaginatedListings verifies every paginated listing honors Page and
// PageSize and reports Total and TotalPages.
func testPaginatedListings(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	tr := TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}
	var qs []Query
	for i := range 3 {
		q := instantQuery(now.Add(time.Duration(i-10)*time.Minute), "up")
		q.QueryParam, q.Fingerprint = fmt.Sprintf("up%d", i), fmt.Sprintf("fp%d", i)
		x := instantQuery(now.Add(time.Duration(i-20)*time.Minute), "x")
		x.Fingerprint = "fpx"
		qs = append(qs, q, x)
	}
	mustInsertQueries(t, p, qs)
	var rules []RulesUsage
	var dashes []DashboardUsage
	var catalog []MetricCatalogItem
	for i := range 3 {
		rules = append(rules, RulesUsage{Serie: "up", GroupName: "g", Name: fmt.Sprintf("r%d", i), Expression: "up", Kind: string(RuleUsageKindAlert), Labels: []string{}, CreatedAt: now})
		dashes = append(dashes, DashboardUsage{Id: fmt.Sprintf("d%d", i), Serie: "up", Name: fmt.Sprintf("D%d", i), URL: "http://d", CreatedAt: now})
		catalog = append(catalog, MetricCatalogItem{Name: fmt.Sprintf("m%d", i)})
	}
	mustInsertRules(t, p, rules)
	mustInsertDashboards(t, p, dashes)
	mustUpsertCatalog(t, p, catalog)

	t.Run("GetQueryExpressions", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetQueryExpressions(ctx, QueryExpressionsParams{TimeRange: tr, Page: page, PageSize: 2, Filter: "up"})
			require.NoError(t, err)
			return res, fingerprints(t, res)
		})
	})
	t.Run("GetQueryExecutions", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetQueryExecutions(ctx, QueryExecutionsParams{Fingerprint: "fpx", TimeRange: tr, Page: page, PageSize: 2, SortBy: "ts", SortOrder: "asc"})
			require.NoError(t, err)
			return res, keysOf(rowsOf[QueryExecutionRow](t, res), func(r QueryExecutionRow) string { return r.Timestamp.UTC().Format(time.RFC3339) })
		})
	})
	t.Run("GetQueriesBySerieName", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetQueriesBySerieName(ctx, QueriesBySerieNameParams{SerieName: "up", TimeRange: tr, Page: page, PageSize: 2, SortBy: "avgDuration", SortOrder: "asc"})
			require.NoError(t, err)
			return res, keysOf(rowsOf[QueriesBySerieNameResult](t, res), func(r QueriesBySerieNameResult) string { return r.Query })
		})
	})
	t.Run("GetRulesUsage", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetRulesUsage(ctx, RulesUsageParams{Serie: "up", Kind: string(RuleUsageKindAlert), TimeRange: tr, Page: page, PageSize: 2, SortBy: "name", SortOrder: "asc"})
			require.NoError(t, err)
			return res, keysOf(rowsOf[RulesUsage](t, res), func(r RulesUsage) string { return r.Name })
		})
	})
	t.Run("GetDashboardUsage", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetDashboardUsage(ctx, DashboardUsageParams{Serie: "up", TimeRange: tr, Page: page, PageSize: 2, SortBy: "name", SortOrder: "asc"})
			require.NoError(t, err)
			return res, keysOf(rowsOf[DashboardUsage](t, res), func(r DashboardUsage) string { return r.Id })
		})
	})
	t.Run("GetSeriesMetadata", func(t *testing.T) {
		assertPages(t, func(page int) (PagedResult, []string) {
			res, err := p.GetSeriesMetadata(ctx, SeriesMetadataParams{Page: page, PageSize: 2, SortBy: "name", SortOrder: "asc", Type: "all"})
			require.NoError(t, err)
			return res, keysOf(rowsOf[models.MetricMetadata](t, res), func(r models.MetricMetadata) string { return r.Name })
		})
	})
}

func TestPostgreSQL_PaginatedListings(t *testing.T) {
	t.Parallel()
	testPaginatedListings(t, postgreSQLTestBackend)
}

func TestSQLite_PaginatedListings(t *testing.T) {
	t.Parallel()
	testPaginatedListings(t, sqliteTestBackend)
}
