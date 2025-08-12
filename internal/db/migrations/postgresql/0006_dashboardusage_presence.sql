-- +goose Up
-- Add presence tracking and uniqueness for DashboardUsage

ALTER TABLE dashboardusage 
  ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMP NOT NULL DEFAULT now();

ALTER TABLE dashboardusage 
  ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP NOT NULL DEFAULT now();

UPDATE dashboardusage SET first_seen_at = created_at, last_seen_at = created_at;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dashboardusage_identity
  ON dashboardusage (id, serie);

CREATE INDEX IF NOT EXISTS idx_dashboardusage_presence
  ON dashboardusage (serie, first_seen_at, last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS idx_dashboardusage_presence;
DROP INDEX IF EXISTS uq_dashboardusage_identity;
ALTER TABLE dashboardusage DROP COLUMN IF EXISTS last_seen_at;
ALTER TABLE dashboardusage DROP COLUMN IF EXISTS first_seen_at;


