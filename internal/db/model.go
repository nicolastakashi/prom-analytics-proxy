package db

import (
	"time"
)

type QueryType string
type DatabaseProvider string

const (
	QueryTypeRange   QueryType        = "range"
	QueryTypeInstant QueryType        = "instant"
	ClickHouse       DatabaseProvider = "clickhouse"
	PostGreSQL       DatabaseProvider = "postgresql"
	SQLite           DatabaseProvider = "sqlite"
)

type LabelMatchers []map[string]string

type Query struct {
	TS                    time.Time
	QueryParam            string
	TimeParam             time.Time
	Duration              time.Duration
	StatusCode            int
	BodySize              int
	LabelMatchers         LabelMatchers
	Fingerprint           string
	Type                  QueryType
	Step                  float64
	Start                 time.Time
	End                   time.Time
	TotalQueryableSamples int
	PeakSamples           int
}

type QueryResult struct {
	Columns []string                 `json:"columns"`
	Data    []map[string]interface{} `json:"data"`
}

type QueryTypesResult struct {
	TotalQueries   *int     `json:"total_queries"`
	InstantPercent *float64 `json:"instant_percent"`
	RangePercent   *float64 `json:"range_percent"`
}

type AverageDurationResult struct {
	AvgDuration  *float64 `json:"avg_duration"`
	DeltaPercent *float64 `json:"delta_percent"`
}

type QueryRateResult struct {
	SuccessTotal       *int     `json:"success_total"`
	SuccessRatePercent *float64 `json:"success_rate_percent"`
	ErrorTotal         *int     `json:"error_total"`
	ErrorRatePercent   *float64 `json:"error_rate_percent"`
}

type QueryShortCut struct {
	Title string `json:"title"`
	Query string `json:"query"`
}

type PagedResult struct {
	TotalPages int         `json:"totalPages"`
	Total      int         `json:"total"`
	Data       interface{} `json:"data"`
}

type QueriesBySerieNameResult struct {
	Query           string    `json:"query"`
	AvgDuration     float64   `json:"avgDuration"`
	AvgPeakySamples float64   `json:"avgPeakySamples"`
	MaxPeakSamples  int       `json:"maxPeakSamples"`
	TS              time.Time `json:"ts"`
}

type QueryStatusDistributionResult struct {
	Time      string `json:"time"`
	Status2xx int    `json:"2xx"`
	Status4xx int    `json:"4xx"`
	Status5xx int    `json:"5xx"`
}

type QueryLatencyTrendsResult struct {
	Value float64 `json:"value"`
	P95   int     `json:"p95"`
	Time  string  `json:"time"`
}

type QueryThroughputAnalysisResult struct {
	Value float64 `json:"value"`
	Time  string  `json:"time"`
}

type QueryErrorAnalysisResult struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

type MetricUsageStatics struct {
	SerieCount      int `json:"serieCount"`
	LabelCount      int `json:"labelCount"`
	AlertCount      int `json:"alertCount"`
	RecordCount     int `json:"recordCount"`
	DashboardCount  int `json:"dashboardCount"`
	TotalAlerts     int `json:"totalAlerts"`
	TotalRecords    int `json:"totalRecords"`
	TotalDashboards int `json:"totalDashboards"`
}

type RuleUsageKind string

const (
	RuleUsageKindAlert  RuleUsageKind = "alert"
	RuleUsageKindRecord RuleUsageKind = "record"
)

type RulesUsage struct {
	Serie      string    `json:"serie"`
	GroupName  string    `json:"group_name"`
	Name       string    `json:"name"`
	Expression string    `json:"expression"`
	Kind       string    `json:"kind"`
	Labels     []string  `json:"labels"`
	CreatedAt  time.Time `json:"created_at"`
}

type DashboardUsage struct {
	Id        string    `json:"id"`
	Serie     string    `json:"serie"`
	Name      string    `json:"title"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type MetricQueryPerformanceStatistics struct {
	QueryRate       *QueryRateResult `json:"queryRate"`
	TotalQueries    *int             `json:"totalQueries"`
	AverageSamples  *float64         `json:"averageSamples"`
	PeakSamples     *int             `json:"peakSamples"`
	AverageDuration *float64         `json:"averageDuration"`
	SampleRate      *float64         `json:"sampleRate"`
}
