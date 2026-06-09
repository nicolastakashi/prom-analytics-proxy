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

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_job_index_job_name;
