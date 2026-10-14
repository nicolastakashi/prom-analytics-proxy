package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seriesPoint is one bucket of a time series: its label and its value.
type seriesPoint struct {
	time  string
	value float64
}

// timeSeries reads each bucketed time series as seriesPoints. Every value is a
// count of queries, except latency's, which is the bucket's average duration.
var timeSeries = []struct {
	name     string
	duration bool // value is an average duration rather than a count
	read     func(testing.TB, Provider, TimeRange) []seriesPoint
}{
	{"Throughput", false, func(t testing.TB, p Provider, tr TimeRange) []seriesPoint {
		rows, err := p.GetQueryThroughputAnalysis(context.Background(), tr)
		require.NoError(t, err, "Throughput")
		return pointsOf(rows, func(r QueryThroughputAnalysisResult) seriesPoint { return seriesPoint{r.Time, r.Value} })
	}},
	{"ErrorAnalysis", false, func(t testing.TB, p Provider, tr TimeRange) []seriesPoint {
		rows, err := p.GetQueryErrorAnalysis(context.Background(), tr, "")
		require.NoError(t, err, "ErrorAnalysis")
		return pointsOf(rows, func(r QueryErrorAnalysisResult) seriesPoint { return seriesPoint{r.Time, r.Value} })
	}},
	{"StatusDistribution", false, func(t testing.TB, p Provider, tr TimeRange) []seriesPoint {
		rows, err := p.GetQueryStatusDistribution(context.Background(), tr, "")
		require.NoError(t, err, "StatusDistribution")
		return pointsOf(rows, func(r QueryStatusDistributionResult) seriesPoint {
			return seriesPoint{r.Time, float64(r.Status2xx + r.Status4xx + r.Status5xx)}
		})
	}},
	{"LatencyTrends", true, func(t testing.TB, p Provider, tr TimeRange) []seriesPoint {
		rows, err := p.GetQueryLatencyTrends(context.Background(), tr, "", "")
		require.NoError(t, err, "LatencyTrends")
		return pointsOf(rows, func(r QueryLatencyTrendsResult) seriesPoint { return seriesPoint{r.Time, r.Value} })
	}},
}

func pointsOf[T any](rows []T, point func(T) seriesPoint) []seriesPoint {
	out := make([]seriesPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, point(r))
	}
	return out
}

// nonZero returns the label and value of every bucket whose value is not 0.
func nonZero(points []seriesPoint) map[string]float64 {
	out := map[string]float64{}
	for _, p := range points {
		if p.value != 0 {
			out[p.time] = p.value
		}
	}
	return out
}

// testTimeSeriesPlaceEachQueryInItsBucket verifies each time series labels
// its buckets with their start and counts a query in the bucket containing
// it: on the range start, on a bucket start, and in a bucket's last
// millisecond.
func testTimeSeriesPlaceEachQueryInItsBucket(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	from := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	tr := mustTimeRange(t, from, from.Add(time.Hour)) // 1-minute buckets
	var qs []Query
	for _, s := range []struct {
		at       time.Duration
		duration time.Duration
	}{{0, 10 * time.Millisecond}, {15 * time.Minute, 10 * time.Millisecond}, {16*time.Minute - time.Millisecond, 30 * time.Millisecond}} {
		q := instantQuery(from.Add(s.at), "up")
		q.StatusCode, q.Duration = 500, s.duration
		qs = append(qs, q)
	}
	mustInsertQueries(t, p, qs)

	first, bucket := b.bucketTime(from), b.bucketTime(from.Add(15*time.Minute))
	want := map[string]map[string]float64{
		"Throughput":         {first: 1, bucket: 2},
		"ErrorAnalysis":      {first: 1, bucket: 2},
		"StatusDistribution": {first: 1, bucket: 2},
		"LatencyTrends":      {first: 10, bucket: 20}, // average duration
	}
	for _, s := range timeSeries {
		points := s.read(t, p, tr)
		require.Len(t, points, 61, s.name)
		assert.Equal(t, [2]string{first, b.bucketTime(from.Add(time.Hour))}, [2]string{points[0].time, points[60].time}, "%s first and last bucket", s.name)
		assert.Equal(t, want[s.name], nonZero(points), s.name)
	}
}

