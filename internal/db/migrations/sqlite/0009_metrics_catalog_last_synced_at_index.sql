-- +goose Up
-- Covering index backing RefreshMetricsUsageSummary's "only recompute
-- actively-synced metrics" filter: the query filters on last_synced_at but
-- only ever needs name from metrics_catalog, so name rides along as a
-- second key column (SQLite has no INCLUDE clause) to let the planner
-- satisfy the scan straight from the index. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/579.
CREATE INDEX IF NOT EXISTS idx_metrics_catalog_last_synced_at
    ON metrics_catalog(last_synced_at, name);

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_catalog_last_synced_at;
