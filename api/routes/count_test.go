package routes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countResp is the expected shape of every /count endpoint response.
type countResp struct {
	Total int `json:"total"`
}

func newCountTestRouter(t *testing.T, provider db.Provider) *routes {
	t.Helper()
	upstream, _ := url.Parse("http://127.0.0.1")
	reg := prometheus.NewRegistry()
	uiFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	r, err := NewRoutes(
		WithDBProvider(provider),
		WithProxy(upstream),
		WithPromAPI(upstream),
		WithHandlers(uiFS, reg, false),
	)
	require.NoError(t, err)
	return r
}

func newCountTestProvider(t *testing.T) db.Provider {
	t.Helper()
	p, err := db.GetDbProvider(context.Background(), db.SQLite)
	if err != nil {
		t.Skipf("sqlite provider unavailable: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func getCount(t *testing.T, r *routes, path string) (int, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w
}

func decodeCountResp(t *testing.T, w *httptest.ResponseRecorder) countResp {
	t.Helper()
	var resp countResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "decode count response: %s", w.Body.String())
	return resp
}

// ---------------------------------------------------------------------------
// Cache-Control header
// ---------------------------------------------------------------------------

func TestCountEndpoints_CacheControlHeader(t *testing.T) {
	p := newCountTestProvider(t)
	r := newCountTestRouter(t, p)

	// Any count endpoint must carry Cache-Control so clients can cache safely.
	code, w := getCount(t, r, "/api/v1/seriesMetadata/count")
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Contains(t, w.Header().Get("Cache-Control"), "max-age=")
	assert.Contains(t, w.Header().Get("Cache-Control"), "must-revalidate")
}

// ---------------------------------------------------------------------------
// /api/v1/seriesMetadata/count
// ---------------------------------------------------------------------------

func TestCountEndpoints_SeriesMetadata(t *testing.T) {
	p := newCountTestProvider(t)
	_ = p.UpsertMetricsCatalog(context.Background(), []db.MetricCatalogItem{
		{Name: "alpha", Type: "gauge"},
		{Name: "beta", Type: "counter"},
		{Name: "gamma", Type: "gauge"},
	})
	_ = p.RefreshMetricsUsageSummary(context.Background(), db.TimeRange{})
	r := newCountTestRouter(t, p)

	t.Run("total matches catalog size", func(t *testing.T) {
		code, w := getCount(t, r, "/api/v1/seriesMetadata/count")
		require.Equal(t, 200, code)
		assert.Equal(t, 3, decodeCountResp(t, w).Total)
	})

	t.Run("usage=unused filter is applied", func(t *testing.T) {
		// No usage has been recorded, so all three are unused.
		code, w := getCount(t, r, "/api/v1/seriesMetadata/count?usage=unused")
		require.Equal(t, 200, code)
		assert.Equal(t, 3, decodeCountResp(t, w).Total)
	})

	t.Run("type filter is applied", func(t *testing.T) {
		code, w := getCount(t, r, "/api/v1/seriesMetadata/count?type=gauge")
		require.Equal(t, 200, code)
		assert.Equal(t, 2, decodeCountResp(t, w).Total)
	})

	t.Run("filter matches catalog size", func(t *testing.T) {
		code, w := getCount(t, r, "/api/v1/seriesMetadata/count?filter=alpha")
		require.Equal(t, 200, code)
		assert.Equal(t, 1, decodeCountResp(t, w).Total)
	})

	t.Run("count matches paginated total field", func(t *testing.T) {
		// The count endpoint must agree with the total field returned by the
		// data endpoint for the same filter parameters.
		dataReq := httptest.NewRequest("GET", "/api/v1/seriesMetadata?page=1&pageSize=1&type=all", nil)
		dataW := httptest.NewRecorder()
		r.ServeHTTP(dataW, dataReq)
		require.Equal(t, 200, dataW.Code)
		var dataResp struct{ Total int }
		require.NoError(t, json.Unmarshal(dataW.Body.Bytes(), &dataResp))

		code, w := getCount(t, r, "/api/v1/seriesMetadata/count")
		require.Equal(t, 200, code)
		assert.Equal(t, dataResp.Total, decodeCountResp(t, w).Total)
	})
}

// ---------------------------------------------------------------------------
// /api/v1/query/expressions/count
// ---------------------------------------------------------------------------

func TestCountEndpoints_QueryExpressions(t *testing.T) {
	p := newCountTestProvider(t)
	now := time.Now().UTC()
	queries := []struct{ fp, q string }{
		{"fp1", "up"},
		{"fp2", "go_goroutines"},
		{"fp3", "process_cpu_seconds_total"},
	}
	for _, entry := range queries {
		_ = p.Insert(context.Background(), []db.Query{{
			TS:          now.Add(-5 * time.Minute),
			QueryParam:  entry.q,
			Fingerprint: entry.fp,
			TimeParam:   now,
			Duration:    1 * time.Millisecond,
			StatusCode:  200,
			Type:        db.QueryTypeInstant,
		}})
	}
	r := newCountTestRouter(t, p)

	from := now.Add(-1 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)

	code, w := getCount(t, r, "/api/v1/query/expressions/count?from="+from+"&to="+to)
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Equal(t, 3, decodeCountResp(t, w).Total)
}

// ---------------------------------------------------------------------------
// /api/v1/query/executions/count
// ---------------------------------------------------------------------------

func TestCountEndpoints_QueryExecutions(t *testing.T) {
	p := newCountTestProvider(t)
	now := time.Now().UTC()
	fingerprint := "abc123"
	for i := 0; i < 4; i++ {
		_ = p.Insert(context.Background(), []db.Query{{
			TS:          now.Add(-time.Duration(i+1) * time.Minute),
			QueryParam:  "up",
			Fingerprint: fingerprint,
			TimeParam:   now,
			Duration:    1 * time.Millisecond,
			StatusCode:  200,
			Type:        db.QueryTypeInstant,
		}})
	}
	r := newCountTestRouter(t, p)

	from := now.Add(-1 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)

	code, w := getCount(t, r, "/api/v1/query/executions/count?fingerprint="+fingerprint+"&from="+from+"&to="+to)
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Equal(t, 4, decodeCountResp(t, w).Total)
}

// ---------------------------------------------------------------------------
// /api/v1/serieExpressions/{name}/count
// ---------------------------------------------------------------------------

func TestCountEndpoints_SerieExpressions(t *testing.T) {
	p := newCountTestProvider(t)
	now := time.Now().UTC()
	for _, q := range []string{"rate(cpu[5m])", "sum(cpu)"} {
		_ = p.Insert(context.Background(), []db.Query{{
			TS:            now.Add(-5 * time.Minute),
			QueryParam:    q,
			TimeParam:     now,
			Duration:      1 * time.Millisecond,
			StatusCode:    200,
			Type:          db.QueryTypeInstant,
			LabelMatchers: db.LabelMatchers{{"__name__": "cpu"}},
		}})
	}
	r := newCountTestRouter(t, p)

	from := now.Add(-1 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)

	code, w := getCount(t, r, "/api/v1/serieExpressions/cpu/count?from="+from+"&to="+to)
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Equal(t, 2, decodeCountResp(t, w).Total)
}

// ---------------------------------------------------------------------------
// /api/v1/serieUsage/{name}/count
// ---------------------------------------------------------------------------

func TestCountEndpoints_RulesUsage(t *testing.T) {
	p := newCountTestProvider(t)
	now := time.Now().UTC()
	_ = p.InsertRulesUsage(context.Background(), []db.RulesUsage{
		{Serie: "cpu", GroupName: "g1", Name: "rule1", Expression: "cpu > 0", Kind: "alert", CreatedAt: now},
		{Serie: "cpu", GroupName: "g1", Name: "rule2", Expression: "cpu > 1", Kind: "alert", CreatedAt: now},
	})
	r := newCountTestRouter(t, p)

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)

	code, w := getCount(t, r, "/api/v1/serieUsage/cpu/count?kind=alert&from="+from+"&to="+to)
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Equal(t, 2, decodeCountResp(t, w).Total)
}

func TestCountEndpoints_DashboardUsage(t *testing.T) {
	p := newCountTestProvider(t)
	now := time.Now().UTC()
	_ = p.InsertDashboardUsage(context.Background(), []db.DashboardUsage{
		{Id: "d1", Serie: "cpu", Name: "CPU Dashboard", URL: "http://grafana/1", CreatedAt: now},
		{Id: "d2", Serie: "cpu", Name: "CPU Overview", URL: "http://grafana/2", CreatedAt: now},
		{Id: "d3", Serie: "cpu", Name: "Infra", URL: "http://grafana/3", CreatedAt: now},
	})
	r := newCountTestRouter(t, p)

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)

	code, w := getCount(t, r, "/api/v1/serieUsage/cpu/count?kind=dashboard&from="+from+"&to="+to)
	require.Equal(t, 200, code, "body: %s", w.Body.String())
	assert.Equal(t, 3, decodeCountResp(t, w).Total)
}
