package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"strconv"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestSeriesMetadata_DBBacked(t *testing.T) {
	// Use SQLite provider for an integration-style test
	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		t.Skipf("sqlite provider unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Seed catalog directly
	_ = provider.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "up", Type: "gauge"}})
	_ = provider.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{})

	upstream, _ := url.Parse("http://127.0.0.1")
	reg := prometheus.NewRegistry()
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	r, _ := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)

	req := httptest.NewRequest("GET", "/api/v1/seriesMetadata?page=1&pageSize=5&type=all", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSeriesMetadata_UsageFilter_Route(t *testing.T) {
	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		t.Skipf("sqlite provider unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	now := time.Now().UTC()
	_ = provider.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{
		{Name: "used_metric", Type: "gauge"},
		{Name: "unused_metric", Type: "gauge"},
	})
	_ = provider.Insert(context.Background(), []db.Query{{
		TS:            now.Add(-5 * time.Minute),
		QueryParam:    "used_metric",
		TimeParam:     now,
		Duration:      10 * time.Millisecond,
		StatusCode:    200,
		LabelMatchers: db.LabelMatchers{{"__name__": "used_metric"}},
		Type:          db.QueryTypeInstant,
	}})
	_ = provider.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{From: now.Add(-1 * time.Hour), To: now})
	_ = provider.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "no_summary_metric", Type: "gauge"}})

	upstream, _ := url.Parse("http://127.0.0.1")
	reg := prometheus.NewRegistry()
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	r, _ := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)

	tests := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{
			name:      "used usage filter",
			query:     "/api/v1/seriesMetadata?page=1&pageSize=10&type=all&usage=used",
			wantNames: []string{"used_metric"},
		},
		{
			name:      "unused usage filter",
			query:     "/api/v1/seriesMetadata?page=1&pageSize=10&type=all&usage=unused",
			wantNames: []string{"no_summary_metric", "unused_metric"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
			}

			var response struct {
				Data []models.MetricMetadata `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			names := make([]string, 0, len(response.Data))
			for _, metric := range response.Data {
				names = append(names, metric.Name)
			}
			assert.ElementsMatch(t, tt.wantNames, names)
		})
	}
}

func TestQueryTimeRangeDistribution_Route(t *testing.T) {
	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		t.Skipf("sqlite provider unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Seed a simple range query
	now := time.Now().UTC()
	_ = provider.Insert(context.Background(), []db.Query{{
		TS:         now.Add(-1 * time.Minute),
		QueryParam: "up",
		TimeParam:  now,
		Duration:   1 * time.Millisecond,
		StatusCode: 200,
		Type:       db.QueryTypeRange,
		Start:      now.Add(-30 * time.Minute),
		End:        now,
	}})

	upstream, _ := url.Parse("http://127.0.0.1")
	reg := prometheus.NewRegistry()
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	r, _ := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)

	req := httptest.NewRequest("GET", "/api/v1/query/time_range_distribution?from="+now.Add(-24*time.Hour).Format(time.RFC3339)+"&to="+now.Format(time.RFC3339), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
}
func TestValidateQuery(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name      string
		query     db.Query
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid instant query",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeInstant,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
				Step:       15,
			},
			wantError: false,
		},
		{
			name: "valid range query",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeRange,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
				Step:       15,
			},
			wantError: false,
		},
		{
			name: "missing query parameter",
			query: db.Query{
				QueryParam: "",
				TimeParam:  now,
				Type:       db.QueryTypeInstant,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
			},
			wantError: true,
			errorMsg:  "missing query parameter",
		},
		{
			name: "invalid query type",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       "invalid",
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
			},
			wantError: true,
			errorMsg:  "invalid query type",
		},
		{
			name: "invalid status code",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeInstant,
				StatusCode: 999,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
			},
			wantError: true,
			errorMsg:  "invalid status code",
		},
		{
			name: "negative duration",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeInstant,
				StatusCode: 200,
				Duration:   -100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
			},
			wantError: true,
			errorMsg:  "invalid duration",
		},
		{
			name: "negative body size",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeInstant,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   -1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
			},
			wantError: true,
			errorMsg:  "invalid body size",
		},
		{
			name: "missing start parameter for range query",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeRange,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      time.Time{},
				End:        now,
			},
			wantError: true,
			errorMsg:  "missing start parameter",
		},
		{
			name: "missing end parameter for range query",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeRange,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        time.Time{},
			},
			wantError: true,
			errorMsg:  "missing end parameter",
		},
		{
			name: "range query with end before start",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeRange,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now,
				End:        now.Add(-1 * time.Hour),
				Step:       15,
			},
			wantError: true,
			errorMsg:  "invalid range: end before start",
		},
		{
			name: "range query with negative step",
			query: db.Query{
				QueryParam: "up",
				TimeParam:  now,
				Type:       db.QueryTypeRange,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
				BodySize:   1024,
				Start:      now.Add(-1 * time.Hour),
				End:        now,
				Step:       -15,
			},
			wantError: true,
			errorMsg:  "invalid step",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validateQuery(tt.query)

			if tt.wantError {
				if err == nil {
					t.Errorf("validateQuery() expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("validateQuery() error = %v, want error containing %v", err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validateQuery() unexpected error = %v", err)
				}
				tt.query.TS = result.TS
				assert.Equal(t, tt.query, result, "validateQuery() result mismatch")
			}
		})
	}
}

// benchDBProvider is a no-op db.Provider that returns pre-built data from
// GetSeriesMetadataByNames, isolating the handler's own processing overhead.
type benchDBProvider struct {
	data []models.MetricMetadata
}

var _ db.Provider = (*benchDBProvider)(nil)

func (p *benchDBProvider) GetSeriesMetadataByNames(_ context.Context, _ []string, _ string) ([]models.MetricMetadata, error) {
	return p.data, nil
}

func (p *benchDBProvider) Close() error { return nil }
func (p *benchDBProvider) WithDB(_ func(*sql.DB)) {}
func (p *benchDBProvider) Insert(_ context.Context, _ []db.Query) error { return nil }
func (p *benchDBProvider) InsertRulesUsage(_ context.Context, _ []db.RulesUsage) error {
	return nil
}
func (p *benchDBProvider) InsertDashboardUsage(_ context.Context, _ []db.DashboardUsage) error {
	return nil
}
func (p *benchDBProvider) GetSeriesMetadata(_ context.Context, _ db.SeriesMetadataParams) (*db.PagedResult, error) {
	return nil, nil
}
func (p *benchDBProvider) UpsertMetricsCatalog(_ context.Context, _ []db.MetricCatalogItem) error {
	return nil
}
func (p *benchDBProvider) RefreshMetricsUsageSummary(_ context.Context, _ db.TimeRange) error {
	return nil
}
func (p *benchDBProvider) UpsertMetricsJobIndex(_ context.Context, _ []db.MetricJobIndexItem) error {
	return nil
}
func (p *benchDBProvider) ListJobs(_ context.Context) ([]string, error) { return nil, nil }
func (p *benchDBProvider) GetQueryTypes(_ context.Context, _ db.TimeRange, _ string) (*db.QueryTypesResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetAverageDuration(_ context.Context, _ db.TimeRange, _ string) (*db.AverageDurationResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryRate(_ context.Context, _ db.TimeRange, _ string, _ string) (*db.QueryRateResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueriesBySerieName(_ context.Context, _ db.QueriesBySerieNameParams) (*db.PagedResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryStatusDistribution(_ context.Context, _ db.TimeRange, _ string) ([]db.QueryStatusDistributionResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryLatencyTrends(_ context.Context, _ db.TimeRange, _ string, _ string) ([]db.QueryLatencyTrendsResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryThroughputAnalysis(_ context.Context, _ db.TimeRange) ([]db.QueryThroughputAnalysisResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryErrorAnalysis(_ context.Context, _ db.TimeRange, _ string) ([]db.QueryErrorAnalysisResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryTimeRangeDistribution(_ context.Context, _ db.TimeRange, _ string) ([]db.QueryTimeRangeDistributionResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetQueryExpressions(_ context.Context, _ db.QueryExpressionsParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}
func (p *benchDBProvider) GetQueryExecutions(_ context.Context, _ db.QueryExecutionsParams) (db.PagedResult, error) {
	return db.PagedResult{}, nil
}
func (p *benchDBProvider) GetMetricStatistics(_ context.Context, _ string, _ db.TimeRange) (db.MetricUsageStatics, error) {
	return db.MetricUsageStatics{}, nil
}
func (p *benchDBProvider) GetMetricQueryPerformanceStatistics(_ context.Context, _ string, _ db.TimeRange) (db.MetricQueryPerformanceStatistics, error) {
	return db.MetricQueryPerformanceStatistics{}, nil
}
func (p *benchDBProvider) GetRulesUsage(_ context.Context, _ db.RulesUsageParams) (*db.PagedResult, error) {
	return nil, nil
}
func (p *benchDBProvider) GetDashboardUsage(_ context.Context, _ db.DashboardUsageParams) (*db.PagedResult, error) {
	return nil, nil
}
func (p *benchDBProvider) DeleteQueriesBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// seriesMetaBenchProvider extends benchDBProvider with a real GetSeriesMetadata
// implementation that returns pre-built paged results, isolating the handler's
// HTTP and JSON overhead from any database work.
type seriesMetaBenchProvider struct {
	benchDBProvider
	result *db.PagedResult
}

func (p *seriesMetaBenchProvider) GetSeriesMetadata(_ context.Context, _ db.SeriesMetadataParams) (*db.PagedResult, error) {
	return p.result, nil
}

// makePagedResult builds a PagedResult representing one page of n unused metrics
// out of a total of n*totalPages metrics across all pages.
func makePagedResult(n, totalPages int) *db.PagedResult {
	data := make([]models.MetricMetadata, n)
	for i := range n {
		data[i] = models.MetricMetadata{Name: fmt.Sprintf("metric_%d", i), Type: "gauge"}
	}
	return &db.PagedResult{
		TotalPages: totalPages,
		Total:      n * totalPages,
		Data:       data,
	}
}

// seedCatalogAndJobIndex seeds n metrics into metrics_catalog and
// metrics_job_index for the given job. No metrics_usage_summary rows are
// written, so all metrics COALESCE to zero counts and appear unused.
func seedCatalogAndJobIndex(b *testing.B, provider db.Provider, n int, job string) {
	b.Helper()
	ctx := context.Background()

	catalog := make([]db.MetricCatalogItem, n)
	for i := range n {
		catalog[i] = db.MetricCatalogItem{Name: fmt.Sprintf("metric_%d", i), Type: "gauge"}
	}
	if err := provider.UpsertMetricsCatalog(ctx, catalog); err != nil {
		b.Fatalf("UpsertMetricsCatalog: %v", err)
	}

	jobIdx := make([]db.MetricJobIndexItem, n)
	for i := range n {
		jobIdx[i] = db.MetricJobIndexItem{Name: fmt.Sprintf("metric_%d", i), Job: job}
	}
	if err := provider.UpsertMetricsJobIndex(ctx, jobIdx); err != nil {
		b.Fatalf("UpsertMetricsJobIndex: %v", err)
	}
}

// pageSizeCases are the page sizes benchmarked across all SeriesMetadataUnused
// variants. 100 is the old cap (baseline); the rest exercise the new limit.
var pageSizeCases = []int{100, 500, 1000, 5000, 10000}

// totalMetrics is the production scenario used by the pagination benchmarks:
// the number of unused metrics the operator must sweep through.
const totalMetrics = 2400

// pagesFor returns the number of pages needed to cover total metrics at pageSize.
func pagesFor(pageSize int) int {
	return (totalMetrics + pageSize - 1) / pageSize
}

// BenchmarkSeriesMetadataUnused measures the handler cost of one
// GET /api/v1/seriesMetadata?unused=true request with the DB mocked, varying
// page size. This isolates HTTP parsing, parameter validation, and JSON
// serialisation from database work.
func BenchmarkSeriesMetadataUnused(b *testing.B) {
	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, ps := range pageSizeCases {
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			provider := &seriesMetaBenchProvider{result: makePagedResult(ps, pagesFor(ps))}
			reg := prometheus.NewRegistry()
			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, reg, false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=test-job&page=1&pageSize=%d", ps), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_Pagination measures the cost of a complete
// sweep over totalMetrics unused metrics at each page size, showing how
// increasing the page size reduces the number of round-trips.
// Each b.Loop() iteration covers all pages for that size.
func BenchmarkSeriesMetadataUnused_Pagination(b *testing.B) {
	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, ps := range pageSizeCases {
		pages := pagesFor(ps)
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			provider := &seriesMetaBenchProvider{result: makePagedResult(ps, pages)}
			reg := prometheus.NewRegistry()
			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, reg, false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for pg := range pages {
					req := httptest.NewRequest(http.MethodGet,
						fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=test-job&page=%d&pageSize=%d", pg+1, ps), nil)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					if w.Code != http.StatusOK {
						b.Fatalf("page %d: unexpected status %d: %s", pg+1, w.Code, w.Body.String())
					}
				}
			}
		})
	}
}

// startBenchPostgres launches a PostgreSQL 16 container via testcontainers and
// returns a fully-migrated db.Provider ready for use. Skips if Docker is not
// available. The returned cleanup must be deferred by the caller.
func startBenchPostgres(b *testing.B) (db.Provider, func()) {
	b.Helper()
	ctx := context.Background()

	pgc, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(1).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		b.Skipf("PostgreSQL container unavailable (Docker not running?): %v", err)
	}

	host, err := pgc.Host(ctx)
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("container host: %v", err)
	}
	port, err := pgc.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("container port: %v", err)
	}

	config.DefaultConfig.Database.Provider = "postgresql"
	config.DefaultConfig.Database.PostgreSQL.Addr = host
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("parse port: %v", err)
	}
	config.DefaultConfig.Database.PostgreSQL.Port = portNum
	config.DefaultConfig.Database.PostgreSQL.User = "testuser"
	config.DefaultConfig.Database.PostgreSQL.Password = "testpass"
	config.DefaultConfig.Database.PostgreSQL.Database = "testdb"
	config.DefaultConfig.Database.PostgreSQL.SSLMode = "disable"
	config.DefaultConfig.Database.PostgreSQL.DialTimeout = 5 * time.Second

	// Brief pause for the postmaster to finish initialising after the log line.
	time.Sleep(2 * time.Second)

	prov, err := db.GetDbProvider(ctx, db.PostGreSQL)
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("db provider: %v", err)
	}
	return prov, func() {
		_ = prov.Close()
		_ = pgc.Terminate(ctx)
	}
}

// BenchmarkSeriesMetadataUnused_SQLite_SinglePage measures the full cost
// (handler + real SQLite query) of one GET /api/v1/seriesMetadata?unused=true
// request returning 100 metrics, with a job filter matching all seeded metrics.
// BenchmarkSeriesMetadataUnused_SQLite_SinglePage measures the full cost
// (handler + real SQLite queries) of one seriesMetadata?unused=true request,
// varying page size. 10000 metrics are seeded so every page size returns a
// full page.
func BenchmarkSeriesMetadataUnused_SQLite_SinglePage(b *testing.B) {
	const seedN = 10000
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		b.Skipf("sqlite unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	seedCatalogAndJobIndex(b, provider, seedN, job)

	reg := prometheus.NewRegistry()
	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)
	if err != nil {
		b.Fatalf("NewRoutes: %v", err)
	}

	for _, ps := range pageSizeCases {
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", job, ps), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_SQLite_Pagination measures the full sweep cost
// against a real SQLite database seeded with totalMetrics (2400) unused
// metrics, showing how increasing page size reduces round-trips.
func BenchmarkSeriesMetadataUnused_SQLite_Pagination(b *testing.B) {
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		b.Skipf("sqlite unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	seedCatalogAndJobIndex(b, provider, totalMetrics, job)

	reg := prometheus.NewRegistry()
	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)
	if err != nil {
		b.Fatalf("NewRoutes: %v", err)
	}

	for _, ps := range pageSizeCases {
		pages := pagesFor(ps)
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for pg := range pages {
					req := httptest.NewRequest(http.MethodGet,
						fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=%d&pageSize=%d", job, pg+1, ps), nil)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					if w.Code != http.StatusOK {
						b.Fatalf("page %d: unexpected status %d: %s", pg+1, w.Code, w.Body.String())
					}
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_PostgreSQL_SinglePage measures the full cost
// (handler + real PostgreSQL queries) of one seriesMetadata?unused=true
// request, varying page size. 10000 metrics are seeded so every page size
// returns a full page.
func BenchmarkSeriesMetadataUnused_PostgreSQL_SinglePage(b *testing.B) {
	const seedN = 10000
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	provider, cleanup := startBenchPostgres(b)
	defer cleanup()

	seedCatalogAndJobIndex(b, provider, seedN, job)

	reg := prometheus.NewRegistry()
	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)
	if err != nil {
		b.Fatalf("NewRoutes: %v", err)
	}

	for _, ps := range pageSizeCases {
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", job, ps), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_PostgreSQL_Pagination measures the full sweep
// cost against a real PostgreSQL database seeded with totalMetrics (2400)
// unused metrics, showing how increasing page size reduces round-trips.
func BenchmarkSeriesMetadataUnused_PostgreSQL_Pagination(b *testing.B) {
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	provider, cleanup := startBenchPostgres(b)
	defer cleanup()

	seedCatalogAndJobIndex(b, provider, totalMetrics, job)

	reg := prometheus.NewRegistry()
	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)
	if err != nil {
		b.Fatalf("NewRoutes: %v", err)
	}

	for _, ps := range pageSizeCases {
		pages := pagesFor(ps)
		b.Run(fmt.Sprintf("pageSize%d", ps), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for pg := range pages {
					req := httptest.NewRequest(http.MethodGet,
						fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=%d&pageSize=%d", job, pg+1, ps), nil)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					if w.Code != http.StatusOK {
						b.Fatalf("page %d: unexpected status %d: %s", pg+1, w.Code, w.Body.String())
					}
				}
			}
		})
	}
}
