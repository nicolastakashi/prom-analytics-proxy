-- +goose NO TRANSACTION
-- +goose Up
-- Covering index for RefreshMetricsUsageSummary's RulesUsage subquery: it
-- aggregates GROUP BY serie with no serie/kind predicate, so
-- idx_rulesusage_presence (serie, kind, first_seen_at, last_seen_at) can't
-- support it and the planner falls back to a full sequential scan.
-- idx_rulesusage_presence stays untouched - splitting it would regress
-- GetRulesUsage's equality-prefix lookup. Leading on last_seen_at makes the
-- range condition indexable; serie/kind ride along via INCLUDE for an
-- index-only scan. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/589.
--
-- CREATE INDEX CONCURRENTLY (hence NO TRANSACTION, and no in-line ANALYZE -
-- see migration 0013's deadlock note) so the build doesn't block writes
-- from the inventory syncer.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rulesusage_summary_presence
    ON RulesUsage (last_seen_at, first_seen_at) INCLUDE (serie, kind);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_rulesusage_summary_presence;
