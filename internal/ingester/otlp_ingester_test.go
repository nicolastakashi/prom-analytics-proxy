package ingester

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	otlppkg "github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockUsageProvider struct{ MockDBProvider }

func buildGaugeMetric(name string, n int) *metricspb.Metric {
	dps := make([]*metricspb.NumberDataPoint, 0, n)
	for i := 0; i < n; i++ {
		dps = append(dps, &metricspb.NumberDataPoint{Attributes: []*commonpb.KeyValue{}})
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}},
	}
}

func buildExportRequest(metrics ...*metricspb.Metric) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource:     &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "default"}}}}},
				ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
			},
		},
	}
}

func TestExport_DropsUnusedMetrics_KeepsUsedAndUnknown(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
		buildGaugeMetric("unknown_metric", 2),
	)

	// Expect a single call with any slice (we assert content via behavior)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
		// unknown_metric intentionally not returned
	}, nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// After filtering, only used_metric and unknown_metric remain
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	got := rms[0].ScopeMetrics[0].Metrics
	names := []string{got[0].GetName(), got[1].GetName()}
	assert.ElementsMatch(t, []string{"used_metric", "unknown_metric"}, names)

	mp.AssertExpectations(t)
}

func buildExportRequestForJobs(jobs []string, metrics [][]*metricspb.Metric) *colmetricspb.ExportMetricsServiceRequest {
	rms := make([]*metricspb.ResourceMetrics, 0, len(jobs))
	for i, job := range jobs {
		mset := []*metricspb.Metric{}
		if i < len(metrics) && metrics[i] != nil {
			mset = metrics[i]
		}
		rms = append(rms, &metricspb.ResourceMetrics{
			Resource:     &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: job}}}}},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: mset}},
		})
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: rms}
}

func TestExport_AllowedJobs_ScopesUnusedDrop(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			AllowedJobs: []string{"prometheus"},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	// DB: mark "unused_metric" as unused globally
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Once()

	req := buildExportRequestForJobs(
		[]string{"prometheus", "node"},
		[][]*metricspb.Metric{
			{buildGaugeMetric("unused_metric", 1)}, // should drop (allowed job)
			{buildGaugeMetric("unused_metric", 1)}, // should keep (not allowed job)
		},
	)
	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	rms := req.ResourceMetrics
	// prometheus resource should be removed (empty after drop)
	assert.Len(t, rms, 1)
	assert.Equal(t, "node", commonpbAttrString(rms[0].Resource.Attributes, "service.name"))
	assert.Len(t, rms[0].ScopeMetrics[0].Metrics, 1)
	assert.Equal(t, "unused_metric", rms[0].ScopeMetrics[0].Metrics[0].GetName())
}

func TestExport_DeniedJobs_DisablesUnusedDrop(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			DeniedJobs: []string{"prometheus"},
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	// DB: mark "unused_metric" as unused globally
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
	}, nil).Once()

	req := buildExportRequestForJobs(
		[]string{"prometheus", "node"},
		[][]*metricspb.Metric{
			{buildGaugeMetric("unused_metric", 1)}, // should keep (denied job disables drop)
			{buildGaugeMetric("unused_metric", 1)}, // should drop (default behavior)
		},
	)
	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Equal(t, "prometheus", commonpbAttrString(rms[0].Resource.Attributes, "service.name"))
	assert.Len(t, rms[0].ScopeMetrics[0].Metrics, 1)
	assert.Equal(t, "unused_metric", rms[0].ScopeMetrics[0].Metrics[0].GetName())
}

func commonpbAttrString(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			if v := kv.GetValue(); v != nil {
				if sv, ok := v.Value.(*commonpb.AnyValue_StringValue); ok {
					return sv.StringValue
				}
			}
		}
	}
	return ""
}
func TestExport_DryRunMode_RecordsMetricsButDoesNotDrop(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			DryRun: true,
		},
	}
	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
		buildGaugeMetric("unknown_metric", 2),
	)

	// Expect a single call with any slice (we assert content via behavior)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0, IsUnused: true},
		// unknown_metric intentionally not returned
	}, nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// In dry-run mode, all metrics should remain (no filtering applied)
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	got := rms[0].ScopeMetrics[0].Metrics
	assert.Len(t, got, 3) // All metrics should remain
	names := []string{got[0].GetName(), got[1].GetName(), got[2].GetName()}
	assert.ElementsMatch(t, []string{"used_metric", "unused_metric", "unknown_metric"}, names)

	mp.AssertExpectations(t)
}

