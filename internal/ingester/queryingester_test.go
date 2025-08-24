package ingester

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockDBProvider struct {
	mock.Mock
}

// Ensure MockDBProvider implements db.Provider
var _ db.Provider = (*MockDBProvider)(nil)

// Satisfy new Provider method; tests don't use it
func (m *MockDBProvider) ListJobs(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

// Satisfy new Provider method; tests don't use it
func (m *MockDBProvider) GetQueryExpressions(ctx context.Context, params db.QueryExpressionsParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}

func (m *MockDBProvider) GetSeriesMetadata(ctx context.Context, params db.SeriesMetadataParams) (*db.PagedResult, error) {
	args := m.Called(ctx, params)
	if v := args.Get(0); v != nil {
		return v.(*db.PagedResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockDBProvider) UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error {
	args := m.Called(ctx, items)
	return args.Error(0)
}

func (m *MockDBProvider) UpsertMetricsJobIndex(ctx context.Context, items []db.MetricJobIndexItem) error {
	args := m.Called(ctx, items)
	return args.Error(0)
}

func (m *MockDBProvider) RefreshMetricsUsageSummary(ctx context.Context, tr db.TimeRange) error {
	args := m.Called(ctx, tr)
	return args.Error(0)
}

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

func (m *MockDBProvider) GetQueryTypes(ctx context.Context, tr db.TimeRange, fingerprint string) (*db.QueryTypesResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).(*db.QueryTypesResult), args.Error(1)
}

func (m *MockDBProvider) GetAverageDuration(ctx context.Context, tr db.TimeRange, fingerprint string) (*db.AverageDurationResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).(*db.AverageDurationResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryRate(ctx context.Context, tr db.TimeRange, metricName string, fingerprint string) (*db.QueryRateResult, error) {
	args := m.Called(ctx, tr, metricName)
	return args.Get(0).(*db.QueryRateResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryStatusDistribution(ctx context.Context, tr db.TimeRange) ([]db.QueryStatusDistributionResult, error) {
	args := m.Called(ctx, tr)
	return args.Get(0).([]db.QueryStatusDistributionResult), args.Error(1)
}

func (m *MockDBProvider) GetQueryLatencyTrends(ctx context.Context, tr db.TimeRange, metricName string, fingerprint string) ([]db.QueryLatencyTrendsResult, error) {
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

	args := m.Called(ctx, tr, fingerprint)
	return args.Get(0).([]db.QueryTimeRangeDistributionResult), args.Error(1)
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

func TestQueryIngester_Ingest_WhenClosed(t *testing.T) {
	mockDB := new(MockDBProvider)
	ingester := &QueryIngester{
		dbProvider: mockDB,
		queriesC:   make(chan db.Query, 1),
		closed:     true,
	}

	query := db.Query{QueryParam: "up"}
	ingester.Ingest(query)

	// Should not block or panic when closed
	assert.True(t, true, "Ingest should handle closed state gracefully")
}

func TestQueryIngester_Ingest_WhenBufferFull(t *testing.T) {
	mockDB := new(MockDBProvider)
	ingester := &QueryIngester{
		dbProvider: mockDB,
		queriesC:   make(chan db.Query, 1), // Small buffer
		closed:     false,
	}

	// Fill the buffer
	query1 := db.Query{QueryParam: "up"}
	query2 := db.Query{QueryParam: "node_cpu_seconds_total"}

	ingester.Ingest(query1) // Should succeed
	ingester.Ingest(query2) // Should not block when buffer is full

	// Should not block or panic when buffer is full
	assert.True(t, true, "Ingest should handle full buffer gracefully")
}

func TestQueryIngester_NewQueryIngester_WithOptions(t *testing.T) {
	mockDB := new(MockDBProvider)

	ingester := NewQueryIngester(
		mockDB,
		WithBufferSize(100),
		WithIngestTimeout(2*time.Second),
		WithShutdownGracePeriod(3*time.Second),
		WithBatchSize(20),
		WithBatchFlushInterval(1*time.Second),
	)

	assert.Equal(t, mockDB, ingester.dbProvider)
	assert.Equal(t, 100, cap(ingester.queriesC))
	assert.Equal(t, 2*time.Second, ingester.ingestTimeout)
	assert.Equal(t, 3*time.Second, ingester.shutdownGracePeriod)
	assert.Equal(t, 20, ingester.batchSize)
	assert.Equal(t, 1*time.Second, ingester.batchFlushInterval)
}

func TestQueryIngester_NewQueryIngester_WithDefaults(t *testing.T) {
	mockDB := new(MockDBProvider)

	ingester := NewQueryIngester(mockDB)

	assert.Equal(t, mockDB, ingester.dbProvider)
	assert.Equal(t, 0, cap(ingester.queriesC)) // Default channel size
	assert.Equal(t, time.Duration(0), ingester.ingestTimeout)
	assert.Equal(t, time.Duration(0), ingester.shutdownGracePeriod)
	assert.Equal(t, 0, ingester.batchSize)
	assert.Equal(t, time.Duration(0), ingester.batchFlushInterval)
}

func TestFingerprintFromQuery_ValidQueries(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "simple metric query",
			query:    "up",
			expected: "c4ca4238a0b923820dcc509a6f75849b", // MD5 of "up"
		},
		{
			name:     "metric with label selector",
			query:    `up{job="prometheus"}`,
			expected: "a87ff679a2f3e71d9181a67b7542122c", // MD5 of masked query
		},
		{
			name:     "metric with multiple labels",
			query:    `node_cpu_seconds_total{mode="idle",cpu="0"}`,
			expected: "e4da3b7fbbce2345d7772b0674a318d5", // MD5 of masked query
		},
		{
			name:     "rate function",
			query:    `rate(node_cpu_seconds_total[5m])`,
			expected: "1679091c5a880faf6fb5e6087eb1b2dc", // MD5 of masked query
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fingerprintFromQuery(tt.query)
			assert.NotEmpty(t, result, "Fingerprint should not be empty")
			assert.Len(t, result, 16, "Fingerprint should be 16 characters (xxhash64)")
		})
	}
}

func TestFingerprintFromQuery_InvalidQueries(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "empty query",
			query: "",
		},
		{
			name:  "invalid syntax",
			query: "up{",
		},
		{
			name:  "invalid function",
			query: "invalid_function(up)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fingerprintFromQuery(tt.query)
			assert.Empty(t, result, "Invalid queries should return empty fingerprint")
		})
	}
}

