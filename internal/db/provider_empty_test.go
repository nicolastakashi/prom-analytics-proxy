package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEmptyInputsAndDatabase verifies writes of no items succeed without
// writing anything, and reads of an empty database return empty results
// rather than errors: listings as an empty list, not nil, so they encode as
// [] rather than null.
func testEmptyInputsAndDatabase(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	require.NoError(t, p.Insert(ctx, nil), "Insert")
	require.NoError(t, p.InsertRulesUsage(ctx, nil), "InsertRulesUsage")
	require.NoError(t, p.InsertDashboardUsage(ctx, nil), "InsertDashboardUsage")
	require.NoError(t, p.UpsertMetricsCatalog(ctx, nil), "UpsertMetricsCatalog")
	require.NoError(t, p.UpsertMetricsJobIndex(ctx, nil), "UpsertMetricsJobIndex")

	for _, table := range []string{"queries", "RulesUsage", "DashboardUsage", "metrics_catalog", "metrics_job_index"} {
		var n int
		require.NoError(t, rawDB(p).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n))
		assert.Zero(t, n, table)
	}

	byName, err := p.GetSeriesMetadataByNames(ctx, nil, "")
	require.NoError(t, err, "GetSeriesMetadataByNames")
	assert.Empty(t, byName)

	jobs, err := p.ListJobs(ctx)
	require.NoError(t, err, "ListJobs")
	assert.Equal(t, []string{}, jobs)

	res, err := p.GetSeriesMetadata(ctx, SeriesMetadataParams{Page: 1, PageSize: 10, Type: "all"})
	require.NoError(t, err, "GetSeriesMetadata")
	assert.Equal(t, [2]int{0, 0}, [2]int{res.Total, res.TotalPages}, "Total, TotalPages")
	assert.Equal(t, []models.MetricMetadata{}, rowsOf[models.MetricMetadata](t, res))

	byNames, err := p.GetSeriesMetadataByNames(ctx, []string{"absent"}, "")
	require.NoError(t, err, "GetSeriesMetadataByNames")
	assert.Equal(t, []models.MetricMetadata{}, byNames)

	now := time.Now().UTC()
	tr := mustTimeRange(t, now.Add(-1*time.Hour), now)
	listings := map[string]func() (any, error){
		"GetQueriesBySerieName": func() (any, error) {
			res, err := p.GetQueriesBySerieName(ctx, QueriesBySerieNameParams{SerieName: "up", TimeRange: tr, Page: 1, PageSize: 10})
			return res.Data, err
		},
		"GetRulesUsage": func() (any, error) {
			res, err := p.GetRulesUsage(ctx, RulesUsageParams{Serie: "up", Kind: "alert", TimeRange: tr, Page: 1, PageSize: 10})
			return res.Data, err
		},
		"GetDashboardUsage": func() (any, error) {
			res, err := p.GetDashboardUsage(ctx, DashboardUsageParams{Serie: "up", TimeRange: tr, Page: 1, PageSize: 10})
			return res.Data, err
		},
		"GetQueryExpressions": func() (any, error) {
			res, err := p.GetQueryExpressions(ctx, QueryExpressionsParams{TimeRange: tr, Page: 1, PageSize: 10})
			return res.Data, err
		},
		"GetQueryExecutions": func() (any, error) {
			res, err := p.GetQueryExecutions(ctx, QueryExecutionsParams{Fingerprint: "fp", TimeRange: tr, Page: 1, PageSize: 10})
			return res.Data, err
		},
	}
	for name, list := range listings {
		data, err := list()
		require.NoError(t, err, name)
		v := reflect.ValueOf(data)
		require.Equal(t, reflect.Slice, v.Kind(), "%s returns a list", name)
		assert.False(t, v.IsNil(), "%s returns an empty list, not nil", name)
		assert.Zero(t, v.Len(), name)
	}

	types, err := p.GetQueryTypes(ctx, tr, "")
	require.NoError(t, err, "GetQueryTypes")
	require.NotNil(t, types.TotalQueries)
	assert.Zero(t, *types.TotalQueries)
}

func TestPostgreSQL_EmptyInputsAndDatabase(t *testing.T) {
	t.Parallel()
	testEmptyInputsAndDatabase(t, postgreSQLTestBackend)
}

func TestSQLite_EmptyInputsAndDatabase(t *testing.T) {
	t.Parallel()
	testEmptyInputsAndDatabase(t, sqliteTestBackend)
}
