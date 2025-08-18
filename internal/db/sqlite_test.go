package db

import (
	"context"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
)

// Test histogram and summary metrics catalog handling
func TestSQLite_HistogramSummaryMetricsCatalog(t *testing.T) {
	ctx := context.Background()
	provider, err := newSqliteProvider(ctx)
	if err != nil {
		t.Fatalf("newSqliteProvider: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Test histogram metrics: base metric should generate _bucket, _count, _sum variants
	histogramItems := []MetricCatalogItem{
		{Name: "access_evaluation_duration_bucket", Type: "histogram_bucket", Help: "Access evaluation duration (histogram buckets)", Unit: "seconds"},
		{Name: "access_evaluation_duration_count", Type: "histogram_count", Help: "Access evaluation duration (histogram count)", Unit: ""},
		{Name: "access_evaluation_duration_sum", Type: "histogram_sum", Help: "Access evaluation duration (histogram sum)", Unit: "seconds"},
	}

	// Test summary metrics: base metric should be kept, plus _count, _sum variants
	summaryItems := []MetricCatalogItem{
		{Name: "request_latency", Type: "summary", Help: "Request latency", Unit: "seconds"},
		{Name: "request_latency_count", Type: "summary_count", Help: "Request latency (summary count)", Unit: ""},
		{Name: "request_latency_sum", Type: "summary_sum", Help: "Request latency (summary sum)", Unit: "seconds"},
	}

	// Combine and upsert all items
	allItems := append(histogramItems, summaryItems...)
	if err := provider.UpsertMetricsCatalog(ctx, allItems); err != nil {
		t.Fatalf("UpsertMetricsCatalog: %v", err)
	}

	// Verify all metrics were stored correctly
	res, err := provider.GetSeriesMetadata(ctx, SeriesMetadataParams{
		Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all",
	})
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}

	// Should have 6 metrics total (3 histogram + 3 summary)
	if res.Total != 6 {
		t.Errorf("Expected 6 metrics, got %d", res.Total)
	}

	// Verify histogram metrics exist with correct types
	expectedHistogramMetrics := map[string]string{
		"access_evaluation_duration_bucket": "histogram_bucket",
		"access_evaluation_duration_count":  "histogram_count",
		"access_evaluation_duration_sum":    "histogram_sum",
	}

	// Verify summary metrics exist with correct types
	expectedSummaryMetrics := map[string]string{
		"request_latency":       "summary",
		"request_latency_count": "summary_count",
		"request_latency_sum":   "summary_sum",
	}

	// Check all expected metrics are present
	foundMetrics := make(map[string]string)
	if data, ok := res.Data.([]models.MetricMetadata); ok {
		for _, metric := range data {
			foundMetrics[metric.Name] = metric.Type
		}
	} else {
		t.Fatalf("Expected Data to be []models.MetricMetadata, got %T", res.Data)
	}

	for name, expectedType := range expectedHistogramMetrics {
		if actualType, found := foundMetrics[name]; !found {
			t.Errorf("Histogram metric %s not found", name)
		} else if actualType != expectedType {
			t.Errorf("Histogram metric %s has type %s, expected %s", name, actualType, expectedType)
		}
	}

	for name, expectedType := range expectedSummaryMetrics {
		if actualType, found := foundMetrics[name]; !found {
			t.Errorf("Summary metric %s not found", name)
		} else if actualType != expectedType {
			t.Errorf("Summary metric %s has type %s, expected %s", name, actualType, expectedType)
		}
	}
}

// Basic integration test exercising catalog upsert, summary refresh and list.
func TestSQLite_MetricsInventoryAndList(t *testing.T) {
	ctx := context.Background()
	provider, err := newSqliteProvider(ctx)
	if err != nil {
		t.Fatalf("newSqliteProvider: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Upsert catalog
	items := []MetricCatalogItem{{Name: "up", Type: "gauge", Help: "up metric", Unit: ""}}
	if err := provider.UpsertMetricsCatalog(ctx, items); err != nil {
		t.Fatalf("UpsertMetricsCatalog: %v", err)
	}

	// Insert a few queries for window
	now := time.Now().UTC()
	if err := provider.Insert(ctx, []Query{{
		TS:            now.Add(-time.Hour),
		QueryParam:    "up",
		TimeParam:     now,
		Duration:      10 * time.Millisecond,
		StatusCode:    200,
		BodySize:      10,
		LabelMatchers: LabelMatchers{{"__name__": "up"}},
		Type:          QueryTypeInstant,
	}}); err != nil {
		t.Fatalf("Insert queries: %v", err)
	}

	// Refresh summary for 1 day window
	if err := provider.RefreshMetricsUsageSummary(ctx, TimeRange{From: now.Add(-24 * time.Hour), To: now}); err != nil {
		t.Fatalf("RefreshMetricsUsageSummary: %v", err)
	}

	// Read list
	res, err := provider.GetSeriesMetadata(ctx, SeriesMetadataParams{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", Type: "all"})
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}
	if res.Total == 0 {
		t.Fatalf("expected at least one metric in catalog")
	}
}

// TestSQLite_DashboardUsage verifies dashboard usage time range filtering
func TestSQLite_DashboardUsage(t *testing.T) {
	ctx := context.Background()
	provider, err := newSqliteProvider(ctx)
	if err != nil {
		t.Fatalf("newSqliteProvider: %v", err)
	}
	defer func() { _ = provider.Close() }()

	// Use fixed timestamps for predictable testing
	baseTime := time.Date(2025, 8, 18, 20, 0, 0, 0, time.UTC)
	t.Logf("Base time: %v", baseTime.Format(time.RFC3339))
	dashboards := []DashboardUsage{
		{
			Id:        "dash1",
			Serie:     "metric1",
			Name:      "Dashboard 1",
			URL:       "http://grafana/d/dash1",
			CreatedAt: baseTime,
		},
		{
			Id:        "dash2",
			Serie:     "metric1",
			Name:      "Dashboard 2",
			URL:       "http://grafana/d/dash2",
			CreatedAt: baseTime,
		},
		{
			Id:        "dash3",
			Serie:     "metric2", // Different metric
			Name:      "Dashboard 3",
			URL:       "http://grafana/d/dash3",
			CreatedAt: baseTime,
		},
	}

	// Helper function to insert with specific timestamps
	insertWithTime := func(d DashboardUsage, firstSeen, lastSeen time.Time) error {
		// SQLite doesn't expose first_seen_at/last_seen_at in the struct,
		// but we can set them directly in the database
		_, err := provider.(*SQLiteProvider).db.ExecContext(ctx, `
			INSERT INTO DashboardUsage (
				id, serie, name, url, created_at, first_seen_at, last_seen_at
			) VALUES (?, ?, ?, ?, datetime(?), datetime(?), datetime(?))
			ON CONFLICT(id, serie) DO UPDATE SET 
				last_seen_at = CASE 
					WHEN datetime(excluded.last_seen_at) > datetime(last_seen_at) THEN datetime(excluded.last_seen_at) 
					ELSE datetime(last_seen_at) 
				END`,
			d.Id, d.Serie, d.Name, d.URL,
			d.CreatedAt.Format(SQLiteTimeFormat),
			firstSeen.Format(SQLiteTimeFormat),
			lastSeen.Format(SQLiteTimeFormat))
		return err
	}

	// Insert dashboards with specific time ranges
	// dash1: Should not be visible in time range filter test (18:30-20:00)
	if err := insertWithTime(dashboards[0], baseTime.Add(-2*time.Hour), baseTime.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Failed to insert dash1: %v", err)
	}
	// dash2: Should be visible in time range filter test (18:30-20:00)
	if err := insertWithTime(dashboards[1], baseTime.Add(-30*time.Minute), baseTime); err != nil {
		t.Fatalf("Failed to insert dash2: %v", err)
	}
	// dash3: Different metric
	if err := insertWithTime(dashboards[2], baseTime.Add(-30*time.Minute), baseTime); err != nil {
		t.Fatalf("Failed to insert dash3: %v", err)
	}

	// Debug: Check what's in the database
	rows, err := provider.(*SQLiteProvider).db.QueryContext(ctx, `
		SELECT id, serie, name, created_at, first_seen_at, last_seen_at,
		       datetime(first_seen_at) <= datetime(?) AS meets_start,
		       datetime(last_seen_at) >= datetime(?) AS meets_end
		FROM DashboardUsage 
		WHERE serie = ?
		ORDER BY id`,
		baseTime.Add(-3*time.Hour).Format(SQLiteTimeFormat),
		baseTime.Format(SQLiteTimeFormat),
		"metric1")
	if err != nil {
		t.Fatalf("Debug query failed: %v", err)
	}
	defer rows.Close()

	t.Log("Current database contents with time range check:")
	for rows.Next() {
		var id, serie, name string
		var created, firstSeen, lastSeen time.Time
		var meetsStart, meetsEnd bool
		if err := rows.Scan(&id, &serie, &name, &created, &firstSeen, &lastSeen, &meetsStart, &meetsEnd); err != nil {
			t.Fatalf("Debug scan failed: %v", err)
		}
		t.Logf("Dashboard %s (serie=%s): created=%v, first_seen=%v, last_seen=%v, meets_start=%v, meets_end=%v",
			id, serie, created.Format(time.RFC3339),
			firstSeen.Format(time.RFC3339),
			lastSeen.Format(time.RFC3339),
			meetsStart, meetsEnd)
	}

	tests := []struct {
		name    string
		params  DashboardUsageParams
		wantLen int
		wantIDs []string
		wantErr bool
	}{
		{
			name: "Find all dashboards for metric1",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 2,
			wantIDs: []string{"dash1", "dash2"},
		},
		{
			name: "Find dashboards with time range filter",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-90 * time.Minute),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash2"},
		},
		{
			name: "Find dashboards with name filter",
			params: DashboardUsageParams{
				Serie: "metric1",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Filter:   "Dashboard 1",
				Page:     1,
				PageSize: 10,
			},
			wantLen: 1,
			wantIDs: []string{"dash1"},
		},
		{
			name: "No dashboards for non-existent metric",
			params: DashboardUsageParams{
				Serie: "non-existent",
				TimeRange: TimeRange{
					From: baseTime.Add(-3 * time.Hour),
					To:   baseTime,
				},
				Page:     1,
				PageSize: 10,
			},
			wantLen: 0,
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Test case %s: time range from=%v to=%v",
				tt.name,
				tt.params.TimeRange.From.Format(time.RFC3339),
				tt.params.TimeRange.To.Format(time.RFC3339))
			got, err := provider.GetDashboardUsage(ctx, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDashboardUsage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			results, ok := got.Data.([]DashboardUsage)
			if !ok {
				t.Fatalf("Expected Data to be []DashboardUsage, got %T", got.Data)
			}

			if len(results) != tt.wantLen {
				t.Errorf("GetDashboardUsage() got %d results, want %d", len(results), tt.wantLen)
			}

			// Verify we got the expected dashboard IDs
			gotIDs := make([]string, len(results))
			for i, r := range results {
				gotIDs[i] = r.Id
			}

			// Check if we got all expected IDs
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetDashboardUsage() got IDs %v, want %v", gotIDs, tt.wantIDs)
				return
			}

			// Create maps for easier comparison
			gotIDMap := make(map[string]bool)
			for _, id := range gotIDs {
				gotIDMap[id] = true
			}
			for _, wantID := range tt.wantIDs {
				if !gotIDMap[wantID] {
					t.Errorf("GetDashboardUsage() missing expected ID %s", wantID)
				}
			}
		})
	}
}
