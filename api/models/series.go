package models

type SerieMetadata struct {
	Labels      []string `json:"labels"`
	SeriesCount int      `json:"seriesCount"`
}

type MetricMetadata struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}
