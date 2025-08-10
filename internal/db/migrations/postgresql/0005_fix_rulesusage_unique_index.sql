-- +goose Up
-- Align unique index with ON CONFLICT target (no expression casts)

DROP INDEX IF EXISTS uq_rulesusage_version;

CREATE UNIQUE INDEX IF NOT EXISTS uq_rulesusage_version
  ON rulesusage (serie, kind, group_name, name, expression, labels);

-- +goose Down
DROP INDEX IF EXISTS uq_rulesusage_version;


