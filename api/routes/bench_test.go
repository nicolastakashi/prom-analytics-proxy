package routes

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// benchDBProvider is a no-op db.Provider that returns pre-built data from
// GetSeriesMetadataByNames, isolating the handler's own processing overhead.
type benchDBProvider struct {
	data []models.MetricMetadata
}

var _ db.Provider = (*benchDBProvider)(nil)

func (p *benchDBProvider) GetSeriesMetadataByNames(_ context.Context, _ []string, _ string) ([]models.MetricMetadata, error) {
	return p.data, nil
}

func (p *benchDBProvider) Close() error                    { return nil }
func (p *benchDBProvider) WithDB(_ func(*sql.DB))          {}
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

// seedCatalogAndSparseJobIndex seeds n catalog rows where only the first
// targetN are tagged with the target job; the rest are tagged with a noise
// job. The catalog stays fully unused (no RulesUsage / Refresh). This is
// the shape ?unused=true&job=<target> hits in production: a large unused
// universe across many jobs, with only a sparse subset under the operator's
// requested job. seedCatalogAndJobIndex puts every metric under one job and
// therefore cannot exercise this access pattern.
func seedCatalogAndSparseJobIndex(b *testing.B, provider db.Provider, n, targetN int, targetJob, noiseJob string) {
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
		job := noiseJob
		if i < targetN {
			job = targetJob
		}
		jobIdx[i] = db.MetricJobIndexItem{Name: fmt.Sprintf("metric_%d", i), Job: job}
	}
	if err := provider.UpsertMetricsJobIndex(ctx, jobIdx); err != nil {
		b.Fatalf("UpsertMetricsJobIndex: %v", err)
	}
}

// seedSummaryUsedRows marks the catalog rows metric_<fromIdx> through
// metric_<toIdx-1> as used by inserting one RulesUsage entry per metric and
// then refreshing metrics_usage_summary. After this call those metrics have
// alert_count >= 1 and are excluded from ?usage=unused results; everything
// outside [fromIdx, toIdx) remains unused. Uses the production code paths
// so the same helper works on both SQLite and PostgreSQL.
//
// After the refresh we run ANALYZE on the affected tables so the query
// planner has accurate row-count statistics. Without this, PostgreSQL's
// planner uses default 50%-selectivity estimates and picks a Hash Join
// over Seq Scans instead of a Nested Loop driven from the unused subset,
// which is ~100x slower at 240k rows. In production, autovacuum's
// autoanalyze covers the same ground between Refresh cycles; the bench
// calls ANALYZE explicitly to model that production-realistic state
// rather than the worst-case "just refreshed, autoanalyze hasn't run
// yet" snapshot.
func seedSummaryUsedRows(b *testing.B, provider db.Provider, fromIdx, toIdx int) {
	b.Helper()
	if fromIdx >= toIdx {
		return
	}
	ctx := context.Background()

	rules := make([]db.RulesUsage, 0, toIdx-fromIdx)
	for i := fromIdx; i < toIdx; i++ {
		rules = append(rules, db.RulesUsage{
			Serie:      fmt.Sprintf("metric_%d", i),
			GroupName:  "g",
			Name:       "r",
			Expression: "e",
			Kind:       "alert",
		})
	}
	if err := provider.InsertRulesUsage(ctx, rules); err != nil {
		b.Fatalf("InsertRulesUsage: %v", err)
	}

	tr := db.TimeRange{
		From: time.Now().Add(-time.Hour),
		To:   time.Now().Add(time.Hour),
	}
	if err := provider.RefreshMetricsUsageSummary(ctx, tr); err != nil {
		b.Fatalf("RefreshMetricsUsageSummary: %v", err)
	}

	var rawDB *sql.DB
	provider.WithDB(func(d *sql.DB) { rawDB = d })
	if rawDB == nil {
		return
	}
	for _, table := range []string{"metrics_usage_summary", "metrics_catalog", "metrics_job_index"} {
		if _, err := rawDB.ExecContext(ctx, "ANALYZE "+table); err != nil {
			b.Fatalf("ANALYZE %s: %v", table, err)
		}
	}
}

// totalMetrics is the production scenario used by the pagination benchmarks:
// the number of unused metrics the operator must sweep through.
const totalMetrics = 2400

// scaleUpTotals are the catalog sizes swept by the ScaleUp benchmarks. The
// number of unused metrics is held constant across all sizes so any change
// in latency reflects how the unused query scales with the catalog size
// rather than with the unused subset size.
var scaleUpTotals = []int{2400, 24000, 240000}

// scaleUpUnusedN is the constant number of unused metrics across all
// ScaleUp benchmark sizes. The remaining metric_<i> rows are marked as
// used by seeding RulesUsage and refreshing metrics_usage_summary.
const scaleUpUnusedN = 100

// scaleUpPageSize is large enough that scaleUpUnusedN unused metrics fit on
// a single page, so the benchmark measures one indexed lookup rather than
// pagination overhead.
const scaleUpPageSize = 1000

// pagesFor returns the number of pages needed to cover total metrics at pageSize.
func pagesFor(pageSize int) int {
	return (totalMetrics + pageSize - 1) / pageSize
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
				WithOccurrence(2).
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
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("parse port: %v", err)
	}

	pgCfg := config.PostgreSQLConfig{
		Addr:        host,
		Port:        portNum,
		User:        "testuser",
		Password:    "testpass",
		Database:    "testdb",
		SSLMode:     "disable",
		DialTimeout: 5 * time.Second,
	}
	prov, err := db.NewPostgreSQLProvider(ctx, pgCfg)
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("db provider: %v", err)
	}
	return prov, func() {
		_ = prov.Close()
		_ = pgc.Terminate(ctx)
	}
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
			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
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
			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
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

	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, prometheus.NewRegistry(), false),
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

	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, prometheus.NewRegistry(), false),
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

	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, prometheus.NewRegistry(), false),
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

	handler, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, prometheus.NewRegistry(), false),
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

