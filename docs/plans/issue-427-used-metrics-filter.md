# Issue 427: Used Metrics Filter

## Goal

Add a `Used Only` filter to the Metrics Catalog so users can switch between:

- `All Metrics`
- `Used Only`
- `Unused Only`

A metric counts as used when any of these summary fields is greater than zero:

- `queryCount`
- `alertCount`
- `recordCount`
- `dashboardCount`

The usage view should continue to reflect the inventory summary window, which defaults to 30 days.

## Implementation Checklist

- [x] Replace the current boolean unused filter flow with a three-state usage filter in the UI.
- [x] Update the Metrics Catalog header to expose `All Metrics`, `Used Only`, and `Unused Only`.
- [x] Extend the API client to send a usage filter parameter for series metadata.
- [x] Update the `/api/v1/seriesMetadata` route to parse the usage filter.
- [x] Extend the shared DB params model to carry the usage filter.
- [x] Add `used` and `unused` filtering logic to the SQLite series metadata query.
- [x] Add `used` and `unused` filtering logic to the PostgreSQL series metadata query.
- [x] Add route coverage for the new filter.
- [x] Add SQLite coverage for `all`, `used`, and `unused` behavior.
- [x] Add PostgreSQL coverage for `all`, `used`, and `unused` behavior.
- [x] Run targeted UI and Go tests.

## Verification

- `go test ./api/routes ./internal/db` reaches existing PostgreSQL tests that require Docker and fails in this environment because rootless Docker is unavailable.
- `go test ./internal/db -run 'TestSeriesMetadataParams_Struct|TestSQLite_GetSeriesMetadata_UsageFilters'` passed.
- `npm run lint` passed with pre-existing warnings only.
- `npm ci --legacy-peer-deps && npm run build` passed.

## Notes

- Keep `All Metrics` as the default selection.
- Metrics with no summary row should still behave as unused.
- Prefer a single usage parameter such as `usage=all|used|unused` over adding more booleans.