func TestPostgreSQL_TimeSeries_PlaceEachQueryInItsBucket(t *testing.T) {
	t.Parallel()
	testTimeSeriesPlaceEachQueryInItsBucket(t, postgreSQLTestBackend)
}

func TestSQLite_TimeSeries_PlaceEachQueryInItsBucket(t *testing.T) {
	t.Parallel()
	testTimeSeriesPlaceEachQueryInItsBucket(t, sqliteTestBackend)
}

// testTimeSeriesBucketsStartAtRangeStartMinute verifies each time series
// starts its buckets at the range start truncated to the minute, not at a
// wall-clock multiple of the bucket width, and counts a query past its
// bucket's first minute.
func testTimeSeriesBucketsStartAtRangeStartMinute(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	from := time.Date(2026, 1, 2, 10, 7, 30, 0, time.UTC)
	q := instantQuery(time.Date(2026, 1, 2, 10, 15, 30, 0, time.UTC), "up")
	q.StatusCode, q.Duration = 500, 10*time.Millisecond
	mustInsertQueries(t, p, []Query{q})

	tr := mustTimeRange(t, from, from.Add(12*time.Hour)) // 15-minute buckets
	first := b.bucketTime(time.Date(2026, 1, 2, 10, 7, 0, 0, time.UTC))
	for _, s := range timeSeries {
		points := s.read(t, p, tr)
		require.Len(t, points, 49, s.name)
		assert.Equal(t, [2]string{first, b.bucketTime(time.Date(2026, 1, 2, 10, 22, 0, 0, time.UTC))},
			[2]string{points[0].time, points[1].time}, "%s first two buckets", s.name)
		want := 1.0
		if s.duration {
			want = 10
		}
		assert.Equal(t, map[string]float64{first: want}, nonZero(points), s.name)
	}
}

func TestPostgreSQL_TimeSeries_BucketsStartAtRangeStartMinute(t *testing.T) {
	t.Parallel()
	testTimeSeriesBucketsStartAtRangeStartMinute(t, postgreSQLTestBackend)
}

func TestSQLite_TimeSeries_BucketsStartAtRangeStartMinute(t *testing.T) {
	t.Parallel()
	testTimeSeriesBucketsStartAtRangeStartMinute(t, sqliteTestBackend)
}

// testTimeSeriesBucketWidthFollowsRangeSpan verifies each time series' bucket
// width on and past every range span threshold, with both range ends getting a
// bucket, and that a range with no queries yields every bucket with value 0.
func testTimeSeriesBucketWidthFollowsRangeSpan(t *testing.T, b testBackend) {
	p := b.newProvider(t)

	from := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	for _, tier := range []struct {
		span, width time.Duration
		buckets     int
	}{
		// On each span threshold, then one next-tier width past it.
		{2 * time.Hour, time.Minute, 121},
		{2*time.Hour + 5*time.Minute, 5 * time.Minute, 26},
		{6 * time.Hour, 5 * time.Minute, 73},
		{6*time.Hour + 15*time.Minute, 15 * time.Minute, 26},
		{24 * time.Hour, 15 * time.Minute, 97},
		{25 * time.Hour, time.Hour, 26},
		{7 * 24 * time.Hour, time.Hour, 169},
		{7*24*time.Hour + 6*time.Hour, 6 * time.Hour, 30},
		{30 * 24 * time.Hour, 6 * time.Hour, 121},
		{31 * 24 * time.Hour, 24 * time.Hour, 32},
		{100 * 24 * time.Hour, 24 * time.Hour, 101},
	} {
		t.Run(tier.span.String(), func(t *testing.T) {
			t.Parallel()
			for _, s := range timeSeries {
				points := s.read(t, p, mustTimeRange(t, from, from.Add(tier.span)))
				require.Len(t, points, tier.buckets, s.name)
				assert.Equal(t, [2]string{b.bucketTime(from), b.bucketTime(from.Add(tier.width))},
					[2]string{points[0].time, points[1].time}, "%s first two buckets", s.name)
				assert.Empty(t, nonZero(points), s.name)
			}
		})
	}
}

