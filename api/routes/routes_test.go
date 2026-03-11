package routes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
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
