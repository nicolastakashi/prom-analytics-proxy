package models

type UnusedSummary struct {
	AlertCount     int `json:"alert_count"`
	RecordCount    int `json:"record_count"`
	DashboardCount int `json:"dashboard_count"`
	QueryCount     int `json:"query_count"`
}

type UnusedMetric struct {
	Name    string        `json:"name"`
	Unused  bool          `json:"unused"`
	Summary UnusedSummary `json:"summary"`
}
