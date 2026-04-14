package routes

import (
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

type QueryExpressionsResponse struct {
	TotalPages int                  `json:"totalPages"`
	Total      int                  `json:"total"`
	Data       []db.QueryExpression `json:"data"`
}

type QueryExecutionsResponse struct {
	TotalPages int                    `json:"totalPages"`
	Total      int                    `json:"total"`
	Data       []db.QueryExecutionRow `json:"data"`
}

type SeriesMetadataResponse struct {
	TotalPages int                     `json:"totalPages"`
	Total      int                     `json:"total"`
	Data       []models.MetricMetadata `json:"data"`
}

type SerieExpressionsResponse struct {
	TotalPages int                           `json:"totalPages"`
	Total      int                           `json:"total"`
	Data       []db.QueriesBySerieNameResult `json:"data"`
}

type MetricUsageItem struct {
	ID         string    `json:"id,omitempty"`
	Serie      string    `json:"serie,omitempty"`
	Title      string    `json:"title,omitempty"`
	URL        string    `json:"url,omitempty"`
	GroupName  string    `json:"group_name,omitempty"`
	Name       string    `json:"name,omitempty"`
	Expression string    `json:"expression,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type MetricUsageResponse struct {
	TotalPages int               `json:"totalPages"`
	Total      int               `json:"total"`
	Data       []MetricUsageItem `json:"data"`
}
