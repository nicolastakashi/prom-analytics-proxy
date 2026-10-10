package db

import (
	"context"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEmptyInputsAndDatabase verifies writes of no items succeed without
// writing anything, and reads of an empty database return empty results
// rather than errors.
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
	assert.Empty(t, jobs)

	res, err := p.GetSeriesMetadata(ctx, SeriesMetadataParams{Page: 1, PageSize: 10, Type: "all"})
	require.NoError(t, err, "GetSeriesMetadata")
	assert.Equal(t, [2]int{0, 0}, [2]int{res.Total, res.TotalPages}, "Total, TotalPages")
	assert.Empty(t, rowsOf[models.MetricMetadata](t, res))

	now := time.Now().UTC()
	types, err := p.GetQueryTypes(ctx, TimeRange{From: now.Add(-1 * time.Hour), To: now}, "")
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
