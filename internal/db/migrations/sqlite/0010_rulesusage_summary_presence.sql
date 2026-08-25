-- +goose Up
-- Covering index for RefreshMetricsUsageSummary's RulesUsage subquery: it
-- aggregates GROUP BY serie with no serie/kind predicate, so
-- idx_rulesusage_presence (serie, kind, first_seen_at, last_seen_at) can't
-- support it and the planner falls back to a full table scan.
-- idx_rulesusage_presence stays untouched - splitting it would regress
-- GetRulesUsage's equality-prefix lookup. Leading on last_seen_at makes the
-- range condition indexable; serie/kind ride along as trailing key columns
-- (no INCLUDE clause in SQLite) so the aggregate is still satisfiable from
-- the index alone. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/589.
CREATE INDEX IF NOT EXISTS idx_rulesusage_summary_presence
    ON RulesUsage(last_seen_at, first_seen_at, serie, kind);

-- +goose Down
DROP INDEX IF EXISTS idx_rulesusage_summary_presence;