func buildGaugeMetricN(name string, dps int) *metricspb.Metric {
	points := make([]*metricspb.NumberDataPoint, 0, dps)
	for i := 0; i < dps; i++ {
		points = append(points, &metricspb.NumberDataPoint{Attributes: []*commonpb.KeyValue{}})
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}},
	}
}

func buildRequestN(metricsCount, dpsPerMetric int) *colmetricspb.ExportMetricsServiceRequest {
	ms := make([]*metricspb.Metric, 0, metricsCount)
	for i := 0; i < metricsCount; i++ {
		name := "metric_" + strconv.Itoa(i)
		ms = append(ms, buildGaugeMetricN(name, dpsPerMetric))
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource:     &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: ms}},
		}},
	}
}

func BenchmarkExport_FilterSizes(b *testing.B) {
	cases := []struct {
		metrics     int
		dps         int
		unusedRatio float64
	}{
		{100, 1, 0.0},
		{1000, 5, 0.5},
		{10000, 1, 0.9},
		{100000, 1, 0.9},
		{1048576, 1, 0.9},
	}

	for _, c := range cases {
		b.Run(fmt.Sprintf("m%d_d%d_u%.1f", c.metrics, c.dps, c.unusedRatio), func(b *testing.B) {
			mp := &benchUsageProvider{gen: func(names []string) []models.MetricMetadata {
				res := make([]models.MetricMetadata, 0, len(names))
				k := int(float64(len(names)) * c.unusedRatio)
				for i, n := range names {
					if i < k {
						res = append(res, models.MetricMetadata{Name: n, IsUnused: true})
					} else {
						res = append(res, models.MetricMetadata{Name: n, QueryCount: 1})
					}
				}
				return res
			}}
			cfg := &config.Config{}
			ing, err := NewOtlpIngester(cfg, mp)
			assert.NoError(b, err)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := buildRequestN(c.metrics, c.dps)
				_, _ = ing.Export(context.Background(), req)
			}
		})
	}
}

type benchUsageProvider struct {
	MockDBProvider
	gen func([]string) []models.MetricMetadata
}

func (m *benchUsageProvider) GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error) {
	if m.gen != nil {
		return m.gen(names), nil
	}
	return []models.MetricMetadata{}, nil
}

// BenchmarkExport_WithCache benchmarks Export performance with cache enabled vs disabled.
func BenchmarkExport_WithCache(b *testing.B) {
	cases := []struct {
		name         string
		metrics      int
		cacheEnabled bool
		cacheHitRate float64 // 0.0 = all misses, 1.0 = all hits
	}{
		{"100_metrics_no_cache", 100, false, 0.0},
		{"100_metrics_cache_all_misses", 100, true, 0.0},
		{"100_metrics_cache_half_hits", 100, true, 0.5},
		{"100_metrics_cache_all_hits", 100, true, 1.0},
		{"1000_metrics_no_cache", 1000, false, 0.0},
		{"1000_metrics_cache_all_misses", 1000, true, 0.0},
		{"1000_metrics_cache_half_hits", 1000, true, 0.5},
		{"1000_metrics_cache_all_hits", 1000, true, 1.0},
		{"10000_metrics_no_cache", 10000, false, 0.0},
		{"10000_metrics_cache_all_misses", 10000, true, 0.0},
		{"10000_metrics_cache_half_hits", 10000, true, 0.5},
		{"10000_metrics_cache_all_hits", 10000, true, 1.0},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			mp := &benchUsageProvider{gen: func(names []string) []models.MetricMetadata {
				res := make([]models.MetricMetadata, 0, len(names))
				for _, n := range names {
					res = append(res, models.MetricMetadata{Name: n, QueryCount: 1})
				}
				return res
			}}

			cfg := &config.Config{}
			ing, err := NewOtlpIngester(cfg, mp)
			assert.NoError(b, err)

			// Pre-populate cache if needed (using in-memory cache, not Redis)
			if c.cacheEnabled {
				cache := &benchCache{
					states: make(map[string]otlppkg.MetricUsageState),
				}
				if c.cacheHitRate > 0 {
					hitCount := int(float64(c.metrics) * c.cacheHitRate)
					for i := 0; i < hitCount; i++ {
						name := "metric_" + strconv.Itoa(i)
						cache.states[name] = otlppkg.StateUsed
					}
				}
				ing.SetMetricCache(cache)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := buildRequestN(c.metrics, 1)
				_, _ = ing.Export(context.Background(), req)
			}
		})
	}
}

