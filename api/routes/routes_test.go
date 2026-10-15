package routes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestSeriesMetadata_DBBacked(t *testing.T) {
	t.Parallel()
	// Use SQLite provider for an integration-style test
	provider, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		t.Skipf("sqlite provider unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Seed catalog directly
	now := time.Now().UTC()
	_ = provider.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "up", Type: "gauge"}})
	assert.NoError(t, provider.RefreshMetricsUsageSummary(context.Background(), mustTimeRange(t, now.Add(-1*time.Hour), now)))

	r := newTestRouter(t, provider)

	req := httptest.NewRequest("GET", "/api/v1/seriesMetadata?page=1&pageSize=5&type=all", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSeriesMetadata_UsageFilter_Route(t *testing.T) {
	t.Parallel()
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
	_ = provider.RefreshMetricsUsageSummary(context.Background(), mustTimeRange(t, now.Add(-1*time.Hour), now))
	// Catalogued after the only RefreshMetricsUsageSummary run: this metric has
	// never actually been evaluated, so it must not show up as "unused". See
	// https://github.com/nicolastakashi/prom-analytics-proxy/issues/570.
	_ = provider.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{{Name: "never_evaluated_metric", Type: "gauge"}})

	r := newTestRouter(t, provider)

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
			wantNames: []string{"unused_metric"},
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
	t.Parallel()
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

	r := newTestRouter(t, provider)

	req := httptest.NewRequest("GET", "/api/v1/query/time_range_distribution?from="+now.Add(-24*time.Hour).Format(time.RFC3339)+"&to="+now.Format(time.RFC3339), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

// captureSeriesMetadataProvider records the last SeriesMetadataParams it receives.
type captureSeriesMetadataProvider struct {
	benchDBProvider
	captured db.SeriesMetadataParams
}

func (p *captureSeriesMetadataProvider) GetSeriesMetadata(_ context.Context, params db.SeriesMetadataParams) (db.PagedResult, error) {
	p.captured = params
	return db.PagedResult{}, nil
}

func TestSeriesMetadataPageSizeClamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pageSize     string
		wantPageSize int
	}{
		{"10000", 10000},
		{"10001", 10000},
	}

	for _, tt := range tests {
		t.Run("pageSize="+tt.pageSize, func(t *testing.T) {
			spy := &captureSeriesMetadataProvider{}
			r := newTestRouter(t, spy)

			req := httptest.NewRequest("GET", "/api/v1/seriesMetadata?type=all&page=1&pageSize="+tt.pageSize, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			assert.Equal(t, tt.wantPageSize, spy.captured.PageSize)
		})
	}
}

func TestValidateQuery(t *testing.T) {
	t.Parallel()
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

// rangeCapturingProvider records the TimeRange GetQueryTypes receives.
type rangeCapturingProvider struct {
	db.Provider
	got db.TimeRange
}

func (c *rangeCapturingProvider) GetQueryTypes(_ context.Context, tr db.TimeRange, _ string) (*db.QueryTypesResult, error) {
	c.got = tr
	return &db.QueryTypesResult{}, nil
}

// TestQueryTypes_TimeRangeFromRequest verifies an endpoint, not the DB, owns
// the range's defaults: from and to pass through as given, and each defaults
// to now when absent.
func TestQueryTypes_TimeRangeFromRequest(t *testing.T) {
	t.Parallel()
	capture := &rangeCapturingProvider{}
	r := newTestRouter(t, capture)
	serve := func(t *testing.T, target string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s: expected 200, got %d, body=%s", target, w.Code, w.Body.String())
		}
	}

	serve(t, "/api/v1/query/types?from=2026-01-02T10:00:00Z&to=2026-01-02T11:00:00Z")
	assert.Equal(t, time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC), capture.got.From())
	assert.Equal(t, time.Date(2026, 1, 2, 11, 0, 0, 0, time.UTC), capture.got.To())

	synctest.Test(t, func(t *testing.T) {
		serve(t, "/api/v1/query/types")
		now := time.Now().UTC()
		assert.Equal(t, [2]time.Time{now, now}, [2]time.Time{capture.got.From(), capture.got.To()}, "from and to default to now")
	})
}

// newTestRouter returns routes serving provider, with stub Prometheus and UI
// endpoints, failing t if they cannot be built.
func newTestRouter(t testing.TB, provider db.Provider) *routes {
	t.Helper()
	upstream, _ := url.Parse("http://127.0.0.1")
	r, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, prometheus.NewRegistry(), false),
	)
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}
	return r
}

// TestPaginatedEndpoints_RejectNonIntegerPaging verifies every paginated
// endpoint answers 400, naming the parameter and the cause, to a page or
// pageSize that is not an integer, before reaching the database.
func TestPaginatedEndpoints_RejectNonIntegerPaging(t *testing.T) {
	t.Parallel()
	r := newTestRouter(t, struct{ db.Provider }{}) // any database call panics
	for _, path := range []string{
		"/api/v1/query/expressions?",
		"/api/v1/query/executions?fingerprint=fp&",
		"/api/v1/serieExpressions/up?",
		"/api/v1/serieUsage/up?kind=alert&",
	} {
		for paging, want := range map[string]string{
			"page=x":     `invalid page parameter: strconv.Atoi: parsing "x": invalid syntax`,
			"pageSize=x": `invalid pageSize parameter: strconv.Atoi: parsing "x": invalid syntax`,
		} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path+paging, nil))
			assert.Equal(t, 400, w.Code, "%s%s: %s", path, paging, w.Body.String())
			assert.Equal(t, want, errorMessage(t, w), "%s%s", path, paging)
		}
	}
}

// TestTimeRangeEndpoints_RejectAZeroInstantBound verifies every endpoint
// reading a time range answers 400 to a bound at the zero instant, which no
// range can hold, before reaching the database.
func TestTimeRangeEndpoints_RejectAZeroInstantBound(t *testing.T) {
	t.Parallel()
	r := newTestRouter(t, struct{ db.Provider }{}) // any database call panics
	for _, path := range []string{
		"/api/v1/query/types?",
		"/api/v1/query/average_duration?",
		"/api/v1/query/rate?",
		"/api/v1/query/status_distribution?",
		"/api/v1/query/latency?",
		"/api/v1/query/throughput?",
		"/api/v1/query/errors?",
		"/api/v1/query/time_range_distribution?",
		"/api/v1/query/expressions?",
		"/api/v1/query/executions?fingerprint=fp&",
		"/api/v1/metricStatistics/up?",
		"/api/v1/metricQueryPerformanceStatistics/up?",
		"/api/v1/serieExpressions/up?",
		"/api/v1/serieUsage/up?kind=alert&",
	} {
		for _, bound := range []string{"from=0001-01-01T00:00:00Z", "to=-62135596800"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path+bound, nil))
			assert.Equal(t, 400, w.Code, "%s%s: %s", path, bound, w.Body.String())
			assert.Equal(t, "invalid time range: time range required", errorMessage(t, w), "%s%s", path, bound)
		}
	}
}

// errorMessage returns the error field of w's JSON error body, failing t if
// the body is not one.
func errorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return body.Error
}
