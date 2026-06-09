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
CREATE INDEX IF NOT EXISTS idx_metrics_job_index_job_name
    ON metrics_job_index(job, name);

ANALYZE metrics_job_index;

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_job_index_job_name;