type benchCache struct {
	states map[string]otlppkg.MetricUsageState
}

func (c *benchCache) GetStates(ctx context.Context, names []string) (map[string]otlppkg.MetricUsageState, error) {
	result := make(map[string]otlppkg.MetricUsageState, len(names))
	for _, name := range names {
		if state, ok := c.states[name]; ok {
			result[name] = state
		} else {
			result[name] = otlppkg.StateUnknown
		}
	}
	return result, nil
}

func (c *benchCache) SetStates(ctx context.Context, states map[string]otlppkg.MetricUsageState) error {
	for name, state := range states {
		c.states[name] = state
	}
	return nil
}

func (c *benchCache) Close() error {
	return nil
}

// BenchmarkExport_MemoryUsage benchmarks memory consumption for different datapoint rates.
// This helps estimate memory usage for production workloads (e.g., 100k datapoints/second).
func BenchmarkExport_MemoryUsage(b *testing.B) {
	cases := []struct {
		name         string
		metrics      int
		dpsPerMetric int
		cacheEnabled bool
	}{
		{"1000_metrics_1_dp_no_cache", 1000, 1, false},
		{"1000_metrics_1_dp_with_cache", 1000, 1, true},
		{"10000_metrics_1_dp_no_cache", 10000, 1, false},
		{"10000_metrics_1_dp_with_cache", 10000, 1, true},
		{"1000_metrics_10_dp_no_cache", 1000, 10, false},
		{"1000_metrics_10_dp_with_cache", 1000, 10, true},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			mp := &benchUsageProvider{gen: func(names []string) []models.MetricMetadata {
				res := make([]models.MetricMetadata, 0, len(names))
				for _, n := range names {
					res = append(res, models.MetricMetadata{Name: n, QueryCount: 1})
				}
				return res
			}}

			cfg := &config.Config{}
			ing, err := NewOtlpIngester(cfg, mp)
			assert.NoError(b, err)

			if c.cacheEnabled {
				cache := &benchCache{
					states: make(map[string]otlppkg.MetricUsageState),
				}
				// Pre-populate cache with all metrics as used
				for i := 0; i < c.metrics; i++ {
					name := "metric_" + strconv.Itoa(i)
					cache.states[name] = otlppkg.StateUsed
				}
				ing.SetMetricCache(cache)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				req := buildRequestN(c.metrics, c.dpsPerMetric)
				_, _ = ing.Export(context.Background(), req)
			}
		})
	}
}

// TestExport_DownstreamTimeout verifies each forward to the downstream
// exporter runs under the configured timeout, or 5s when it is not positive,
// and until the incoming request's deadline when that is under 50ms away.
func TestExport_DownstreamTimeout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured time.Duration
		incoming   time.Duration // 0: no incoming deadline
		want       time.Duration
	}{
		{name: "configured", configured: 2 * time.Second, want: 2 * time.Second},
		{name: "unset", configured: 0, want: 5 * time.Second},
		{name: "negative", configured: -time.Second, want: 5 * time.Second},
		{name: "nearly expired request", configured: 2 * time.Second, incoming: 30 * time.Millisecond, want: 30 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mp := &mockUsageProvider{}
				mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, nil)
				cfg := &config.Config{Ingester: config.IngesterConfig{OTLP: config.OtlpIngesterConfig{
					DownstreamTimeout: tc.configured,
				}}}
				ing, err := NewOtlpIngester(cfg, mp)
				require.NoError(t, err)
				exp := &deadlineExporter{}
				ing.SetExporter(exp)
				ctx := context.Background()
				if tc.incoming > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.incoming)
					defer cancel()
				}

				start := time.Now()
				_, err = ing.Export(ctx, buildExportRequest(buildGaugeMetric("some_metric", 1)))
				require.NoError(t, err)
				require.Len(t, exp.deadlines, 1)
				assert.Equal(t, tc.want, exp.deadlines[0].Sub(start))
			})
		})
	}
}

