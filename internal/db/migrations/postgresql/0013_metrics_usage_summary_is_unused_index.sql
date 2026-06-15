-- +goose NO TRANSACTION
-- +goose Up
-- Partial index over the unused subset of metrics_usage_summary. The
-- predicate matches the unused branch of GetSeriesMetadata verbatim so the
-- planner can satisfy the scan from this index instead of walking the full
-- summary table.
--
-- CREATE INDEX CONCURRENTLY (which is why this migration disables the
-- goose-wrapping transaction) so the index build does not block writes
-- from the inventory syncer while the migration runs. Mirrors migration
-- 0012's pattern. An earlier form ran the index build in 0011's transaction
-- alongside ADD COLUMN + UPDATE + INSERT, which combined with in-tx ANALYZE
-- deadlocked on cx10: a concurrent INSERT from the inventory syncer held a
-- RowExclusiveLock that the in-tx ANALYZE's ShareUpdateExclusiveLock could
-- not acquire. No in-line ANALYZE here; production autovacuum refreshes
-- stats on its normal cadence.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_usage_summary_is_unused
    ON metrics_usage_summary(name)
    WHERE is_unused = TRUE;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_usage_summary_is_unused;
