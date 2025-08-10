-- +goose Up
-- Introduce presence window and uniqueness for RulesUsage

-- Add presence columns
ALTER TABLE rulesusage 
    ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMP NOT NULL DEFAULT now();

ALTER TABLE rulesusage 
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP NOT NULL DEFAULT now();

-- Backfill from created_at so presence matches historical creation time
UPDATE rulesusage 
SET first_seen_at = created_at, last_seen_at = created_at;

-- Unique version by identity+content to prevent duplicates
CREATE UNIQUE INDEX IF NOT EXISTS uq_rulesusage_version
    ON rulesusage (serie, kind, group_name, name, expression, (labels::text));

-- Presence query support (overlap scans)
CREATE INDEX IF NOT EXISTS idx_rulesusage_presence
    ON rulesusage (serie, kind, first_seen_at, last_seen_at);

-- +goose Down
-- Best-effort rollback. Drop indexes and columns.
DROP INDEX IF EXISTS idx_rulesusage_presence;
DROP INDEX IF EXISTS uq_rulesusage_version;
ALTER TABLE rulesusage DROP COLUMN IF EXISTS last_seen_at;
ALTER TABLE rulesusage DROP COLUMN IF EXISTS first_seen_at;


