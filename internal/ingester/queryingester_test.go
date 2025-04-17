package ingester

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/mock"
)

type MockDBProvider struct {
	mock.Mock
}

func (m *MockDBProvider) Insert(ctx context.Context, queries []db.Query) error {
	args := m.Called(ctx, queries)
	return args.Error(0)
}

func (m *MockDBProvider) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDBProvider) Query(ctx context.Context, query string) (*db.QueryResult, error) {
	args := m.Called(ctx, query)
	return args.Get(0).(*db.QueryResult), args.Error(1)
}

func (p *MockDBProvider) WithDB(f func(db *sql.DB)) {
}

func (m *MockDBProvider) QueryShortCuts() []db.QueryShortCut {
	return nil
}

func (p *MockDBProvider) GetQueriesBySerieName(
	ctx context.Context,
	params db.QueriesBySerieNameParams) (*db.PagedResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockDBProvider) InsertRulesUsage(ctx context.Context, rulesUsage []db.RulesUsage) error {
	args := m.Called(ctx, rulesUsage)
	return args.Error(0)
}

func (m *MockDBProvider) GetRulesUsage(ctx context.Context, params db.RulesUsageParams) (*db.PagedResult, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(*db.PagedResult), args.Error(1)
}

func (p *MockDBProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []db.DashboardUsage) error {
	return nil
}

func (p *MockDBProvider) GetDashboardUsage(ctx context.Context, serieName string, page, pageSize int) (*db.PagedResult, error) {
	return nil, nil
}

func (p *MockDBProvider) QueryTypes(ctx context.Context, tr db.TimeRange) (*db.QueryTypesResult, error) {
	return nil, nil
}

func (p *MockDBProvider) AverageDuration(ctx context.Context, tr db.TimeRange) (*db.AverageDurationResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetQueryRate(ctx context.Context, tr db.TimeRange, metricName string) (*db.QueryRateResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetQueryStatusDistribution(ctx context.Context, tr db.TimeRange) ([]db.QueryStatusDistributionResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetQueryLatencyTrends(ctx context.Context, tr db.TimeRange, metricName string) ([]db.QueryLatencyTrendsResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetQueryThroughputAnalysis(ctx context.Context, tr db.TimeRange) ([]db.QueryThroughputAnalysisResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetQueryErrorAnalysis(ctx context.Context, tr db.TimeRange) ([]db.QueryErrorAnalysisResult, error) {
	return nil, nil
}

func (p *MockDBProvider) GetRecentQueries(ctx context.Context, params db.RecentQueriesParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}

func (p *MockDBProvider) GetMetricStatistics(ctx context.Context, metricName string, tr db.TimeRange) (db.MetricUsageStatics, error) {
	return db.MetricUsageStatics{}, nil
}

func (p *MockDBProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr db.TimeRange) (db.MetricQueryPerformanceStatistics, error) {
	return db.MetricQueryPerformanceStatistics{}, nil
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

	mockDB.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

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

	mockDB.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

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

	mockDB.On("Insert", mock.Anything, mock.Anything).Return(nil).Once()

	ingester.Ingest(query1)

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}
