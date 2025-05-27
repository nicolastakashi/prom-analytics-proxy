package ingester

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/mock"
)

type MockDBProvider struct {
	mock.Mock
}

// Ensure MockDBProvider implements db.Provider
var _ db.Provider = (*MockDBProvider)(nil)

func (m *MockDBProvider) Insert(ctx context.Context, queries []db.Query) error {
	args := m.Called(ctx, queries)
	return args.Error(0)
}

func (m *MockDBProvider) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDBProvider) WithDB(f func(db *sql.DB)) {
	m.Called(f)
}

func (m *MockDBProvider) GetQueriesBySerieName(
	ctx context.Context,
	params db.QueriesBySerieNameParams) (*db.PagedResult, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(*db.PagedResult), args.Error(1)
}

func (m *MockDBProvider) InsertRulesUsage(ctx context.Context, rulesUsage []db.RulesUsage) error {
	args := m.Called(ctx, rulesUsage)
	return args.Error(0)
}

func (m *MockDBProvider) GetRulesUsage(ctx context.Context, params db.RulesUsageParams) (*db.PagedResult, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(*db.PagedResult), args.Error(1)
}

func (m *MockDBProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []db.DashboardUsage) error {
	args := m.Called(ctx, dashboardUsage)
	return args.Error(0)
}

func (m *MockDBProvider) GetDashboardUsage(ctx context.Context, params db.DashboardUsageParams) (*db.PagedResult, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(*db.PagedResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryTypes(ctx context.Context, tr db.TimeRange) (*db.QueryTypesResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).(*db.QueryTypesResult), args.Error(1)
}

func (m *MockDBProvider) GetAverageDuration(ctx context.Context, tr db.TimeRange) (*db.AverageDurationResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).(*db.AverageDurationResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryRate(ctx context.Context, tr db.TimeRange, metricName string) (*db.QueryRateResult, error) {
	args := m.Called(ctx, tr, metricName)
	return args.Get(0).(*db.QueryRateResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryStatusDistribution(ctx context.Context, tr db.TimeRange) ([]db.QueryStatusDistributionResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).([]db.QueryStatusDistributionResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryLatencyTrends(ctx context.Context, tr db.TimeRange, metricName string) ([]db.QueryLatencyTrendsResult, error) {
	args := m.Called(ctx, tr, metricName)
	return args.Get(0).([]db.QueryLatencyTrendsResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryThroughputAnalysis(ctx context.Context, tr db.TimeRange) ([]db.QueryThroughputAnalysisResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).([]db.QueryThroughputAnalysisResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryErrorAnalysis(ctx context.Context, tr db.TimeRange) ([]db.QueryErrorAnalysisResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).([]db.QueryErrorAnalysisResult), args.Error(1)
}

func (m *MockDBProvider) GetRecentQueries(ctx context.Context, params db.RecentQueriesParams) (db.PagedResult, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(db.PagedResult), args.Error(1)
}

func (m *MockDBProvider) GetMetricStatistics(ctx context.Context, metricName string, tr db.TimeRange) (db.MetricUsageStatics, error) {
	args := m.Called(ctx, metricName, tr)
	return args.Get(0).(db.MetricUsageStatics), args.Error(1)
}

func (m *MockDBProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr db.TimeRange) (db.MetricQueryPerformanceStatistics, error) {
	args := m.Called(ctx, metricName, tr)
	return args.Get(0).(db.MetricQueryPerformanceStatistics), args.Error(1)
}

func TestQueryIngester_Run(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 1 * time.Second,
		ingestTimeout:       1 * time.Second,
		batchSize:           2,
		batchFlushInterval:  500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ingester.Run(ctx)

	query1 := db.Query{QueryParam: "up"}
	query2 := db.Query{QueryParam: "node_cpu_seconds_total"}

	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

	ingester.Ingest(query1)
	ingester.Ingest(query2)

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}

func TestQueryIngester_Run_ShutdownGracePeriod(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 1 * time.Second,
		ingestTimeout:       1 * time.Second,
		batchSize:           2,
		batchFlushInterval:  500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	go ingester.Run(ctx)

	query1 := db.Query{QueryParam: "up"}
	query2 := db.Query{QueryParam: "node_cpu_seconds_total"}

	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

	ingester.Ingest(query1)
	ingester.Ingest(query2)

	time.Sleep(500 * time.Millisecond)
	cancel()

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}

func TestQueryIngester_Run_BatchFlushInterval(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 1 * time.Second,
		ingestTimeout:       1 * time.Second,
		batchSize:           10,
		batchFlushInterval:  500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ingester.Run(ctx)

	query1 := db.Query{QueryParam: "up"}

	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

	ingester.Ingest(query1)

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}