func TestPostgreSQL_TimeSeries_BucketWidthFollowsRangeSpan(t *testing.T) {
	t.Parallel()
	testTimeSeriesBucketWidthFollowsRangeSpan(t, postgreSQLTestBackend)
}

func TestSQLite_TimeSeries_BucketWidthFollowsRangeSpan(t *testing.T) {
	t.Parallel()
	testTimeSeriesBucketWidthFollowsRangeSpan(t, sqliteTestBackend)
}

type rangeMethod struct {
	name  string
	count func(testing.TB, Provider, TimeRange) int
}

// rangeMethods counts, for each read filtering queries by TimeRange, how many
// queries seeded by rangeBoundsQuery it reports in tr. A time series counts
// its values summed over buckets or, for a duration, its non-empty buckets.
var rangeMethods = func() []rangeMethod {
	var out []rangeMethod
	for _, s := range timeSeries {
		out = append(out, rangeMethod{s.name, func(t testing.TB, p Provider, tr TimeRange) int {
			n := 0
			for _, v := range nonZero(s.read(t, p, tr)) {
				if s.duration {
					v = 1
				}
				n += int(v)
			}
			return n
		}})
	}
	ctx := context.Background()
	return append(out,
		rangeMethod{"TimeRangeDistribution", func(t testing.TB, p Provider, tr TimeRange) int {
			rows, err := p.GetQueryTimeRangeDistribution(ctx, tr, "")
			require.NoError(t, err, "TimeRangeDistribution")
			n := 0
			for _, r := range rows {
				n += r.Count
			}
			return n
		}},
		rangeMethod{"Executions", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetQueryExecutions(ctx, QueryExecutionsParams{Fingerprint: "fp", TimeRange: tr, Page: 1, PageSize: 10})
			require.NoError(t, err, "Executions")
			return res.Total
		}},
		rangeMethod{"QueryTypes", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetQueryTypes(ctx, tr, "")
			require.NoError(t, err, "QueryTypes")
			return intOrZero(res.TotalQueries)
		}},
		rangeMethod{"AverageDuration", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetAverageDuration(ctx, tr, "")
			require.NoError(t, err, "AverageDuration")
			if res.AvgDuration == nil || *res.AvgDuration == 0 {
				return 0
			}
			return 1
		}},
		rangeMethod{"QueryRate", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetQueryRate(ctx, tr, "", "")
			require.NoError(t, err, "QueryRate")
			return intOrZero(res.ErrorTotal)
		}},
		rangeMethod{"QueriesBySerieName", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetQueriesBySerieName(ctx, QueriesBySerieNameParams{SerieName: "up", TimeRange: tr, Page: 1, PageSize: 10})
			require.NoError(t, err, "QueriesBySerieName")
			return res.Total
		}},
		rangeMethod{"QueryExpressions", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetQueryExpressions(ctx, QueryExpressionsParams{TimeRange: tr, Page: 1, PageSize: 10})
			require.NoError(t, err, "QueryExpressions")
			return res.Total
		}},
		rangeMethod{"MetricQueryPerformanceStatistics", func(t testing.TB, p Provider, tr TimeRange) int {
			res, err := p.GetMetricQueryPerformanceStatistics(ctx, "up", tr)
			require.NoError(t, err, "MetricQueryPerformanceStatistics")
			return intOrZero(res.TotalQueries)
		}},
	)
}()

