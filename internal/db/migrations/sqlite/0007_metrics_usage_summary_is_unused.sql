-- +goose NO TRANSACTION
-- +goose Up
-- Add is_unused as a directly indexable property of metrics_usage_summary so
-- that GET /api/v1/seriesMetadata?usage=unused can be driven from the summary
-- table via a partial index instead of scanning metrics_catalog and filtering
-- with a four-way COALESCE-on-zero predicate.
--
-- Default TRUE: a brand-new row with all counts zero is unused by definition.
-- RefreshMetricsUsageSummary maintains the column going forward; the UPDATE
-- below brings any rows that existed before this migration into alignment.
ALTER TABLE metrics_usage_summary
    ADD COLUMN is_unused BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE metrics_usage_summary
SET is_unused = (alert_count = 0
                 AND record_count = 0
                 AND dashboard_count = 0
                 AND query_count = 0);

-- Backfill: ensure every catalog row has a summary row so the new unused
-- query can INNER JOIN safely. INSERT OR IGNORE is defensive in case a
-- refresh races the migration.
INSERT OR IGNORE INTO metrics_usage_summary(
    name, alert_count, record_count, dashboard_count, query_count, updated_at, is_unused
)
SELECT c.name, 0, 0, 0, 0, datetime('now'), 1
FROM   metrics_catalog c
WHERE  NOT EXISTS (SELECT 1 FROM metrics_usage_summary s WHERE s.name = c.name);

-- Partial index over the unused subset. The predicate is matched verbatim by
-- the unused branch of GetSeriesMetadata so the planner can satisfy the scan
-- from this index. SQLite has supported partial indexes since 3.8.0.
CREATE INDEX IF NOT EXISTS idx_metrics_usage_summary_is_unused
    ON metrics_usage_summary(name)
    WHERE is_unused = TRUE;

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_usage_summary_is_unused;
ALTER TABLE metrics_usage_summary DROP COLUMN is_unused;
