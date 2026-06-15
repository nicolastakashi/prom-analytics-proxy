-- +goose NO TRANSACTION
-- +goose Up
-- Composite index over (job, name) on metrics_job_index. The existing
-- idx_metrics_job_index_job covers j.job lookups but only stores rowids;
-- joining to metrics_usage_summary / metrics_catalog by j.name still
-- requires a heap fetch per outer row. (job, name) lets the job-scoped
-- ?usage=unused query do an index-only scan over the job's metrics and
-- feed names directly into the nested-loop joins to summary and catalog.
--
-- Without this index a query like
--   FROM metrics_job_index j
--   JOIN metrics_usage_summary s ON s.name = j.name
--   JOIN metrics_catalog c ON c.name = j.name
--   WHERE j.job = $3 AND s.is_unused = TRUE
-- still works but pays a heap fetch per j.name; with it, the planner can
-- pick an index-only scan on metrics_job_index for the j.job=... probe
-- and join straight to summary/catalog by PK.
--
-- CREATE INDEX CONCURRENTLY (which is why this migration disables the
-- goose-wrapping transaction) so the index build does not block writes
-- from the inventory syncer while the migration runs. An earlier form
-- of this migration ran transactional CREATE INDEX followed by ANALYZE
-- and deadlocked on cx10: a concurrent INSERT into metrics_job_index
-- held a RowExclusiveLock that the in-tx ANALYZE's
-- ShareUpdateExclusiveLock could not acquire. Planner stats refresh
-- via autovacuum on its normal cadence; no in-line ANALYZE here.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_job_index_job_name
    ON metrics_job_index(job, name);

-- Drop the redundant single-column index from migration 0008. PostgreSQL
-- uses the leftmost prefix of a composite index for single-column lookups,
-- so idx_metrics_job_index_job_name(job, name) covers every plan that
-- idx_metrics_job_index_job(job) covered. Keeping both costs disk and
-- write amplification with no read-side benefit.
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_job_index_job;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_metrics_job_index_job_name;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metrics_job_index_job
    ON metrics_job_index(job);
