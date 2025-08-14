-- +goose Up
-- Metrics job index table for PostgreSQL

CREATE TABLE IF NOT EXISTS metrics_job_index (
  name TEXT NOT NULL,
  job TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT now(),
  PRIMARY KEY (name, job)
);

CREATE INDEX IF NOT EXISTS idx_metrics_job_index_job ON metrics_job_index(job);
CREATE INDEX IF NOT EXISTS idx_metrics_job_index_name ON metrics_job_index(name);

-- +goose Down
DROP TABLE IF EXISTS metrics_job_index;

