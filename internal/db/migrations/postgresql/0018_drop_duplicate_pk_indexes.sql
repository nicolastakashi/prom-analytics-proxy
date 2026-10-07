-- +goose NO TRANSACTION
-- +goose Up
-- idx_metrics_catalog_name and idx_metrics_usage_summary_counts each index
-- (name), the column their table's primary key already indexes uniquely, so
-- every lookup they serve the primary key serves identically. Each upsert
-- still writes to them. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/646.
--
-- CONCURRENTLY (hence NO TRANSACTION) so the drops don't block writes.
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_catalog_name;
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_usage_summary_counts;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_usage_summary_counts
    ON metrics_usage_summary(name);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_catalog_name
    ON metrics_catalog(name);
