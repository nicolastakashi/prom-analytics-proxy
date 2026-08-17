-- +goose NO TRANSACTION
-- +goose Up
-- Covering index backing RefreshMetricsUsageSummary's "only recompute
-- actively-synced metrics" filter. The query filters metrics_catalog on
-- last_synced_at and only ever needs name from that table (for the join to
-- RulesUsage/DashboardUsage/queries), so name rides along via INCLUDE
-- rather than as a full key column - it doesn't need to participate in
-- ordering or uniqueness, just be present at the leaf level so the planner
-- can satisfy the scan as an index-only scan. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/579.
--
-- CREATE INDEX CONCURRENTLY (hence the disabled goose transaction, and no
-- in-line ANALYZE - see migration 0013's note on the RowExclusiveLock vs
-- ShareUpdateExclusiveLock deadlock this avoids) so the build does not
-- block writes from the inventory syncer while it runs.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_catalog_last_synced_at
    ON metrics_catalog (last_synced_at) INCLUDE (name);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_catalog_last_synced_at;
