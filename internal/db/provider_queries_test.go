package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGetQueryTypes(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 10)
	for i := range 4 { // 4 instant
		q := instantQuery(now.Add(time.Duration(i)*time.Minute), "up")
		q.Fingerprint = "fp1"
		qs = append(qs, q)
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

func TestPostgreSQL_GetQueryTypes(t *testing.T) {
	t.Parallel()
	testGetQueryTypes(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryTypes(t *testing.T) {
	t.Parallel()
	testGetQueryTypes(t, sqliteTestBackend)
}

func testGetAverageDuration(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	base := time.Date(2025, 8, 20, 12, 0, 0, 0, time.UTC)
	prevFrom := base.Add(-20 * time.Minute)
	curFrom := base.Add(-10 * time.Minute)
	curTo := base

	// Seed previous with avg 10ms, current with avg 20ms. Seeds stay off the
	// window boundaries: backends disagree on whether a query exactly on the
	// shared boundary counts in both windows.
	offBoundary := 30 * time.Second
	var qs []Query
	for i := range 5 {
		q := instantQuery(prevFrom.Add(time.Duration(i)*time.Minute+offBoundary), "up")
		q.Duration = 10 * time.Millisecond
		qs = append(qs, q)
	}
	for i := range 5 {
		q := instantQuery(curFrom.Add(time.Duration(i)*time.Minute+offBoundary), "up")
		q.Duration = 20 * time.Millisecond
		qs = append(qs, q)
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

func TestPostgreSQL_GetAverageDuration(t *testing.T) {
	t.Parallel()
	testGetAverageDuration(t, postgreSQLTestBackend)
}

func TestSQLite_GetAverageDuration(t *testing.T) {
	t.Parallel()
	testGetAverageDuration(t, sqliteTestBackend)
}

func testGetQueryRate(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 5)
	for i := range 5 { // 3 successes, then 2 errors
		q := instantQuery(now.Add(time.Duration(i)*time.Minute), "up")
		q.Fingerprint = "fp1"
		if i >= 3 {
			q.StatusCode = 500
		}
		qs = append(qs, q)
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

func TestPostgreSQL_GetQueryRate(t *testing.T) {
	t.Parallel()
	testGetQueryRate(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryRate(t *testing.T) {
	t.Parallel()
	testGetQueryRate(t, sqliteTestBackend)
}

func testGetQueryLatencyTrendsAndThroughputAndErrors(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 13)
	for i := range 10 {
		q := instantQuery(now.Add(time.Duration(i)*time.Minute), "up")
		q.Duration, q.Fingerprint = time.Duration(5+i)*time.Millisecond, "fp-lat"
		qs = append(qs, q)
	}
	for i := range 3 {
		q := instantQuery(now.Add(time.Duration(i*2)*time.Minute), "up")
		q.Duration, q.StatusCode, q.Fingerprint = 10*time.Millisecond, 500, "fp-lat"
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)

	tr := TimeRange{From: now.Add(-10 * time.Minute), To: now.Add(20 * time.Minute)}

	// One query per minute at 5..14ms; minutes 0, 2 and 4 add a 10ms error.
	lat, err := p.GetQueryLatencyTrends(context.Background(), tr, "up", "fp-lat")
	require.NoError(t, err, "GetQueryLatencyTrends")
	var avgs []float64
	for _, r := range lat {
		if r.Value == 0 {
			continue
		}
		avgs = append(avgs, r.Value)
		if r.Value == 6 {
			assert.Equal(t, 6, r.P95, "a single-query bucket's p95 is its duration")
		}
	}
	assert.ElementsMatch(t, []float64{7.5, 6, 8.5, 8, 9.5, 10, 11, 12, 13, 14}, avgs, "per-minute average latency")

	thr, err := p.GetQueryThroughputAnalysis(context.Background(), tr)
	require.NoError(t, err, "GetQueryThroughputAnalysis")
	var total float64
	for _, r := range thr {
		total += r.Value
	}
	assert.Equal(t, 13.0, total, "throughput counts every query in range")

	errSeries, err := p.GetQueryErrorAnalysis(context.Background(), tr, "fp-lat")
	require.NoError(t, err, "GetQueryErrorAnalysis")
	var errs float64
	for _, r := range errSeries {
		errs += r.Value
	}
	assert.Equal(t, 3.0, errs, "error analysis counts status >= 400")

	dist, err := p.GetQueryStatusDistribution(context.Background(), tr, "fp-lat")
	require.NoError(t, err, "GetQueryStatusDistribution")
	assert.Equal(t, [3]int{10, 0, 3}, statusTotals(dist), "2xx, 4xx, 5xx totals")
}

func TestPostgreSQL_GetQueryLatencyTrends_And_Throughput_And_Errors(t *testing.T) {
	t.Parallel()
	testGetQueryLatencyTrendsAndThroughputAndErrors(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryLatencyTrends_And_Throughput_And_Errors(t *testing.T) {
	t.Parallel()
	testGetQueryLatencyTrendsAndThroughputAndErrors(t, sqliteTestBackend)
}

// testTimeSeriesWideRangeCountsEveryQuery verifies the bucketed
// time series count every query in range when buckets span several minutes,
// not only queries in a bucket's first minute, and step one bucket at a time.
func testTimeSeriesWideRangeCountsEveryQuery(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	from := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	tr := TimeRange{From: from, To: from.Add(6 * time.Hour)} // 5-minute buckets
	var qs []Query
	for i, minute := range []int{1, 2, 3} {
		q := instantQuery(from.Add(time.Duration(minute)*time.Minute), "up")
		q.Duration, q.StatusCode, q.Fingerprint = time.Duration(10*(i+1))*time.Millisecond, 500, "fp"
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)

	thr, err := p.GetQueryThroughputAnalysis(ctx, tr)
	require.NoError(t, err)
	var throughput float64
	for _, r := range thr {
		throughput += r.Value
	}
	assert.Equal(t, 3.0, throughput, "throughput")
	assert.Len(t, thr, 73, "one bucket every 5 minutes across 6 hours, both ends included")

	errs, err := p.GetQueryErrorAnalysis(ctx, tr, "fp")
	require.NoError(t, err)
	var errCount float64
	for _, r := range errs {
		errCount += r.Value
	}
	assert.Equal(t, 3.0, errCount, "error analysis")

	dist, err := p.GetQueryStatusDistribution(ctx, tr, "fp")
	require.NoError(t, err)
	assert.Equal(t, [3]int{0, 0, 3}, statusTotals(dist), "status distribution")

	lat, err := p.GetQueryLatencyTrends(ctx, tr, "up", "fp")
	require.NoError(t, err)
	require.NotEmpty(t, lat)
	assert.Equal(t, 20.0, lat[0].Value, "first bucket averages all three queries")
}

func TestPostgreSQL_TimeSeries_WideRangeCountsEveryQuery(t *testing.T) {
	t.Parallel()
	testTimeSeriesWideRangeCountsEveryQuery(t, postgreSQLTestBackend)
}

func TestSQLite_TimeSeries_WideRangeCountsEveryQuery(t *testing.T) {
	t.Parallel()
	testTimeSeriesWideRangeCountsEveryQuery(t, sqliteTestBackend)
}

func testGetQueriesBySerieName(t *testing.T, b testBackend) {
	p := b.newProvider(t)

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
	require.NoError(t, err, "GetQueriesBySerieName")
	assert.Equal(t, 2, res.Total)
	rows := rowsOf[QueriesBySerieNameResult](t, res)
	require.Len(t, rows, 2)
	assert.Equal(t, "rate(up[5m])", rows[0].Query, "highest average duration first")
	assert.InDelta(t, 30.0, rows[0].AvgDuration, 0.2)
	assert.Equal(t, 300, rows[0].MaxPeakSamples)
	assert.Equal(t, "up", rows[1].Query)
	assert.InDelta(t, 15.0, rows[1].AvgDuration, 0.2)
	assert.InDelta(t, 150.0, rows[1].AvgPeakySamples, 0.2)
	assert.Equal(t, 200, rows[1].MaxPeakSamples)
}

func TestPostgreSQL_GetQueriesBySerieName(t *testing.T) {
	t.Parallel()
	testGetQueriesBySerieName(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueriesBySerieName(t *testing.T) {
	t.Parallel()
	testGetQueriesBySerieName(t, sqliteTestBackend)
}

func testGetQueryExpressionsAndExecutions(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Minute)
	qs := make([]Query, 0, 8)
	for i := range 5 {
		q := instantQuery(now.Add(time.Duration(i)*time.Minute), "up")
		q.Duration, q.PeakSamples, q.Fingerprint = 10*time.Millisecond, 10+i, "fp-a"
		if i == 4 {
			q.QueryParam = "up offset 1m" // latest text for fp-a
		}
		qs = append(qs, q)
	}
	for i := range 3 {
		qs = append(qs, Query{
			TS:                    now.Add(time.Duration(i) * time.Minute),
			QueryParam:            "rate(up[5m])",
			TimeParam:             now,
			Duration:              20 * time.Millisecond,
			StatusCode:            500,
			LabelMatchers:         LabelMatchers{{"__name__": "up"}},
			Type:                  QueryTypeRange,
			Start:                 now.Add(-5 * time.Minute),
			End:                   now,
			Step:                  15,
			PeakSamples:           100,
			TotalQueryableSamples: 1000 + i,
			Fingerprint:           "fp-b",
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
	require.NoError(t, err, "GetQueryExpressions")
	assert.Equal(t, []QueryExpression{
		{Fingerprint: "fp-a", Query: "up offset 1m", Executions: 5, AvgDuration: 10, ErrorRatePercent: 0, PeakSamples: 14},
		{Fingerprint: "fp-b", Query: "rate(up[5m])", Executions: 3, AvgDuration: 20, ErrorRatePercent: 100, PeakSamples: 100},
	}, rowsOf[QueryExpression](t, pr))

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
		require.NoError(t, err, "GetQueryExecutions")
		erows := rowsOf[QueryExecutionRow](t, per)
		require.Len(t, erows, 3)
		var samples []int
		for _, r := range erows {
			assert.Equal(t, [2]int{500, 20}, [2]int{r.Status, int(r.Duration)}, "status, duration")
			assert.Equal(t, string(QueryTypeRange), r.Type)
			assert.Equal(t, 15.0, r.Steps)
			samples = append(samples, r.Samples)
		}
		if tc.sortOrder == "asc" {
			assert.Equal(t, []int{1000, 1001, 1002}, samples, "rows in ts order, samples from TotalQueryableSamples")
		} else {
			assert.Equal(t, []int{1002, 1001, 1000}, samples, "rows in reverse ts order")
		}
	}
}

func TestPostgreSQL_GetQueryExpressions_And_Executions(t *testing.T) {
	t.Parallel()
	testGetQueryExpressionsAndExecutions(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryExpressions_And_Executions(t *testing.T) {
	t.Parallel()
	testGetQueryExpressionsAndExecutions(t, sqliteTestBackend)
}

// testQueryTimeRangeDistribution verifies bucketed counts and percents for
// range queries.
func testQueryTimeRangeDistribution(t *testing.T, b testBackend) {
	p := b.newProvider(t)

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

	// Instant queries are excluded from the distribution.
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

	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: now.Add(-24 * time.Hour), To: now}, "")
	assert.NoError(t, err, "GetQueryTimeRangeDistribution")

	got := map[string]int{}
	total := 0
	for _, b := range out {
		got[b.Label] = b.Count
		total += b.Count
	}

	assert.Equal(t, 13, total, "unexpected total count")

	assert.Equal(t, 5, got["<24h"], "unexpected bucket count for <24h")
	assert.Equal(t, 3, got["24h"], "unexpected bucket count for 24h")
	assert.Equal(t, 2, got["7d"], "unexpected bucket count for 7d")
	assert.Equal(t, 1, got["30d"], "unexpected bucket count for 30d")
	assert.Equal(t, 1, got["60d"], "unexpected bucket count for 60d")
	assert.Equal(t, 1, got["90d+"], "unexpected bucket count for 90d+")

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

func TestPostgreSQL_QueryTimeRangeDistribution(t *testing.T) {
	t.Parallel()
	testQueryTimeRangeDistribution(t, postgreSQLTestBackend)
}

func TestSQLite_QueryTimeRangeDistribution(t *testing.T) {
	t.Parallel()
	testQueryTimeRangeDistribution(t, sqliteTestBackend)
}

// testDeleteQueriesBefore verifies retention deletes exactly the queries
// older than the cutoff, keeps one at the cutoff, and that repeating it
// deletes nothing. Seeds go
// through Insert, so ts has the stored format, and old and new rows share a
// calendar day, so a date-only comparison fails.
func testDeleteQueriesBefore(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-1 * time.Hour)

	var qs []Query
	for i := range 3 {
		qs = append(qs, instantQuery(cutoff.Add(-time.Duration(i+1)*time.Hour), "old"))
	}
	for i := range 3 {
		qs = append(qs, instantQuery(cutoff.Add(time.Duration(i)*time.Hour), "new"))
	}
	mustInsertQueries(t, p, qs)

	deleted, err := p.DeleteQueriesBefore(ctx, cutoff)
	require.NoError(t, err, "DeleteQueriesBefore")
	assert.Equal(t, int64(3), deleted)

	var remaining []string
	rows, err := rawDB(p).QueryContext(ctx, `SELECT queryParam FROM queries`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var param string
		require.NoError(t, rows.Scan(&param))
		remaining = append(remaining, param)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"new", "new", "new"}, remaining, "only queries at or after the cutoff remain")

	deleted, err = p.DeleteQueriesBefore(ctx, cutoff)
	require.NoError(t, err, "DeleteQueriesBefore second call")
	assert.Equal(t, int64(0), deleted)
}

func TestPostgreSQL_DeleteQueriesBefore(t *testing.T) {
	t.Parallel()
	testDeleteQueriesBefore(t, postgreSQLTestBackend)
}

func TestSQLite_DeleteQueriesBefore(t *testing.T) {
	t.Parallel()
	testDeleteQueriesBefore(t, sqliteTestBackend)
}

// testQueryFiltersScopeResults verifies fingerprint, metric and text filters,
// and the range's upper bound, scope every query read.
func testQueryFiltersScopeResults(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	tr := TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}
	instant := func(ts time.Time, param, metric, fp string) Query {
		q := instantQuery(ts, metric)
		q.QueryParam, q.Fingerprint = param, fp
		return q
	}
	qs := []Query{
		instant(now.Add(-10*time.Minute), "up", "up", "fpA"),
		instant(now.Add(-9*time.Minute), "up", "up", "fpA"),
		instant(now.Add(3*time.Hour), "up", "up", "fpA"), // after the range
		instant(now.Add(-8*time.Minute), "sum(up)", "up", "fpC"),
	}
	for i := range 3 {
		qs = append(qs, Query{
			TS: now.Add(time.Duration(-7+i) * time.Minute), QueryParam: "rate(other[5m])", TimeParam: now,
			Duration: 40 * time.Millisecond, StatusCode: 200, LabelMatchers: LabelMatchers{{"__name__": "other"}},
			Type: QueryTypeRange, Start: now.Add(-5 * time.Minute), End: now, Step: 15, Fingerprint: "fpB",
		})
	}
	mustInsertQueries(t, p, qs)

	types, err := p.GetQueryTypes(ctx, tr, "fpA")
	require.NoError(t, err)
	assert.Equal(t, 2, *types.TotalQueries, "fpA queries inside the range")
	assert.InDelta(t, 100.0, *types.InstantPercent, 0.2)

	avg, err := p.GetAverageDuration(ctx, tr, "fpB")
	require.NoError(t, err)
	assert.InDelta(t, 40.0, *avg.AvgDuration, 0.2)

	rate, err := p.GetQueryRate(ctx, tr, "up", "")
	require.NoError(t, err)
	assert.Equal(t, 3, *rate.SuccessTotal, "queries for metric up inside the range")

	dist, err := p.GetQueryStatusDistribution(ctx, tr, "fpA")
	require.NoError(t, err)
	assert.Equal(t, [3]int{2, 0, 0}, statusTotals(dist), "fpA's 2xx, 4xx, 5xx")

	for fp, want := range map[string]int{"fpA": 0, "fpB": 3} {
		ranges, err := p.GetQueryTimeRangeDistribution(ctx, tr, fp)
		require.NoError(t, err)
		var n int
		for _, r := range ranges {
			n += r.Count
		}
		assert.Equal(t, want, n, "range queries of %s", fp)
	}

	exprs, err := p.GetQueryExpressions(ctx, QueryExpressionsParams{TimeRange: tr, Page: 1, PageSize: 10, Filter: "other"})
	require.NoError(t, err)
	assert.Equal(t, []string{"fpB"}, fingerprints(t, exprs), "expressions matching the text filter")

	for typ, want := range map[QueryType]int{QueryTypeInstant: 0, QueryTypeRange: 3} {
		execs, err := p.GetQueryExecutions(ctx, QueryExecutionsParams{Fingerprint: "fpB", Type: string(typ), TimeRange: tr, Page: 1, PageSize: 10})
		require.NoError(t, err)
		assert.Equal(t, want, execs.Total, "%s executions of fpB", typ)
	}

	bySerie, err := p.GetQueriesBySerieName(ctx, QueriesBySerieNameParams{SerieName: "up", TimeRange: tr, Page: 1, PageSize: 10, Filter: "sum"})
	require.NoError(t, err)
	rows := rowsOf[QueriesBySerieNameResult](t, bySerie)
	require.Len(t, rows, 1)
	assert.Equal(t, "sum(up)", rows[0].Query)

	perf, err := p.GetMetricQueryPerformanceStatistics(ctx, "other", tr)
	require.NoError(t, err)
	assert.Equal(t, 3, *perf.TotalQueries)
}

func TestPostgreSQL_QueryFiltersScopeResults(t *testing.T) {
	t.Parallel()
	testQueryFiltersScopeResults(t, postgreSQLTestBackend)
}

func TestSQLite_QueryFiltersScopeResults(t *testing.T) {
	t.Parallel()
	testQueryFiltersScopeResults(t, sqliteTestBackend)
}

// testQueryStatusClasses verifies 2xx counts as success, 4xx and 5xx as
// errors, and other classes as neither.
func testQueryStatusClasses(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	tr := TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}
	var qs []Query
	for i, status := range []int{200, 304, 404, 500} {
		q := instantQuery(now.Add(time.Duration(i-10)*time.Minute), "up")
		q.StatusCode, q.Fingerprint = status, "fp"
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)

	rate, err := p.GetQueryRate(ctx, tr, "", "fp")
	require.NoError(t, err)
	assert.Equal(t, [2]int{1, 2}, [2]int{*rate.SuccessTotal, *rate.ErrorTotal}, "success, error totals")

	dist, err := p.GetQueryStatusDistribution(ctx, tr, "fp")
	require.NoError(t, err)
	assert.Equal(t, [3]int{1, 1, 1}, statusTotals(dist), "2xx, 4xx, 5xx totals")

	errs, err := p.GetQueryErrorAnalysis(ctx, tr, "fp")
	require.NoError(t, err)
	var n float64
	for _, r := range errs {
		n += r.Value
	}
	assert.Equal(t, 2.0, n)
}

func TestPostgreSQL_QueryStatusClasses(t *testing.T) {
	t.Parallel()
	testQueryStatusClasses(t, postgreSQLTestBackend)
}

func TestSQLite_QueryStatusClasses(t *testing.T) {
	t.Parallel()
	testQueryStatusClasses(t, sqliteTestBackend)
}

// testGetQueryLatencyTrendsP95IsUpperTail verifies p95 reports the upper tail
// of a bucket rather than its middle. Backends compute it differently
// (interpolated or nearest rank), so only the side of the median is pinned.
func testGetQueryLatencyTrendsP95IsUpperTail(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Minute)
	var qs []Query
	for i := range 10 { // 10..100ms in one minute; median 55
		q := instantQuery(now.Add(time.Duration(i)*time.Second), "up")
		q.Duration = time.Duration(10*(i+1)) * time.Millisecond
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)

	lat, err := p.GetQueryLatencyTrends(context.Background(), TimeRange{From: now, To: now.Add(30 * time.Minute)}, "", "")
	require.NoError(t, err)
	require.NotEmpty(t, lat)
	assert.GreaterOrEqual(t, lat[0].P95, 90)
}

func TestPostgreSQL_GetQueryLatencyTrends_P95IsUpperTail(t *testing.T) {
	t.Parallel()
	testGetQueryLatencyTrendsP95IsUpperTail(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryLatencyTrends_P95IsUpperTail(t *testing.T) {
	t.Parallel()
	testGetQueryLatencyTrendsP95IsUpperTail(t, sqliteTestBackend)
}

// fingerprints returns the fingerprints res lists, in order.
func fingerprints(t *testing.T, res PagedResult) []string {
	t.Helper()
	return keysOf(rowsOf[QueryExpression](t, res), func(e QueryExpression) string { return e.Fingerprint })
}

// testGetQueryTimeRangeDistributionDayBoundary verifies a range of exactly
// 24h falls in the "24h" bucket, not "<24h".
func testGetQueryTimeRangeDistributionDayBoundary(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Second)
	mustInsertQueries(t, p, []Query{{
		TS: now.Add(-5 * time.Minute), QueryParam: "up", TimeParam: now, Duration: time.Millisecond, StatusCode: 200,
		LabelMatchers: LabelMatchers{{"__name__": "up"}}, Type: QueryTypeRange, Start: now.Add(-24 * time.Hour), End: now, Step: 15,
	}})

	out, err := p.GetQueryTimeRangeDistribution(context.Background(), TimeRange{From: now.Add(-1 * time.Hour), To: now}, "")
	require.NoError(t, err)
	got := map[string]int{}
	for _, r := range out {
		got[r.Label] = r.Count
	}
	assert.Equal(t, [2]int{0, 1}, [2]int{got["<24h"], got["24h"]}, "<24h, 24h")
}

func TestPostgreSQL_GetQueryTimeRangeDistribution_DayBoundary(t *testing.T) {
	t.Parallel()
	testGetQueryTimeRangeDistributionDayBoundary(t, postgreSQLTestBackend)
}

func TestSQLite_GetQueryTimeRangeDistribution_DayBoundary(t *testing.T) {
	t.Parallel()
	testGetQueryTimeRangeDistributionDayBoundary(t, sqliteTestBackend)
}

// testInsertHTTPHeadersRoundTrip verifies a query's HTTP headers are stored
// and returned with its executions.
func testInsertHTTPHeadersRoundTrip(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	now := time.Now().UTC().Truncate(time.Second)
	headers := map[string]string{"X-Grafana-User": "alice", "X-Dashboard-Uid": "d1"}
	q := instantQuery(now.Add(-5*time.Minute), "up")
	q.Fingerprint, q.HTTPHeaders = "fp", headers
	mustInsertQueries(t, p, []Query{q})

	res, err := p.GetQueryExecutions(context.Background(), QueryExecutionsParams{
		Fingerprint: "fp", TimeRange: TimeRange{From: now.Add(-1 * time.Hour), To: now}, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	rows := rowsOf[QueryExecutionRow](t, res)
	require.Len(t, rows, 1)
	assert.Equal(t, headers, rows[0].HTTPHeaders)
}

func TestPostgreSQL_Insert_HTTPHeadersRoundTrip(t *testing.T) {
	t.Parallel()
	testInsertHTTPHeadersRoundTrip(t, postgreSQLTestBackend)
}

func TestSQLite_Insert_HTTPHeadersRoundTrip(t *testing.T) {
	t.Parallel()
	testInsertHTTPHeadersRoundTrip(t, sqliteTestBackend)
}