// TestExport_RetriesUnavailableUnderDownstreamTimeout verifies a forward
// failing with Unavailable is retried once, 250ms later, under its own
// downstream timeout.
func TestExport_RetriesUnavailableUnderDownstreamTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mp := &mockUsageProvider{}
		mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, nil)
		cfg := &config.Config{Ingester: config.IngesterConfig{OTLP: config.OtlpIngesterConfig{
			DownstreamTimeout: 2 * time.Second,
		}}}
		ing, err := NewOtlpIngester(cfg, mp)
		require.NoError(t, err)
		exp := &deadlineExporter{errs: []error{status.Error(codes.Unavailable, "downstream unavailable")}}
		ing.SetExporter(exp)

		start := time.Now()
		_, err = ing.Export(context.Background(), buildExportRequest(buildGaugeMetric("some_metric", 1)))
		require.NoError(t, err)
		require.Len(t, exp.deadlines, 2)
		assert.Equal(t, 2*time.Second, exp.deadlines[0].Sub(start))
		assert.Equal(t, 250*time.Millisecond+2*time.Second, exp.deadlines[1].Sub(start))
	})
}

// metricValue returns the value of the counter, or the sample count of the
// histogram, named name whose labels are exactly labels, from the default
// registry; 0 when there is no such series.
func metricValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	series:
		for _, m := range mf.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			for _, lp := range m.GetLabel() {
				if labels[lp.GetName()] != lp.GetValue() {
					continue series
				}
			}
			if h := m.GetHistogram(); h != nil {
				return float64(h.GetSampleCount())
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// deadlineExporter records the deadline of the context each forward runs
// under, and fails forwards with errs in order.
type deadlineExporter struct {
	errs      []error
	deadlines []time.Time
}

func (d *deadlineExporter) Export(ctx context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	deadline, _ := ctx.Deadline()
	d.deadlines = append(d.deadlines, deadline)
	if len(d.errs) == 0 {
		return nil
	}
	err := d.errs[0]
	d.errs = d.errs[1:]
	return err
}
func (d *deadlineExporter) Close() error { return nil }

type captureExporter struct {
	captured *colmetricspb.ExportMetricsServiceRequest
}

func (c *captureExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	c.captured = req
	return nil
}
func (c *captureExporter) Close() error { return nil }

func runPostgres(t *testing.T) (db.Provider, func()) {
	t.Helper()
	ctx := context.Background()
	pgc, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skipf("Skipping OTLP integration (Docker not available): %v", err)
	}
	host, err := pgc.Host(ctx)
	assert.NoError(t, err)
	port, err := pgc.MappedPort(ctx, "5432/tcp")
	assert.NoError(t, err)
	portNum, err := strconv.Atoi(port.Port())
	assert.NoError(t, err)

	prov, err := db.NewPostgreSQLProvider(ctx, config.PostgreSQLConfig{
		Addr:        host,
		Port:        portNum,
		User:        "testuser",
		Password:    "testpass",
		Database:    "testdb",
		SSLMode:     "disable",
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		_ = pgc.Terminate(ctx)
		t.Fatalf("db provider: %v", err)
	}
	cleanup := func() {
		_ = prov.Close()
		_ = pgc.Terminate(ctx)
	}
	return prov, cleanup
}

func TestOTLPIngester_Integration_UnusedFiltering_PostgreSQL(t *testing.T) {
	prov, cleanup := runPostgres(t)
	defer cleanup()

	// Seed catalog and usage: used_metric has queries, unused_metric none
	err := prov.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "used_metric", Type: "gauge"}, {Name: "unused_metric", Type: "gauge"}})
	assert.NoError(t, err)

	now := time.Now().UTC()
	q := db.Query{TS: now, QueryParam: "used_metric", TimeParam: now, Duration: 10 * time.Millisecond, StatusCode: 200, LabelMatchers: db.LabelMatchers{{"__name__": "used_metric"}}, Type: db.QueryTypeInstant}
	assert.NoError(t, prov.Insert(context.Background(), []db.Query{q}))
	assert.NoError(t, prov.RefreshMetricsUsageSummary(context.Background(), mustTimeRange(t, now.Add(-1*time.Hour), now.Add(1*time.Hour))))

	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}}}}}
	}
	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: &resourcepb.Resource{}, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{buildGauge("used_metric"), buildGauge("unused_metric")}}}}}}

	ing, err := NewOtlpIngester(config.DefaultConfig, prov)
	assert.NoError(t, err)
	ing.SetExporter(&captureExporter{})
	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics, 1)
	ms := rms[0].ScopeMetrics[0].Metrics
	assert.Equal(t, 1, len(ms))
	assert.Equal(t, "used_metric", ms[0].GetName())
}

