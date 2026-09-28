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

-- Without stats on this new index, the planner underestimates the
-- presence-window filter's selectivity and keeps sequential-scanning until
-- autovacuum's own analyze cycle catches up, which on a table this size can
-- be a while. There's no ANALYZE CONCURRENTLY - ANALYZE doesn't need one:
-- it already takes only a ShareUpdateExclusiveLock, the same class
-- CREATE INDEX CONCURRENTLY uses above, which doesn't block concurrent
-- reads or writes. That's a different situation from migration 0013's
-- deadlock note: that ANALYZE ran inside one transaction alongside other
-- DDL/DML, so it queued behind a lock that same transaction was still
-- holding from an earlier statement while a concurrent write waited on it
-- too. This ANALYZE is its own separately-committed statement (still under
-- this file's NO TRANSACTION mode), so it never overlaps a lock its own
-- session is still holding from something else.
ANALYZE RulesUsage;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_rulesusage_summary_presence;
