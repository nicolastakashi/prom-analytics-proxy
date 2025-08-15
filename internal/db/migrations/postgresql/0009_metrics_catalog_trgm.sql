-- +goose Up
-- Trigram indexes to speed up ILIKE filters on metrics_catalog

-- Requires pg_trgm extension (created in 0002_optimized_indexes.sql)
CREATE INDEX IF NOT EXISTS gin_metrics_catalog_name_trgm
  ON metrics_catalog USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS gin_metrics_catalog_help_trgm
  ON metrics_catalog USING gin (help gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS gin_metrics_catalog_help_trgm;
DROP INDEX IF EXISTS gin_metrics_catalog_name_trgm;


