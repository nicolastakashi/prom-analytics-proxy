-- +goose NO TRANSACTION
-- +goose Up
-- gin_queries_qp_trgm indexes lower(queryParam), but every queryParam text
-- filter matches the raw column (queryParam ILIKE ...), so no query can use
-- it. It is still maintained on every insert and retention delete. See
-- https://github.com/nicolastakashi/prom-analytics-proxy/issues/600.
--
-- CONCURRENTLY (hence NO TRANSACTION) so the drop doesn't block writes.
DROP INDEX CONCURRENTLY IF EXISTS gin_queries_qp_trgm;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS gin_queries_qp_trgm
    ON queries USING gin (lower(queryParam) gin_trgm_ops);
