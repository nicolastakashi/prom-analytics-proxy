-- +goose Up
-- Schema initialization for PostgreSQL

CREATE TABLE IF NOT EXISTS queries (
    ts TIMESTAMP,
    queryParam TEXT,
    timeParam TIMESTAMP,
    duration BIGINT,
    statusCode SMALLINT,
    bodySize INTEGER,
    fingerprint TEXT,
    labelMatchers JSONB,
    type TEXT,
    step DOUBLE PRECISION,
    start TIMESTAMP,
    "end" TIMESTAMP,
    totalQueryableSamples INTEGER,
    peakSamples INTEGER
);

CREATE TABLE IF NOT EXISTS RulesUsage (
    serie TEXT NOT NULL,
    group_name TEXT NOT NULL,
    name TEXT NOT NULL,
    expression TEXT NOT NULL,
    kind TEXT NOT NULL,
    labels JSONB,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS DashboardUsage (
    id TEXT NOT NULL,
    serie TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS DashboardUsage;
DROP TABLE IF EXISTS RulesUsage;
DROP TABLE IF EXISTS queries;


