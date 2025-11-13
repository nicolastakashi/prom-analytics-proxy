package routes

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
