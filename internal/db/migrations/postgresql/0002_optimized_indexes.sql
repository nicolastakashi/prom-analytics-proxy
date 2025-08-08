-- +goose Up
-- Optimized indexes and extensions for PostgreSQL

-- Enable trigram extension for GIN trgm ops
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Composite index for frequent time/status scans including useful columns
CREATE INDEX IF NOT EXISTS idx_queries_ts_status
    ON queries (ts, statusCode)
    INCLUDE (duration, peakSamples);

-- JSONB label matchers optimized for containment lookups
CREATE INDEX IF NOT EXISTS gin_queries_label
    ON queries USING gin (labelMatchers jsonb_path_ops);

-- Trigram index for case-insensitive queryParam searches
CREATE INDEX IF NOT EXISTS gin_queries_qp_trgm
    ON queries USING gin (lower(queryParam) gin_trgm_ops);

-- Window index for rules usage queries
CREATE INDEX IF NOT EXISTS idx_rulesusage_window
    ON rulesusage (created_at, serie, kind)
    INCLUDE (name);

-- Window index for dashboard usage queries
CREATE INDEX IF NOT EXISTS idx_dashusage_window
    ON dashboardusage (created_at, serie)
    INCLUDE (name);

-- +goose Down
DROP INDEX IF EXISTS idx_dashusage_window;
DROP INDEX IF EXISTS idx_rulesusage_window;
DROP INDEX IF EXISTS gin_queries_qp_trgm;
DROP INDEX IF EXISTS gin_queries_label;
DROP INDEX IF EXISTS idx_queries_ts_status;


