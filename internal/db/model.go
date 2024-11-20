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
	QueryParam      string    `json:"queryParam"`
	AvgDuration     float64   `json:"avgDuration"`
	AvgPeakySamples float64   `json:"avgPeakySamples"`
	MaxPeakSamples  int       `json:"maxPeakSamples"`
	TS              time.Time `json:"ts"`
}