func runOtelCollector(t *testing.T) (endpoint string, logs func(ctx context.Context) (string, error), cleanup func()) {
	t.Helper()
	ctx := context.Background()
	cfg := `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
exporters:
  debug:
    verbosity: detailed
service:
  pipelines:
    metrics:
      receivers: [otlp]
      exporters: [debug]
`
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	req := testcontainers.ContainerRequest{
		Image:        "otel/opentelemetry-collector:0.136.0",
		ExposedPorts: []string{"4317/tcp"},
		Cmd:          []string{"--config", "/etc/otelcol/config.yaml"},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      cfgPath,
				ContainerFilePath: "/etc/otelcol/config.yaml",
				FileMode:          0644,
			},
		},
		WaitingFor: wait.ForLog("Everything is ready").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Skipf("Skipping collector (Docker not available): %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("collector host: %v", err)
	}
	port, err := c.MappedPort(ctx, "4317/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("collector port: %v", err)
	}
	getLogs := func(ctx context.Context) (string, error) {
		rc, err := c.Logs(ctx)
		if err != nil {
			return "", err
		}
		defer func() {
			_ = rc.Close()
		}()
		b, err := io.ReadAll(rc)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	cleanup = func() { _ = c.Terminate(ctx) }
	return host + ":" + port.Port(), getLogs, cleanup
}

func TestOTLPIngester_Integration_DownstreamCollector(t *testing.T) {
	prov, cleanupDB := runPostgres(t)
	defer cleanupDB()
	endpoint, getLogs, cleanupCol := runOtelCollector(t)
	defer cleanupCol()

	// Seed used vs unused
	err := prov.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "used_metric", Type: "gauge"}, {Name: "unused_metric", Type: "gauge"}})
	assert.NoError(t, err)
	now := time.Now().UTC()
	assert.NoError(t, prov.Insert(context.Background(), []db.Query{{TS: now, QueryParam: "used_metric", TimeParam: now, Duration: 5 * time.Millisecond, StatusCode: 200, LabelMatchers: db.LabelMatchers{{"__name__": "used_metric"}}, Type: db.QueryTypeInstant}}))
	assert.NoError(t, prov.RefreshMetricsUsageSummary(context.Background(), mustTimeRange(t, now.Add(-1*time.Hour), now.Add(1*time.Hour))))

	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}}}}}
	}
	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: &resourcepb.Resource{}, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{buildGauge("used_metric"), buildGauge("unused_metric")}}}}}}

	exp, err := otlppkg.NewOTLPExporter(endpoint, string(config.ProtocolOTLP), nil)
	assert.NoError(t, err)
	defer func() {
		_ = exp.Close()
	}()
	ing, err := NewOtlpIngester(config.DefaultConfig, prov)
	assert.NoError(t, err)
	ing.SetExporter(exp)
	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	time.Sleep(500 * time.Millisecond)
	out, err := getLogs(context.Background())
	assert.NoError(t, err)
	assert.Contains(t, out, "used_metric")
	assert.NotContains(t, out, "unused_metric")
}

