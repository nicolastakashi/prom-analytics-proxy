package ingester

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	otlppkg "github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
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
	ing := NewOtlpIngester(nil, mp)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
		buildGaugeMetric("unknown_metric", 2),
	)

	// Expect a single call with any slice (we assert content via behavior)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0},
		// unknown_metric intentionally not returned
	}, nil).Once()

	_, err := ing.Export(context.Background(), req)
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
			OTLP: config.OtlpIngesterConfig{AllowedJobs: []string{"prometheus"}},
		},
	}
	ing := NewOtlpIngester(cfg, mp)

	// DB: mark "unused_metric" as unused globally
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0},
	}, nil).Once()

	req := buildExportRequestForJobs(
		[]string{"prometheus", "node"},
		[][]*metricspb.Metric{
			{buildGaugeMetric("unused_metric", 1)}, // should drop (allowed job)
			{buildGaugeMetric("unused_metric", 1)}, // should keep (not allowed job)
		},
	)
	_, err := ing.Export(context.Background(), req)
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
			OTLP: config.OtlpIngesterConfig{DeniedJobs: []string{"prometheus"}},
		},
	}
	ing := NewOtlpIngester(cfg, mp)

	// DB: mark "unused_metric" as unused globally
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0},
	}, nil).Once()

	req := buildExportRequestForJobs(
		[]string{"prometheus", "node"},
		[][]*metricspb.Metric{
			{buildGaugeMetric("unused_metric", 1)}, // should keep (denied job disables drop)
			{buildGaugeMetric("unused_metric", 1)}, // should drop (default behavior)
		},
	)
	_, err := ing.Export(context.Background(), req)
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
func TestExport_DBError_FailOpen(t *testing.T) {
	mp := &mockUsageProvider{}
	ing := NewOtlpIngester(nil, mp)

	req := buildExportRequest(
		buildGaugeMetric("unused_metric", 1),
	)

	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return(nil, assert.AnError).Once()

	_, err := ing.Export(context.Background(), req)
	assert.NoError(t, err)

	// No drops due to fail-open
	rms := req.ResourceMetrics
	assert.Len(t, rms, 1)
	assert.Len(t, rms[0].ScopeMetrics[0].Metrics, 1)

	mp.AssertExpectations(t)
}

func TestExport_DryRunMode_RecordsMetricsButDoesNotDrop(t *testing.T) {
	mp := &mockUsageProvider{}
	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			DryRun: true,
		},
	}
	ing := NewOtlpIngester(cfg, mp)

	req := buildExportRequest(
		buildGaugeMetric("used_metric", 2),
		buildGaugeMetric("unused_metric", 2),
		buildGaugeMetric("unknown_metric", 2),
	)

	// Expect a single call with any slice (we assert content via behavior)
	mp.On("GetSeriesMetadataByNames", mock.Anything, mock.Anything, "").Return([]models.MetricMetadata{
		{Name: "used_metric", QueryCount: 1},
		{Name: "unused_metric", AlertCount: 0, RecordCount: 0, DashboardCount: 0, QueryCount: 0},
		// unknown_metric intentionally not returned
	}, nil).Once()

	_, err := ing.Export(context.Background(), req)
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
						res = append(res, models.MetricMetadata{Name: n})
					} else {
						res = append(res, models.MetricMetadata{Name: n, QueryCount: 1})
					}
				}
				return res
			}}
			ing := NewOtlpIngester(nil, mp)

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
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(1).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skipf("Skipping OTLP integration (Docker not available): %v", err)
	}
	host, err := pgc.Host(ctx)
	assert.NoError(t, err)
	port, err := pgc.MappedPort(ctx, "5432/tcp")
	assert.NoError(t, err)

	// Wire config for provider
	config.DefaultConfig.Database.Provider = "postgresql"
	config.DefaultConfig.Database.PostgreSQL.Addr = host
	config.DefaultConfig.Database.PostgreSQL.Port = port.Int()
	config.DefaultConfig.Database.PostgreSQL.User = "testuser"
	config.DefaultConfig.Database.PostgreSQL.Password = "testpass"
	config.DefaultConfig.Database.PostgreSQL.Database = "testdb"
	config.DefaultConfig.Database.PostgreSQL.SSLMode = "disable"
	config.DefaultConfig.Database.PostgreSQL.DialTimeout = 5 * time.Second

	time.Sleep(2 * time.Second)
	prov, err := db.GetDbProvider(ctx, db.DatabaseProvider(config.DefaultConfig.Database.Provider))
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
	assert.NoError(t, prov.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}))

	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}}}}}
	}
	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: &resourcepb.Resource{}, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{buildGauge("used_metric"), buildGauge("unused_metric")}}}}}}

	ing := NewOtlpIngester(config.DefaultConfig, prov)
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
	assert.NoError(t, prov.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}))

	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}}}}}
	}
	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: &resourcepb.Resource{}, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{buildGauge("used_metric"), buildGauge("unused_metric")}}}}}}

	exp, err := otlppkg.NewOTLPExporter(endpoint, string(config.ProtocolOTLP), nil)
	assert.NoError(t, err)
	defer func() {
		_ = exp.Close()
	}()
	ing := NewOtlpIngester(config.DefaultConfig, prov)
	ing.SetExporter(exp)
	_, err = ing.Export(context.Background(), req)
	assert.NoError(t, err)

	time.Sleep(500 * time.Millisecond)
	out, err := getLogs(context.Background())
	assert.NoError(t, err)
	assert.Contains(t, out, "used_metric")
	assert.NotContains(t, out, "unused_metric")
}