// intOrZero returns *n, or 0 when n is nil.
func intOrZero(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// rangeBoundsQuery is a failed 7ms range query on metric up at ts, with
// fingerprint fp, so every method in rangeMethods reports it when in range.
func rangeBoundsQuery(ts time.Time) Query {
	q := rangeQuery(ts, "up", time.Hour)
	q.StatusCode, q.Duration, q.Fingerprint = 500, 7*time.Millisecond, "fp"
	return q
}

// rangeBoundOffsets are where testRangeBounds seeds a query, from the start
// of its range [10:00, 11:00].
var rangeBoundOffsets = []struct {
	name   string
	offset time.Duration
}{
	{"From-1s", -time.Second},
	{"From", 0},
	{"From+0.5s", 500 * time.Millisecond},
	{"To-0.5s", time.Hour - 500*time.Millisecond},
	{"To", time.Hour},
	{"To+0.5s", time.Hour + 500*time.Millisecond},
	{"To+59s", time.Hour + 59*time.Second},
}

// testRangeBounds verifies which queries around a range's bounds each range
// method reports, one query per offset in rangeBoundOffsets: want maps each
// range method to its count per offset, in order. Range methods and backends
// differ at To, so each backend states its own expectations.
func testRangeBounds(t *testing.T, b testBackend, want map[string][]int) {
	from := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	tr := mustTimeRange(t, from, from.Add(time.Hour))
	for name, counts := range want {
		require.Len(t, counts, len(rangeBoundOffsets), name)
	}
	for i, o := range rangeBoundOffsets {
		t.Run(o.name, func(t *testing.T) {
			t.Parallel()
			p := b.newProvider(t)
			mustInsertQueries(t, p, []Query{rangeBoundsQuery(from.Add(o.offset))})
			wantHere, got := map[string]int{}, map[string]int{}
			for name, counts := range want {
				wantHere[name] = counts[i]
			}
			for _, r := range rangeMethods {
				got[r.name] = r.count(t, p, tr)
			}
			assert.Equal(t, wantHere, got, "count per range method")
		})
	}
}

func TestPostgreSQL_RangeBounds(t *testing.T) {
	t.Parallel()
	series := []int{0, 1, 1, 1, 0, 0, 0}
	closed := []int{0, 1, 1, 1, 1, 0, 0}
	testRangeBounds(t, postgreSQLTestBackend, map[string][]int{
		"Throughput":                       series,
		"ErrorAnalysis":                    series,
		"StatusDistribution":               series,
		"LatencyTrends":                    series,
		"TimeRangeDistribution":            closed,
		"Executions":                       closed,
		"QueryTypes":                       closed,
		"AverageDuration":                  closed,
		"QueryRate":                        closed,
		"QueriesBySerieName":               closed,
		"QueryExpressions":                 closed,
		"MetricQueryPerformanceStatistics": closed,
	})
}

func TestSQLite_RangeBounds(t *testing.T) {
	t.Parallel()
	lastBucket := []int{0, 1, 1, 1, 1, 1, 1}
	toSecond := []int{0, 1, 1, 1, 1, 1, 0}
	open := []int{0, 1, 1, 1, 0, 0, 0}
	testRangeBounds(t, sqliteTestBackend, map[string][]int{
		"Throughput":                       lastBucket,
		"ErrorAnalysis":                    lastBucket,
		"StatusDistribution":               toSecond,
		"LatencyTrends":                    lastBucket,
		"TimeRangeDistribution":            toSecond,
		"Executions":                       toSecond,
		"QueryTypes":                       open,
		"AverageDuration":                  open,
		"QueryRate":                        open,
		"QueriesBySerieName":               open,
		"QueryExpressions":                 open,
		"MetricQueryPerformanceStatistics": open,
	})
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

	tr := mustTimeRange(t, now.Add(-10*time.Minute), now.Add(20*time.Minute))

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
