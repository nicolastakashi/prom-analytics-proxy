package db

import "time"

type Query struct {
	TS            time.Time
	QueryParam    string
	TimeParam     time.Time
	Duration      time.Duration
	StatusCode    int
	BodySize      int
	LabelMatchers []map[string]string
	Fingerprint   string
}
