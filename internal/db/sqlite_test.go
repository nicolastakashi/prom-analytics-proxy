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
