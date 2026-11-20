package otlp

import (
	"context"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

// Store is the database the ingester reads metric usage from and writes the
// metric catalog to.
type Store interface {
	GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error)
	UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error
}
