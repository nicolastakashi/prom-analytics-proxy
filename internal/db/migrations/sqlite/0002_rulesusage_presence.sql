-- +goose NO TRANSACTION
-- +goose Up
-- Introduce presence window and uniqueness for RulesUsage (SQLite)

ALTER TABLE RulesUsage 
  ADD COLUMN first_seen_at DATETIME NOT NULL DEFAULT (datetime('now'));

ALTER TABLE RulesUsage 
  ADD COLUMN last_seen_at DATETIME NOT NULL DEFAULT (datetime('now'));

UPDATE RulesUsage 
  SET first_seen_at = created_at, last_seen_at = created_at;

CREATE UNIQUE INDEX IF NOT EXISTS uq_rulesusage_version
  ON RulesUsage (serie, kind, group_name, name, expression, labels);

CREATE INDEX IF NOT EXISTS idx_rulesusage_presence
  ON RulesUsage (serie, kind, first_seen_at, last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS idx_rulesusage_presence;
DROP INDEX IF EXISTS uq_rulesusage_version;
-- SQLite cannot drop columns easily; leave columns in place on down.


