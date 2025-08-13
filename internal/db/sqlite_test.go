package db

import (
	"context"
	"testing"
	"time"
)

// Basic integration test exercising catalog upsert, summary refresh and list.
func TestSQLite_MetricsInventoryAndList(t *testing.T) {
	ctx := context.Background()
	provider, err := newSqliteProvider(ctx)
	if err != nil {
		t.Fatalf("newSqliteProvider: %v", err)
	}
	defer provider.Close()

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
