-- +goose Up
-- Inventory catalog and summary tables for PostgreSQL

CREATE TABLE IF NOT EXISTS metrics_catalog (
  name TEXT PRIMARY KEY,
  type TEXT,
  help TEXT,
  unit TEXT,
  first_seen_at TIMESTAMP NOT NULL DEFAULT now(),
  last_synced_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS metrics_usage_summary (
  name TEXT PRIMARY KEY REFERENCES metrics_catalog(name) ON DELETE CASCADE,
  alert_count INTEGER NOT NULL DEFAULT 0,
  record_count INTEGER NOT NULL DEFAULT 0,
  dashboard_count INTEGER NOT NULL DEFAULT 0,
  query_count INTEGER NOT NULL DEFAULT 0,
  last_queried_at TIMESTAMP NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_metrics_catalog_name ON metrics_catalog(name);
CREATE INDEX IF NOT EXISTS idx_metrics_catalog_type ON metrics_catalog(type);
CREATE INDEX IF NOT EXISTS idx_metrics_usage_summary_counts ON metrics_usage_summary(name);

-- +goose Down
DROP TABLE IF EXISTS metrics_usage_summary;
DROP TABLE IF EXISTS metrics_catalog;


