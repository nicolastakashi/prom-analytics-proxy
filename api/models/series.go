package models

type SerieMetadata struct {
	Labels      []string `json:"labels"`
	SeriesCount int      `json:"seriesCount"`
}

type MetricMetadata struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Help           string `json:"help"`
	Unit           string `json:"unit"`
	AlertCount     int    `json:"alertCount,omitempty"`
	RecordCount    int    `json:"recordCount,omitempty"`
	DashboardCount int    `json:"dashboardCount,omitempty"`
	QueryCount     int    `json:"queryCount,omitempty"`
	LastQueriedAt  string `json:"lastQueriedAt,omitempty"`
	// IsUnused reflects metrics_usage_summary.is_unused as evaluated by
	// RefreshMetricsUsageSummary. It is the single source of truth for
	// whether a metric is confirmed unused - callers must not infer
	// "unused" from AlertCount/RecordCount/DashboardCount/QueryCount being
	// zero, since zero counts are also what an unevaluated metric looks
	// like before its first RefreshMetricsUsageSummary run.
	IsUnused bool `json:"isUnused,omitempty"`
}
