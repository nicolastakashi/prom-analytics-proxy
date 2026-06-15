-- +goose Up
-- Composite index over (job, name) on metrics_job_index. The existing
-- idx_metrics_job_index_job covers j.job lookups but only stores rowids;
-- joining to metrics_usage_summary / metrics_catalog by j.name still
-- requires a row fetch per outer row. (job, name) lets the job-scoped
-- ?usage=unused query do a covering scan over the job's metrics and feed
-- names directly into the nested-loop joins to summary and catalog.
--
-- SQLite has supported partial and composite indexes since well before
-- our minimum version.
CREATE INDEX IF NOT EXISTS idx_metrics_job_index_job_name
    ON metrics_job_index(job, name);

-- Drop the redundant single-column index from migration 0005. SQLite uses
-- the leftmost prefix of a composite index for single-column lookups, so
-- idx_metrics_job_index_job_name(job, name) covers every plan that
-- idx_metrics_job_index_job(job) covered. Keeping both costs disk and
-- write amplification with no read-side benefit.
DROP INDEX IF EXISTS idx_metrics_job_index_job;

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_job_index_job_name;
CREATE INDEX IF NOT EXISTS idx_metrics_job_index_job
    ON metrics_job_index(job);