// TestOTLPIngester_Integration_CatalogSync_PostgreSQL verifies that when CatalogSync is
// enabled, metrics arriving via OTLP Export are buffered and correctly flushed to the
// metrics_catalog table in PostgreSQL. It also verifies that the SeenTTL suppresses
// duplicate writes within the TTL window.
func TestOTLPIngester_Integration_CatalogSync_PostgreSQL(t *testing.T) {
	prov, cleanup := runPostgres(t)
	defer cleanup()

	// Build a config that enables catalog sync with an in-memory seen cache (no Redis).
	cfg := *config.DefaultConfig
	cfg.Ingester.CatalogSync.Enabled = true
	cfg.Ingester.CatalogSync.FlushInterval = 5 * time.Minute // manual flush only
	cfg.Ingester.CatalogSync.BufferSize = 100
	cfg.Ingester.CatalogSync.SeenTTL = 1 * time.Hour

	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}}}},
		}
	}
	buildHistogram := func(name string) *metricspb.Metric {
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{DataPoints: []*metricspb.HistogramDataPoint{{Attributes: []*commonpb.KeyValue{}}}}},
		}
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{
					buildGauge("catalog_metric_a"),
					buildHistogram("catalog_metric_b"),
					buildGauge("catalog_metric_c"),
				},
			}},
		}},
	}

	ing, err := NewOtlpIngester(&cfg, prov)
	assert.NoError(t, err)
	// Use a no-op exporter so forwarding doesn't fail with no downstream.
	ing.SetExporter(&captureExporter{})

	// Export populates the catalog buffer.
	ctx := context.Background()
	_, err = ing.Export(ctx, req)
	assert.NoError(t, err)

	// Flush the buffer manually and verify the catalog rows were written.
	ing.FlushCatalog(ctx)

	// Query the DB directly via WithDB to inspect the catalog.
	type catalogRow struct {
		name     string
		metaType string
	}
	var catalogRows []catalogRow
	prov.WithDB(func(sqlDB *sql.DB) {
		rows, err := sqlDB.QueryContext(ctx, `SELECT name, type FROM metrics_catalog ORDER BY name`)
		if err != nil {
			t.Fatalf("query metrics_catalog: %v", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r catalogRow
			if err := rows.Scan(&r.name, &r.metaType); err != nil {
				t.Fatalf("scan row: %v", err)
			}
			catalogRows = append(catalogRows, r)
		}
	})

	assert.Len(t, catalogRows, 6, "histograms should expand into Prometheus catalog variants")
	byName := make(map[string]string, len(catalogRows))
	for _, row := range catalogRows {
		byName[row.name] = row.metaType
	}
	assert.Equal(t, "gauge", byName["catalog_metric_a"])
	assert.Equal(t, "histogram", byName["catalog_metric_b"])
	assert.Equal(t, "histogram", byName["catalog_metric_b_bucket"])
	assert.Equal(t, "histogram", byName["catalog_metric_b_count"])
	assert.Equal(t, "histogram", byName["catalog_metric_b_sum"])
	assert.Equal(t, "gauge", byName["catalog_metric_c"])

	// A second Export + FlushCatalog within the SeenTTL window should not produce
	// additional writes (deduplication suppresses re-flushing the same metrics).
	req2 := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{
					buildGauge("catalog_metric_a"), // already seen
					buildGauge("catalog_metric_d"), // new metric
				},
			}},
		}},
	}
	_, err = ing.Export(ctx, req2)
	assert.NoError(t, err)
	ing.FlushCatalog(ctx)

	var catalogRows2 []catalogRow
	prov.WithDB(func(sqlDB *sql.DB) {
		rows, err := sqlDB.QueryContext(ctx, `SELECT name, type FROM metrics_catalog ORDER BY name`)
		if err != nil {
			t.Fatalf("query metrics_catalog (2nd flush): %v", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r catalogRow
			if err := rows.Scan(&r.name, &r.metaType); err != nil {
				t.Fatalf("scan row (2nd flush): %v", err)
			}
			catalogRows2 = append(catalogRows2, r)
		}
	})

	// catalog_metric_d is new → should be added; catalog_metric_a is suppressed by SeenTTL.
	assert.Len(t, catalogRows2, 7, "new metric catalog_metric_d should have been added alongside histogram variants")
	byName2 := make(map[string]string, len(catalogRows2))
	for _, row := range catalogRows2 {
		byName2[row.name] = row.metaType
	}
	assert.Equal(t, "gauge", byName2["catalog_metric_d"])
}

