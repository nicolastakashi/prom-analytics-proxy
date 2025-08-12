-- +goose NO TRANSACTION
-- +goose Up
-- Add presence tracking and uniqueness for DashboardUsage (SQLite)

ALTER TABLE DashboardUsage 
  ADD COLUMN first_seen_at DATETIME NOT NULL DEFAULT (datetime('now'));

ALTER TABLE DashboardUsage 
  ADD COLUMN last_seen_at DATETIME NOT NULL DEFAULT (datetime('now'));

UPDATE DashboardUsage SET first_seen_at = created_at, last_seen_at = created_at;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dashboardusage_identity
  ON DashboardUsage (id, serie);

CREATE INDEX IF NOT EXISTS idx_dashboardusage_presence
  ON DashboardUsage (serie, first_seen_at, last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS idx_dashboardusage_presence;
DROP INDEX IF EXISTS uq_dashboardusage_identity;
-- Keep columns on down due to SQLite limitations


