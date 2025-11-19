package db

import (
	"time"
)

type QueryType string
type DatabaseProvider string

const (
	QueryTypeRange   QueryType        = "range"
	QueryTypeInstant QueryType        = "instant"
	PostGreSQL       DatabaseProvider = "postgresql"
	SQLite           DatabaseProvider = "sqlite"
)

var (
	KnownQueryTypes = []QueryType{QueryTypeRange, QueryTypeInstant}
)

type LabelMatchers []map[string]string

type Query struct {
	TS                    time.Time     `json:"-"`
	QueryParam            string        `json:"query_param"`
	TimeParam             time.Time     `json:"time_param"`
	Duration              time.Duration `json:"duration"`
	StatusCode            int           `json:"status_code"`
	BodySize              int           `json:"body_size"`
	LabelMatchers         LabelMatchers `json:"-"`
	Fingerprint           string        `json:"-"`
	Type                  QueryType     `json:"type"`
	Step                  float64       `json:"step"`
	Start                 time.Time     `json:"start"`
	End                   time.Time     `json:"end"`
	TotalQueryableSamples int           `json:"total_queryable_samples"`
	PeakSamples           int           `json:"peak_samples"`
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

// QueryTimeRangeDistributionResult represents a bucketed count of range query window sizes.
// Label corresponds to human-friendly bucket labels like "<24h", "24h", "7d", "30d", "60d", "90d+".
type QueryTimeRangeDistributionResult struct {
	Label   string  `json:"label"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

type MetricProducersResult struct {
	Job    string `json:"job"`
	Series int    `json:"series"`
}

type MetricUsageStatics struct {
	SerieCount      int                     `json:"serieCount"`
	LabelCount      int                     `json:"labelCount"`
	AlertCount      int                     `json:"alertCount"`
	RecordCount     int                     `json:"recordCount"`
	DashboardCount  int                     `json:"dashboardCount"`
	TotalAlerts     int                     `json:"totalAlerts"`
	TotalRecords    int                     `json:"totalRecords"`
	TotalDashboards int                     `json:"totalDashboards"`
	Producers       []MetricProducersResult `json:"producers"`
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

type RulesUsageParams struct {
	Serie     string
	Kind      string
	Filter    string
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	TimeRange TimeRange
}

type DashboardUsageParams struct {
	Serie     string
	Filter    string
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	TimeRange TimeRange
}

type MetricQueryPerformanceStatistics struct {
	QueryRate       *QueryRateResult `json:"queryRate"`
	TotalQueries    *int             `json:"totalQueries"`
	AverageSamples  *float64         `json:"averageSamples"`
	PeakSamples     *int             `json:"peakSamples"`
	AverageDuration *float64         `json:"averageDuration"`
	SampleRate      *float64         `json:"sampleRate"`
}

// QueryExpression aggregates query metrics grouped by fingerprint
type QueryExpression struct {
	Fingerprint      string  `json:"fingerprint"`
	Query            string  `json:"query"`
	Executions       int     `json:"executions"`
	AvgDuration      float64 `json:"avgDuration"`
	ErrorRatePercent float64 `json:"errorRatePercent"`
	PeakSamples      int     `json:"peakSamples"`
}
