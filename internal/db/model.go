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
