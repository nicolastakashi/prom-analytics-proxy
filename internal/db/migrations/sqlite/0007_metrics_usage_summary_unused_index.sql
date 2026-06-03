-- +goose NO TRANSACTION
-- +goose Up
-- Partial index over the "unused" subset of metrics_usage_summary
-- (alert/record/dashboard/query counts all zero) so that
-- GET /api/v1/seriesMetadata?usage=unused can index-scan the unused
-- rows directly instead of joining metrics_catalog to
-- metrics_usage_summary and filtering with a 4-way COALESCE predicate.
--
-- SQLite has supported partial indexes since 3.8.0. The index predicate
-- is matched verbatim by an EXISTS subquery in GetSeriesMetadata.
CREATE INDEX IF NOT EXISTS idx_metrics_usage_summary_unused
  ON metrics_usage_summary(name)
  WHERE alert_count = 0
    AND record_count = 0
    AND dashboard_count = 0
    AND query_count = 0;

-- Backfill: ensure every existing catalog row has a metrics_usage_summary
-- row, so the post-rewrite unused predicate is semantically equivalent
-- to the old COALESCE(...) = 0 form for pre-existing rows.
INSERT OR IGNORE INTO metrics_usage_summary(
    name, alert_count, record_count, dashboard_count, query_count, updated_at
)
SELECT c.name, 0, 0, 0, 0, datetime('now')
FROM   metrics_catalog c
WHERE  NOT EXISTS (SELECT 1 FROM metrics_usage_summary s WHERE s.name = c.name);

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_usage_summary_unused;
