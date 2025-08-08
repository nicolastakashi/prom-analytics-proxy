-- +goose NO TRANSACTION
-- +goose Up
VACUUM ANALYZE rulesusage;
VACUUM ANALYZE dashboardusage;
VACUUM ANALYZE queries;

-- +goose Down
-- No-op


