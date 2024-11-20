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
	serieName string,
	page int,
	pageSize int) (*db.PagedResult, error) {
	return nil, fmt.Errorf("not implemented")
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