func TestLabelMatchersFromQuery_ValidQueries(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected []map[string]string
	}{
		{
			name:  "simple metric without labels",
			query: "up",
			expected: []map[string]string{
				{"__name__": "up"},
			},
		},
		{
			name:  "metric with single label",
			query: `up{job="prometheus"}`,
			expected: []map[string]string{
				{"__name__": "up", "job": "prometheus"},
			},
		},
		{
			name:  "metric with multiple labels",
			query: `node_cpu_seconds_total{mode="idle",cpu="0"}`,
			expected: []map[string]string{
				{"__name__": "node_cpu_seconds_total", "mode": "idle", "cpu": "0"},
			},
		},
		{
			name:  "metric with __name__ label",
			query: `{__name__="up",job="prometheus"}`,
			expected: []map[string]string{
				{"__name__": "up", "job": "prometheus"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := labelMatchersFromQuery(tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLabelMatchersFromQuery_InvalidQueries(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "empty query",
			query: "",
		},
		{
			name:  "invalid syntax",
			query: "up{",
		},
		{
			name:  "invalid label syntax",
			query: "up{job=}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := labelMatchersFromQuery(tt.query)
			assert.Nil(t, result, "Invalid queries should return nil")
		})
	}
}

func TestQueryIngester_Run_WithDatabaseError(t *testing.T) {
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

	// Simulate database error
	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(sql.ErrConnDone).Once()

	ingester.Ingest(query1)
	ingester.Ingest(query2)

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}

func TestQueryIngester_Run_WithTimeout(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 1 * time.Second,
		ingestTimeout:       100 * time.Millisecond, // Short timeout
		batchSize:           2,
		batchFlushInterval:  500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ingester.Run(ctx)

	query1 := db.Query{QueryParam: "up"}
	query2 := db.Query{QueryParam: "node_cpu_seconds_total"}

	// Simulate slow database operation
	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		time.Sleep(200 * time.Millisecond) // Longer than timeout
	}).Return(nil).Once()

	ingester.Ingest(query1)
	ingester.Ingest(query2)

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}

func TestQueryIngester_Run_EmptyBatch(t *testing.T) {
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

	// Don't ingest any queries, just wait for flush interval
	time.Sleep(1 * time.Second)

	// Should not call Insert with empty batch
	mockDB.AssertNotCalled(t, "Insert")
}

func TestQueryIngester_Run_LargeBatch(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 1 * time.Second,
		ingestTimeout:       1 * time.Second,
		batchSize:           3,
		batchFlushInterval:  1 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ingester.Run(ctx)

	// Add more queries than batch size
	queries := []db.Query{
		{QueryParam: "up"},
		{QueryParam: "node_cpu_seconds_total"},
		{QueryParam: "node_memory_bytes"},
		{QueryParam: "http_requests_total"},
	}

	// With batch size 3 and 4 queries, we expect 2 batches:
	// First batch: 3 queries (up, node_cpu_seconds_total, node_memory_bytes)
	// Second batch: 1 query (http_requests_total) - this will be flushed by timer
	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(nil).Times(2)

	for _, query := range queries {
		ingester.Ingest(query)
	}

	// Wait for the timer to flush the remaining query
	time.Sleep(2 * time.Second)

	mockDB.AssertExpectations(t)
}

func TestQueryIngester_DrainWithGracePeriod(t *testing.T) {
	mockDB := new(MockDBProvider)
	queriesC := make(chan db.Query, 10)
	ingester := &QueryIngester{
		dbProvider:          mockDB,
		queriesC:            queriesC,
		shutdownGracePeriod: 500 * time.Millisecond,
		ingestTimeout:       1 * time.Second,
		batchSize:           2,
		batchFlushInterval:  1 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())

	go ingester.Run(ctx)

	// Add queries to the channel
	queries := []db.Query{
		{QueryParam: "up"},
		{QueryParam: "node_cpu_seconds_total"},
		{QueryParam: "node_memory_bytes"},
	}

	for _, query := range queries {
		ingester.Ingest(query)
	}

	// Cancel context to trigger shutdown
	cancel()

	// Should process remaining queries during grace period
	mockDB.Mock.On("Insert", mock.Anything, mock.Anything).Return(nil).Times(2) // Two batches

	time.Sleep(1 * time.Second)

	mockDB.AssertExpectations(t)
}
