package db

import (
	"context"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sqliteTestBackend = testBackend{
	newProvider: newTestSQLiteProvider,
	rebind:      func(query string) string { return query },
}

// newTestSQLiteProvider returns a migrated Provider, closed when t ends.
// Tests stay isolated, and so can run in parallel, only while the configured
// SQLite path is empty: each provider then gets a private temporary database.
func newTestSQLiteProvider(t testing.TB) Provider {
	t.Helper()
	require.Empty(t, config.DefaultConfig.Database.SQLite.DatabasePath,
		"a configured SQLite path would make every test share one database")
	p, err := newSqliteProvider(context.Background())
	require.NoError(t, err, "init sqlite provider")
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestSQLite_GetQueryLatencyTrends_P95PerBucket verifies p95 is the
// nearest-rank 95th percentile of each bucket's own durations: not the
// bucket's maximum, and not ranked across the whole range.
func TestSQLite_GetQueryLatencyTrends_P95PerBucket(t *testing.T) {
	t.Parallel()
	p := newTestSQLiteProvider(t)

	ctx := context.Background()
	from := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	query := func(minute int, d time.Duration) Query {
		return Query{
			TS: from.Add(time.Duration(minute) * time.Minute), QueryParam: "up", TimeParam: from, Duration: d,
			StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeInstant,
		}
	}
	// Minute 0: 19 queries at 10ms and one at 100ms; minute 1: one at 20ms.
	var qs []Query
	for range 19 {
		qs = append(qs, query(0, 10*time.Millisecond))
	}
	qs = append(qs, query(0, 100*time.Millisecond), query(1, 20*time.Millisecond))
	mustInsertQueries(t, p, qs)

	// 1-minute buckets.
	lat, err := p.GetQueryLatencyTrends(ctx, mustTimeRange(t, from, from.Add(30*time.Minute)), "", "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(lat), 2)
	assert.Equal(t, 10, lat[0].P95, "minute 0: the 19th of 20 durations")
	assert.Equal(t, 20, lat[1].P95, "minute 1: its only duration")

	// 5-minute buckets: all 21 queries share the first one.
	lat, err = p.GetQueryLatencyTrends(ctx, mustTimeRange(t, from, from.Add(6*time.Hour)), "", "")
	require.NoError(t, err)
	require.NotEmpty(t, lat)
	assert.Equal(t, 20, lat[0].P95, "the 20th of 21 durations")

	// A day later, one bucket where minute 0 holds a single 100ms query and
	// minute 1 holds 39 at 10ms: the bucket's p95 is 10ms, while ranking each
	// minute on its own would report 100ms.
	day := from.Add(24 * time.Hour)
	qs = []Query{{
		TS: day, QueryParam: "up", TimeParam: day, Duration: 100 * time.Millisecond,
		StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeInstant,
	}}
	for range 39 {
		q := qs[0]
		q.TS, q.Duration = day.Add(time.Minute), 10*time.Millisecond
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)
	lat, err = p.GetQueryLatencyTrends(ctx, mustTimeRange(t, day, day.Add(6*time.Hour)), "", "")
	require.NoError(t, err)
	require.NotEmpty(t, lat)
	assert.Equal(t, 10, lat[0].P95, "the 38th of 40 durations, ranked across the whole bucket")
}
