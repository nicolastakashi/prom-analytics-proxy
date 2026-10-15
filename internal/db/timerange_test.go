package db

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTimeRange(t *testing.T) {
	t.Parallel()
	from := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	to := time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC)

	tr, err := NewTimeRange(from, to)
	require.NoError(t, err)
	assert.Equal(t, [2]time.Time{from, to}, [2]time.Time{tr.From(), tr.To()})
	assert.False(t, tr.IsZero())

	reversed, err := NewTimeRange(to, from)
	require.NoError(t, err, "a reversed range is accepted")
	assert.Equal(t, [2]time.Time{to, from}, [2]time.Time{reversed.From(), reversed.To()})

	for name, bounds := range map[string][2]time.Time{
		"no from": {{}, to},
		"no to":   {from, {}},
		"neither": {{}, {}},
	} {
		_, err := NewTimeRange(bounds[0], bounds[1])
		assert.ErrorIs(t, err, ErrTimeRangeRequired, name)
	}
	assert.True(t, TimeRange{}.IsZero())
}

func TestTimeRangeUntilNow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := TimeRangeUntilNow(time.Hour)
		now := time.Now().UTC()
		assert.Equal(t, [2]time.Time{now.Add(-time.Hour), now}, [2]time.Time{tr.From(), tr.To()})
	})
}

func TestTimeRange_Format(t *testing.T) {
	t.Parallel()
	tr := mustTimeRange(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC))

	fromStr, toStr := tr.Format(time.RFC3339)

	assert.Equal(t, "2023-01-01T12:00:00Z", fromStr)
	assert.Equal(t, "2023-01-15T12:00:00Z", toStr)
}

func TestTimeRange_Previous(t *testing.T) {
	t.Parallel()
	tr := mustTimeRange(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC))

	previous := tr.Previous()

	assert.Equal(t, time.Date(2022, 12, 18, 12, 0, 0, 0, time.UTC), previous.From())
	assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), previous.To())
}

// testRangeMethodsRequireTimeRange verifies every range method rejects the
// zero TimeRange with ErrTimeRangeRequired.
func testRangeMethodsRequireTimeRange(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	var none TimeRange
	for name, read := range map[string]func() error{
		"RefreshMetricsUsageSummary": func() error { return p.RefreshMetricsUsageSummary(ctx, none) },
		"GetQueryTypes":              func() error { _, err := p.GetQueryTypes(ctx, none, ""); return err },
		"GetAverageDuration":         func() error { _, err := p.GetAverageDuration(ctx, none, ""); return err },
		"GetQueryRate":               func() error { _, err := p.GetQueryRate(ctx, none, "", ""); return err },
		"GetQueryStatusDistribution": func() error { _, err := p.GetQueryStatusDistribution(ctx, none, ""); return err },
		"GetQueryLatencyTrends":      func() error { _, err := p.GetQueryLatencyTrends(ctx, none, "", ""); return err },
		"GetQueryThroughputAnalysis": func() error { _, err := p.GetQueryThroughputAnalysis(ctx, none); return err },
		"GetQueryErrorAnalysis":      func() error { _, err := p.GetQueryErrorAnalysis(ctx, none, ""); return err },
		"GetQueryTimeRangeDistribution": func() error {
			_, err := p.GetQueryTimeRangeDistribution(ctx, none, "")
			return err
		},
		"GetMetricStatistics": func() error { _, err := p.GetMetricStatistics(ctx, "up", none); return err },
		"GetMetricQueryPerformanceStatistics": func() error {
			_, err := p.GetMetricQueryPerformanceStatistics(ctx, "up", none)
			return err
		},
		"GetQueriesBySerieName": func() error {
			_, err := p.GetQueriesBySerieName(ctx, QueriesBySerieNameParams{SerieName: "up", Page: 1, PageSize: 10})
			return err
		},
		"GetRulesUsage": func() error {
			_, err := p.GetRulesUsage(ctx, RulesUsageParams{Serie: "up", Kind: "alert", Page: 1, PageSize: 10})
			return err
		},
		"GetDashboardUsage": func() error {
			_, err := p.GetDashboardUsage(ctx, DashboardUsageParams{Serie: "up", Page: 1, PageSize: 10})
			return err
		},
		"GetQueryExpressions": func() error {
			_, err := p.GetQueryExpressions(ctx, QueryExpressionsParams{Page: 1, PageSize: 10})
			return err
		},
		"GetQueryExecutions": func() error {
			_, err := p.GetQueryExecutions(ctx, QueryExecutionsParams{Fingerprint: "fp", Page: 1, PageSize: 10})
			return err
		},
	} {
		assert.ErrorIs(t, read(), ErrTimeRangeRequired, name)
	}
}

func TestPostgreSQL_RangeMethodsRequireTimeRange(t *testing.T) {
	t.Parallel()
	testRangeMethodsRequireTimeRange(t, postgreSQLTestBackend)
}

func TestSQLite_RangeMethodsRequireTimeRange(t *testing.T) {
	t.Parallel()
	testRangeMethodsRequireTimeRange(t, sqliteTestBackend)
}
