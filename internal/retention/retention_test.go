package retention

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

type fakeProvider struct {
	deleteCalls   []time.Time
	deleteResults []int64
	deleteErrors  []error
	callIndex     int
}

func (f *fakeProvider) DeleteQueriesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	f.deleteCalls = append(f.deleteCalls, cutoff)
	if f.callIndex < len(f.deleteErrors) {
		err := f.deleteErrors[f.callIndex]
		result := int64(0)
		if f.callIndex < len(f.deleteResults) {
			result = f.deleteResults[f.callIndex]
		}
		f.callIndex++
		return result, err
	}
	result := int64(0)
	if f.callIndex < len(f.deleteResults) {
		result = f.deleteResults[f.callIndex]
	}
	f.callIndex++
	return result, nil
}

func (f *fakeProvider) WithDB(func(*sql.DB))                                            {}
func (f *fakeProvider) Insert(context.Context, []db.Query) error                        { return nil }
func (f *fakeProvider) InsertRulesUsage(context.Context, []db.RulesUsage) error         { return nil }
func (f *fakeProvider) InsertDashboardUsage(context.Context, []db.DashboardUsage) error { return nil }
func (f *fakeProvider) GetSeriesMetadata(context.Context, db.SeriesMetadataParams) (*db.PagedResult, error) {
	return nil, nil
}
func (f *fakeProvider) UpsertMetricsCatalog(context.Context, []db.MetricCatalogItem) error {
	return nil
}
func (f *fakeProvider) RefreshMetricsUsageSummary(context.Context, db.TimeRange) error { return nil }
func (f *fakeProvider) UpsertMetricsJobIndex(context.Context, []db.MetricJobIndexItem) error {
	return nil
}
func (f *fakeProvider) ListJobs(context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) GetQueryTypes(context.Context, db.TimeRange, string) (*db.QueryTypesResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetAverageDuration(context.Context, db.TimeRange, string) (*db.AverageDurationResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryRate(context.Context, db.TimeRange, string, string) (*db.QueryRateResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueriesBySerieName(context.Context, db.QueriesBySerieNameParams) (*db.PagedResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryStatusDistribution(context.Context, db.TimeRange, string) ([]db.QueryStatusDistributionResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryLatencyTrends(context.Context, db.TimeRange, string, string) ([]db.QueryLatencyTrendsResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryThroughputAnalysis(context.Context, db.TimeRange) ([]db.QueryThroughputAnalysisResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryErrorAnalysis(context.Context, db.TimeRange, string) ([]db.QueryErrorAnalysisResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryTimeRangeDistribution(context.Context, db.TimeRange, string) ([]db.QueryTimeRangeDistributionResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetQueryExpressions(context.Context, db.QueryExpressionsParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}
func (f *fakeProvider) GetQueryExecutions(context.Context, db.QueryExecutionsParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}
func (f *fakeProvider) GetMetricStatistics(context.Context, string, db.TimeRange) (db.MetricUsageStatics, error) {
	return db.MetricUsageStatics{}, nil
}
func (f *fakeProvider) GetMetricQueryPerformanceStatistics(context.Context, string, db.TimeRange) (db.MetricQueryPerformanceStatistics, error) {
	return db.MetricQueryPerformanceStatistics{}, nil
}
func (f *fakeProvider) GetRulesUsage(context.Context, db.RulesUsageParams) (*db.PagedResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetDashboardUsage(context.Context, db.DashboardUsageParams) (*db.PagedResult, error) {
	return nil, nil
}
func (f *fakeProvider) GetSeriesMetadataByNames(context.Context, []string, string) ([]models.MetricMetadata, error) {
	return nil, nil
}
func (f *fakeProvider) Close() error { return nil }

func TestNewWorker(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.NoError(t, err)
	assert.NotNil(t, w)
	assert.Equal(t, cfg.Retention.Interval, w.interval)
	assert.Equal(t, cfg.Retention.RunTimeout, w.runTimeout)
	assert.Equal(t, cfg.Retention.QueriesMaxAge, w.queriesMaxAge)
}

func TestNewWorker_RejectsZeroQueriesMaxAge(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 0,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "queries_max_age must be positive")
}

func TestNewWorker_RejectsNegativeQueriesMaxAge(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: -1 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "queries_max_age must be positive")
}

func TestNewWorker_RejectsZeroInterval(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      0,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "interval must be positive")
}

func TestNewWorker_RejectsNegativeInterval(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      -1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "interval must be positive")
}

func TestNewWorker_RejectsZeroRunTimeout(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    0,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "run_timeout must be positive")
}

func TestNewWorker_RejectsNegativeRunTimeout(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    -1 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err)
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "run_timeout must be positive")
}

func TestNewWorker_RejectsIntervalLessThan5Nanoseconds(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      4 * time.Nanosecond,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	// This should still pass validation since interval > 0, but runLoop should handle it defensively
	assert.NoError(t, err)
	assert.NotNil(t, w)
}

func TestNewWorker_RejectsZeroQueriesMaxAgeEvenWhenDisabled(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       false,
			Interval:      1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 0,
		},
	}

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err) // Should fail because validation happens regardless of enabled status
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "queries_max_age must be positive")
}

func TestWorker_runOnce(t *testing.T) {
	fakeProv := &fakeProvider{
		deleteResults: []int64{42},
		deleteErrors:  []error{nil},
	}

	w := &Worker{
		dbProvider:    fakeProv,
		interval:      1 * time.Hour,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 30 * 24 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.runOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 1, "DeleteQueriesBefore should be called once")

	actualCutoff := fakeProv.deleteCalls[0]
	expectedCutoff := time.Now().UTC().Add(-w.queriesMaxAge)
	diff := actualCutoff.Sub(expectedCutoff)
	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 1*time.Second, "cutoff should be approximately now - queriesMaxAge")
}

func TestWorker_runOnce_Error(t *testing.T) {
	fakeProv := &fakeProvider{
		deleteResults: []int64{0},
		deleteErrors:  []error{errors.New("database error")},
	}

	w := &Worker{
		dbProvider:    fakeProv,
		interval:      1 * time.Hour,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 30 * 24 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.runOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 1, "DeleteQueriesBefore should be called once")
}

func TestWorker_runOnce_SkipsDeletionWhenQueriesMaxAgeIsZero(t *testing.T) {
	fakeProv := &fakeProvider{}

	w := &Worker{
		dbProvider:    fakeProv,
		interval:      1 * time.Hour,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 0,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.runOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 0, "DeleteQueriesBefore should not be called when queriesMaxAge is zero")
}

func TestWorker_runOnce_SkipsDeletionWhenQueriesMaxAgeIsNegative(t *testing.T) {
	fakeProv := &fakeProvider{}

	w := &Worker{
		dbProvider:    fakeProv,
		interval:      1 * time.Hour,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: -1 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.runOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 0, "DeleteQueriesBefore should not be called when queriesMaxAge is negative")
}

func TestWorker_runLoop_HandlesSmallInterval(t *testing.T) {
	fakeProv := &fakeProvider{
		deleteResults: []int64{0},
		deleteErrors:  []error{nil},
	}

	w := &Worker{
		dbProvider:    fakeProv,
		interval:      4 * time.Nanosecond, // Less than 5ns, which would cause w.interval/5 to be 0
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 30 * 24 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This should not panic even with a very small interval
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.runLoop(ctx)
	}()

	select {
	case <-done:
		// runLoop exited normally (due to context cancellation)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runLoop should have exited within timeout")
	}

	// Verify that runOnce was called at least once
	assert.GreaterOrEqual(t, len(fakeProv.deleteCalls), 1, "runOnce should have been called at least once")
}
