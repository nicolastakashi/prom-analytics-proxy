-- +goose Up
-- Backs internal/leaderelection's lease-based leader-election strategy
-- (see docs/leader-election.md). A lease's validity is purely a timestamp
-- comparison, decoupled from any connection's physical lifecycle: unlike a
-- session-level advisory lock, a row here can't be left "held by nothing"
-- if its owning process disappears — it just stops being renewed and
-- expires_at eventually passes.
--
-- Postgres-only, no SQLite counterpart: only used when
-- -leader-election-strategy=lease, which is itself only ever selected on
-- PostgreSQL deployments (SQLite is single-instance-per-file in this
-- project's deployment model, so it has no leader-election mechanism at
-- all). Created unconditionally here regardless of which strategy is
-- selected, matching how the rest of this schema works — one small,
-- empty, unused table on advisory-lock deployments is a negligible cost
-- next to having schema state depend on which feature flag happens to be
-- set.
-- fence_token is sourced from this sequence rather than a per-row counter
-- so it stays monotonic even across the row being deleted and recreated
-- (see docs/leader-election.md for why that matters). OWNED BY ties the
-- sequence's lifecycle to the column below, so dropping the table (this
-- migration's Down) drops it too.
CREATE SEQUENCE IF NOT EXISTS leader_lease_fence_token_seq;

CREATE TABLE IF NOT EXISTS leader_leases (
    lease_name  TEXT PRIMARY KEY,
    holder_id   TEXT NOT NULL,
    fence_token BIGINT NOT NULL DEFAULT nextval('leader_lease_fence_token_seq'),
    expires_at  TIMESTAMPTZ NOT NULL
);

ALTER SEQUENCE leader_lease_fence_token_seq OWNED BY leader_leases.fence_token;

-- +goose Down
DROP TABLE IF EXISTS leader_leases;
