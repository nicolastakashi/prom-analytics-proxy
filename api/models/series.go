package models

type SerieMetadata struct {
	Labels      []string `json:"labels"`
	SeriesCount int      `json:"seriesCount"`
}