func TestOTLPIngester_CatalogFlush_RequeuesOnDBFailure(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{}
	cfg.Ingester.CatalogSync.Enabled = true
	cfg.Ingester.CatalogSync.FlushInterval = 5 * time.Minute
	cfg.Ingester.CatalogSync.BufferSize = 100
	cfg.Ingester.CatalogSync.SeenTTL = 1 * time.Hour

	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)
	ing.SetExporter(&captureExporter{})

	req := buildExportRequest(buildGaugeMetric("retry_metric", 1))
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "retry_metric", QueryCount: 1},
	}, nil).Once()

	upsertMatcher := mock.MatchedBy(func(items []db.MetricCatalogItem) bool {
		return len(items) == 1 && items[0].Name == "retry_metric"
	})
	mp.On("UpsertMetricsCatalog", mock.Anything, upsertMatcher).Return(assert.AnError).Once()
	mp.On("UpsertMetricsCatalog", mock.Anything, upsertMatcher).Return(nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// First flush fails and must requeue for retry.
	ing.FlushCatalog(context.Background())
	// Second flush must retry the same buffered item.
	ing.FlushCatalog(context.Background())

	mp.AssertExpectations(t)
}

// TestOTLPIngester_Run_Shutdown verifies that when Run's context is
// canceled, Run flushes the catalog buffer under a live context and closes
// the usage cache.
func TestOTLPIngester_Run_Shutdown(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{}
	cfg.Ingester.OTLP.ListenAddress = "127.0.0.1:0"
	cfg.Ingester.GracefulShutdownTimeout = 2 * time.Second
	cfg.Ingester.CatalogSync.Enabled = true
	cfg.Ingester.CatalogSync.FlushInterval = 5 * time.Minute
	cfg.Ingester.CatalogSync.BufferSize = 100
	cfg.Ingester.CatalogSync.SeenTTL = 1 * time.Hour

	ing, err := NewOtlpIngester(cfg, mp)
	assert.NoError(t, err)
	ing.SetExporter(&captureExporter{})
	cache := &deadlineCache{}
	ing.SetMetricCache(cache)

	req := buildExportRequest(buildGaugeMetric("shutdown_metric", 1))
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "shutdown_metric", QueryCount: 1},
	}, nil).Once()
	mp.On(
		"UpsertMetricsCatalog",
		mock.MatchedBy(func(ctx context.Context) bool { return ctx.Err() == nil }),
		mock.MatchedBy(func(items []db.MetricCatalogItem) bool {
			return len(items) == 1 && items[0].Name == "shutdown_metric"
		}),
	).Return(nil).Once()

	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- ing.Run(runCtx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case runErr := <-runDone:
		assert.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("ingester did not stop in time")
	}

	mp.AssertExpectations(t)
	assert.True(t, cache.closed, "usage cache closed")
}

// TestNewOtlpIngester_RejectsNilStore verifies an ingester cannot be built
// without a store.
func TestNewOtlpIngester_RejectsNilStore(t *testing.T) {
	_, err := NewOtlpIngester(&config.Config{}, nil)
	assert.Error(t, err)
}

// mustTimeRange is the db.TimeRange from from to to, failing tb if it is not
// a valid one.
func mustTimeRange(tb testing.TB, from, to time.Time) db.TimeRange {
	tb.Helper()
	tr, err := db.NewTimeRange(from, to)
	if err != nil {
		tb.Fatalf("NewTimeRange: %v", err)
	}
	return tr
}
