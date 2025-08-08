-- +goose NO TRANSACTION
-- +goose Up
-- Schema initialization for SQLite

CREATE TABLE IF NOT EXISTS queries (
    ts TIMESTAMP,
    queryParam TEXT,
    timeParam TIMESTAMP,
    duration INTEGER,
    statusCode INTEGER,
    bodySize INTEGER,
    fingerprint TEXT,
    labelMatchers TEXT,
    type TEXT,
    step REAL,
    start TIMESTAMP,
    "end" TIMESTAMP,
    totalQueryableSamples INTEGER,
    peakSamples INTEGER
);

PRAGMA journal_mode = WAL;
PRAGMA synchronous = normal;
PRAGMA journal_size_limit = 6144000;

CREATE TABLE IF NOT EXISTS RulesUsage (
    serie TEXT NOT NULL,
    group_name TEXT NOT NULL,
    name TEXT NOT NULL,
    expression TEXT NOT NULL,
    kind TEXT NOT NULL,
    labels TEXT,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS DashboardUsage (
    id TEXT NOT NULL,
    serie TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS DashboardUsage;
DROP TABLE IF EXISTS RulesUsage;
DROP TABLE IF EXISTS queries;


