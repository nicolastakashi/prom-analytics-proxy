package ingester

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/assert"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestOTLPIngester_E2E_GRPC_FilterAndForward(t *testing.T) {
	prov, cleanupDB := runPostgres(t)
	defer cleanupDB()
	endpoint, getLogs, cleanupCol := runOtelCollector(t)
	defer cleanupCol()

	err := prov.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{
		{Name: "used_metric", Type: "gauge"},
		{Name: "unused_metric", Type: "gauge"},
	})
	assert.NoError(t, err)
	now := time.Now().UTC()
	assert.NoError(t, prov.Insert(context.Background(), []db.Query{{
		TS:            now,
		QueryParam:    "used_metric",
		TimeParam:     now,
		Duration:      5 * time.Millisecond,
		StatusCode:    200,
		LabelMatchers: db.LabelMatchers{{"__name__": "used_metric"}},
		Type:          db.QueryTypeInstant,
	}}))
	assert.NoError(t, prov.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{From: now.Add(-1 * time.Hour), To: now.Add(1 * time.Hour)}))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := &config.Config{
		Ingester: config.IngesterConfig{
			Protocol:                string(config.ProtocolOTLP),
			GracefulShutdownTimeout: 5 * time.Second,
			DrainDelay:              0,
			OTLP: config.OtlpIngesterConfig{
				ListenAddress:     addr,
				DownstreamAddress: endpoint,
			},
		},
	}
	ing := NewOtlpIngester(cfg, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ing.Run(ctx)
	}()

	conn, dErr := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if dErr != nil {
		cancel()
		t.Fatalf("grpc client: %v", dErr)
	}
	defer func() { _ = conn.Close() }()

	client := colmetricspb.NewMetricsServiceClient(conn)
	buildGauge := func(name string) *metricspb.Metric {
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{{Attributes: []*commonpb.KeyValue{}}},
				},
			},
		}
	}
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource:     &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{buildGauge("used_metric"), buildGauge("unused_metric")}}},
		}},
	}
	_, err = client.Export(ctx, req)
	assert.NoError(t, err)

	time.Sleep(500 * time.Millisecond)
	out, err := getLogs(context.Background())
	assert.NoError(t, err)
	assert.Contains(t, out, "used_metric")
	assert.NotContains(t, out, "unused_metric")

	cancel()
	select {
	case <-time.After(5 * time.Second):
	case <-errCh:
	}
}