// BenchmarkSeriesMetadataUnused_SQLite_ScaleUp varies the *total* catalog
// size while holding the number of unused metrics constant. The driving
// question for any ?usage=unused optimization is whether per-request
// latency scales with the unused subset (good) or with the total catalog
// (bad — the planner is still walking metrics_catalog). The baseline on
// main scales linearly with the total: this benchmark is the gate any
// proposed fix must clear.
func BenchmarkSeriesMetadataUnused_SQLite_ScaleUp(b *testing.B) {
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, totalN := range scaleUpTotals {
		b.Run(fmt.Sprintf("total%d", totalN), func(b *testing.B) {
			provider, err := db.GetDbProvider(context.Background(), db.SQLite)
			if err != nil {
				b.Skipf("sqlite unavailable: %v", err)
			}
			defer func() { _ = provider.Close() }()

			seedCatalogAndJobIndex(b, provider, totalN, job)
			seedSummaryUsedRows(b, provider, scaleUpUnusedN, totalN)

			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", job, scaleUpPageSize), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_PostgreSQL_ScaleUp is the PostgreSQL
// counterpart to the SQLite ScaleUp benchmark. See that benchmark's
// comment for the rationale.
func BenchmarkSeriesMetadataUnused_PostgreSQL_ScaleUp(b *testing.B) {
	const job = "test-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, totalN := range scaleUpTotals {
		b.Run(fmt.Sprintf("total%d", totalN), func(b *testing.B) {
			provider, cleanup := startBenchPostgres(b)
			defer cleanup()

			seedCatalogAndJobIndex(b, provider, totalN, job)
			seedSummaryUsedRows(b, provider, scaleUpUnusedN, totalN)

			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", job, scaleUpPageSize), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_SQLite_JobScopedScaleUp models the production
// hot path that broke on cx10 (PR #550 deploy): ?usage=unused&job=<name>
// where the unused universe is large but only a sparse subset is tagged with
// the requested job. ScaleUp varies the total catalog while holding the
// target-job match count constant; if the planner drives from the job
// index, latency stays roughly flat. If it drives from is_unused=TRUE and
// EXISTS-probes the job index per row, latency scales with total.
func BenchmarkSeriesMetadataUnused_SQLite_JobScopedScaleUp(b *testing.B) {
	const targetJob = "target-job"
	const noiseJob = "noise-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, totalN := range scaleUpTotals {
		b.Run(fmt.Sprintf("total%d", totalN), func(b *testing.B) {
			provider, err := db.GetDbProvider(context.Background(), db.SQLite)
			if err != nil {
				b.Skipf("sqlite unavailable: %v", err)
			}
			defer func() { _ = provider.Close() }()

			seedCatalogAndSparseJobIndex(b, provider, totalN, scaleUpUnusedN, targetJob, noiseJob)
			// No seedSummaryUsedRows call: every catalog row stays unused.
			// UpsertMetricsCatalog already creates default-unused summary
			// rows, so the unused predicate matches every metric.

			var rawDB *sql.DB
			provider.WithDB(func(d *sql.DB) { rawDB = d })
			if rawDB != nil {
				ctx := context.Background()
				for _, t := range []string{"metrics_usage_summary", "metrics_catalog", "metrics_job_index"} {
					if _, err := rawDB.ExecContext(ctx, "ANALYZE "+t); err != nil {
						b.Fatalf("ANALYZE %s: %v", t, err)
					}
				}
			}

			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", targetJob, scaleUpPageSize), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// BenchmarkSeriesMetadataUnused_PostgreSQL_JobScopedScaleUp is the PostgreSQL
// counterpart to the SQLite JobScopedScaleUp benchmark. See that benchmark's
// comment for the rationale.
func BenchmarkSeriesMetadataUnused_PostgreSQL_JobScopedScaleUp(b *testing.B) {
	const targetJob = "target-job"
	const noiseJob = "noise-job"

	upstream, _ := url.Parse("http://127.0.0.1")
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}

	for _, totalN := range scaleUpTotals {
		b.Run(fmt.Sprintf("total%d", totalN), func(b *testing.B) {
			provider, cleanup := startBenchPostgres(b)
			defer cleanup()

			seedCatalogAndSparseJobIndex(b, provider, totalN, scaleUpUnusedN, targetJob, noiseJob)

			var rawDB *sql.DB
			provider.WithDB(func(d *sql.DB) { rawDB = d })
			if rawDB != nil {
				ctx := context.Background()
				for _, t := range []string{"metrics_usage_summary", "metrics_catalog", "metrics_job_index"} {
					if _, err := rawDB.ExecContext(ctx, "ANALYZE "+t); err != nil {
						b.Fatalf("ANALYZE %s: %v", t, err)
					}
				}
			}

			handler, err := NewRoutes(
				WithDBProvider(provider),
				WithProxy(upstream),
				WithPromAPI(upstream),
				WithHandlers(uiFS, prometheus.NewRegistry(), false),
			)
			if err != nil {
				b.Fatalf("NewRoutes: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet,
					fmt.Sprintf("/api/v1/seriesMetadata?type=all&unused=true&job=%s&page=1&pageSize=%d", targetJob, scaleUpPageSize), nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
