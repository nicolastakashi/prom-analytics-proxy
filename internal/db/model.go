package db

import "time"

type QueryType string

const (
	QueryTypeRange   QueryType = "range"
	QueryTypeInstant QueryType = "instant"
)

type Query struct {
	TS                    time.Time
	QueryParam            string
	TimeParam             time.Time
	Duration              time.Duration
	StatusCode            int
	BodySize              int
	LabelMatchers         []map[string]string
	Fingerprint           string
	Type                  QueryType
	Step                  float64
	Start                 time.Time
	End                   time.Time
	TotalQueryableSamples int
	PeakSamples           int
}
