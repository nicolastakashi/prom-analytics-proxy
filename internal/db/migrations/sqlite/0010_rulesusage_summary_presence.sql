-- +goose Up
-- Covering index for RefreshMetricsUsageSummary's RulesUsage subquery: it
-- aggregates GROUP BY serie with no serie/kind predicate, so
-- idx_rulesusage_presence (serie, kind, first_seen_at, last_seen_at) can't
-- use first_seen_at/last_seen_at to seek - they aren't leading columns.
-- SQLite still covers the query from that index rather than the table (a
-- covering index scan, not a table scan), but walks every row instead of
-- just the ones inside the presence window. idx_rulesusage_presence stays
-- untouched - splitting it would regress GetRulesUsage's equality-prefix
-- lookup. Leading on last_seen_at here makes the range condition seekable
-- instead; serie/kind ride along as trailing key columns (no INCLUDE
-- clause in SQLite) so the aggregate stays satisfiable from the index
-- alone.
--
-- SQLite's planner doesn't choose this index over idx_rulesusage_presence
-- on its own, though - forcing it with INDEXED BY measurably wins in
-- benchmarks. Nothing here does that yet. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/589.
CREATE INDEX IF NOT EXISTS idx_rulesusage_summary_presence
    ON RulesUsage(last_seen_at, first_seen_at, serie, kind);

-- +goose Down
DROP INDEX IF EXISTS idx_rulesusage_summary_presence;
