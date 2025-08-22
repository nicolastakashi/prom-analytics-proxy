package routes

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
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
