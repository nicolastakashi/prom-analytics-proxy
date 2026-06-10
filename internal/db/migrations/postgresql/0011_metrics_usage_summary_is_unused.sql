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
    ADD COLUMN IF NOT EXISTS is_unused BOOLEAN NOT NULL DEFAULT TRUE;

-- ADD COLUMN ... DEFAULT TRUE already initializes every row to the unused
-- value, which is correct for any row with all-zero counts. Only the
-- used rows need flipping, so scope the UPDATE to that subset; otherwise
-- we rewrite the whole table for no semantic change.
UPDATE metrics_usage_summary
SET is_unused = FALSE
WHERE alert_count > 0
   OR record_count > 0
   OR dashboard_count > 0
   OR query_count > 0;

-- Backfill: ensure every catalog row has a summary row so the new unused
-- query can INNER JOIN safely. ON CONFLICT DO NOTHING is defensive in case
-- a refresh races the migration.
INSERT INTO metrics_usage_summary(name, alert_count, record_count, dashboard_count, query_count, updated_at, is_unused)
SELECT c.name, 0, 0, 0, 0, NOW(), TRUE
FROM   metrics_catalog c
WHERE  NOT EXISTS (SELECT 1 FROM metrics_usage_summary s WHERE s.name = c.name)
ON CONFLICT (name) DO NOTHING;

-- Partial index over the unused subset. The predicate is matched verbatim by
-- the unused branch of GetSeriesMetadata so the planner can satisfy the scan
-- from this index.
--
-- No in-line ANALYZE here. Earlier versions of this migration ran ANALYZE
-- on metrics_usage_summary and metrics_catalog at the end of the goose
-- transaction; combined with the same pattern in migration 0012 that
-- deadlocked on cx10, in-tx ANALYZE against a concurrent INSERT from the
-- inventory syncer fails when the syncer's RowExclusiveLock cannot release
-- past ANALYZE's ShareUpdateExclusiveLock. Production autovacuum refreshes
-- stats on its normal cadence; the ?usage=unused query degrades to a Hash
-- Join for at most a few minutes after deploy, which is preferable to a
-- deadlocked migration that fails the rollout.
CREATE INDEX IF NOT EXISTS idx_metrics_usage_summary_is_unused
    ON metrics_usage_summary(name)
    WHERE is_unused = TRUE;

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_usage_summary_is_unused;
ALTER TABLE metrics_usage_summary DROP COLUMN IF EXISTS is_unused;
