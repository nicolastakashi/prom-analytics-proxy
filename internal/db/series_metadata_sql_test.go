package db

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeriesMetadataSQLConsts_Shape asserts the structural fingerprints of
// each named SQL constant backing GetSeriesMetadata. These tests are not
// trying to validate semantics (the integration tests do that) - they exist
// so that the SQL string accidentally drifting in a way the reviewer would
// catch (e.g. losing the is_unused predicate, losing the job filter, or
// drifting placeholder count) trips a fast unit-level check before any
// container is spun up.
func TestSeriesMetadataSQLConsts_Shape(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		wantSubs []string
		// For PG we count $N placeholders; for SQLite we count `?`. Each
		// const targets one backend, so we assert exactly one of those.
		pgPlaceholderCount     int
		sqlitePlaceholderCount int
	}{
		// PostgreSQL: used / all
		{
			name: "pgSeriesMetadataCountSQL",
			sql:  pgSeriesMetadataCountSQL,
			wantSubs: []string{
				"SELECT COUNT(*)",
				"FROM metrics_catalog c",
				"LEFT JOIN metrics_usage_summary s",
				"$1", "$2", "$3", "$4",
				"COALESCE(s.alert_count,0) > 0",
				"metrics_job_index j",
				"j.job = $4",
			},
			pgPlaceholderCount: 4,
		},
		{
			name: "pgSeriesMetadataBaseSQL",
			sql:  pgSeriesMetadataBaseSQL,
			wantSubs: []string{
				"SELECT c.name, c.type, c.help, c.unit",
				"FROM metrics_catalog c",
				"LEFT JOIN metrics_usage_summary s",
				"$1", "$2", "$3", "$4",
				"j.job = $4",
			},
			pgPlaceholderCount: 4,
		},
		// PostgreSQL: unused
		{
			name: "pgSeriesMetadataUnusedCountSQL",
			sql:  pgSeriesMetadataUnusedCountSQL,
			wantSubs: []string{
				"SELECT COUNT(*)",
				"FROM metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"s.is_unused = TRUE",
				"$1", "$2",
			},
			pgPlaceholderCount: 2,
		},
		{
			name: "pgSeriesMetadataUnusedBaseSQL",
			sql:  pgSeriesMetadataUnusedBaseSQL,
			wantSubs: []string{
				"FROM metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"s.is_unused = TRUE",
				"$1", "$2",
			},
			pgPlaceholderCount: 2,
		},
		// PostgreSQL: unused + job
		{
			name: "pgSeriesMetadataUnusedJobCountSQL",
			sql:  pgSeriesMetadataUnusedJobCountSQL,
			wantSubs: []string{
				"FROM metrics_job_index j",
				"JOIN metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"j.job = $3",
				"s.is_unused = TRUE",
				"$1", "$2", "$3",
			},
			pgPlaceholderCount: 3,
		},
		{
			name: "pgSeriesMetadataUnusedJobBaseSQL",
			sql:  pgSeriesMetadataUnusedJobBaseSQL,
			wantSubs: []string{
				"FROM metrics_job_index j",
				"JOIN metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"j.job = $3",
				"s.is_unused = TRUE",
				"$1", "$2", "$3",
			},
			pgPlaceholderCount: 3,
		},
		// SQLite: used / all
		{
			name: "sqliteSeriesMetadataCountSQL",
			sql:  sqliteSeriesMetadataCountSQL,
			wantSubs: []string{
				"SELECT COUNT(*)",
				"FROM metrics_catalog c",
				"LEFT JOIN metrics_usage_summary s",
				"COALESCE(s.alert_count,0) > 0",
				"metrics_job_index j",
				"j.job = ?",
			},
			sqlitePlaceholderCount: 11,
		},
		{
			name: "sqliteSeriesMetadataBaseSQL",
			sql:  sqliteSeriesMetadataBaseSQL,
			wantSubs: []string{
				"SELECT c.name, c.type, c.help, c.unit",
				"FROM metrics_catalog AS c",
				"LEFT JOIN metrics_usage_summary AS s",
				"j.job = ?",
			},
			sqlitePlaceholderCount: 11,
		},
		// SQLite: unused
		{
			name: "sqliteSeriesMetadataUnusedCountSQL",
			sql:  sqliteSeriesMetadataUnusedCountSQL,
			wantSubs: []string{
				"FROM metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"s.is_unused = TRUE",
			},
			sqlitePlaceholderCount: 7,
		},
		{
			name: "sqliteSeriesMetadataUnusedBaseSQL",
			sql:  sqliteSeriesMetadataUnusedBaseSQL,
			wantSubs: []string{
				"FROM metrics_usage_summary AS s",
				"JOIN metrics_catalog AS c",
				"s.is_unused = TRUE",
			},
			sqlitePlaceholderCount: 7,
		},
		// SQLite: unused + job
		{
			name: "sqliteSeriesMetadataUnusedJobCountSQL",
			sql:  sqliteSeriesMetadataUnusedJobCountSQL,
			wantSubs: []string{
				"FROM metrics_job_index j",
				"JOIN metrics_usage_summary s",
				"JOIN metrics_catalog c",
				"j.job = ?",
				"s.is_unused = TRUE",
			},
			sqlitePlaceholderCount: 8,
		},
		{
			name: "sqliteSeriesMetadataUnusedJobBaseSQL",
			sql:  sqliteSeriesMetadataUnusedJobBaseSQL,
			wantSubs: []string{
				"FROM metrics_job_index AS j",
				"JOIN metrics_usage_summary AS s",
				"JOIN metrics_catalog AS c",
				"j.job = ?",
				"s.is_unused = TRUE",
			},
			sqlitePlaceholderCount: 8,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, sub := range tc.wantSubs {
				assert.Contains(t, tc.sql, sub, "missing expected substring")
			}
			if tc.pgPlaceholderCount > 0 {
				// Highest $N must equal pgPlaceholderCount and every lower
				// $i must appear at least once. The straight counter approach
				// (count $1..$N occurrences) breaks under repeated use of
				// the same placeholder, so we just assert presence/absence.
				for i := 1; i <= tc.pgPlaceholderCount; i++ {
					assert.Contains(t, tc.sql, "$"+strconv.Itoa(i),
						"expected placeholder $%d in %s", i, tc.name)
				}
				assert.NotContains(t, tc.sql, "$"+strconv.Itoa(tc.pgPlaceholderCount+1),
					"unexpected extra placeholder $%d in %s", tc.pgPlaceholderCount+1, tc.name)
			}
			if tc.sqlitePlaceholderCount > 0 {
				got := strings.Count(tc.sql, "?")
				assert.Equal(t, tc.sqlitePlaceholderCount, got,
					"placeholder count mismatch in %s", tc.name)
			}
		})
	}
}

// TestSeriesMetadataSQLConsts_PreparesSQLite ensures every SQL const targeting
// the SQLite backend (the static count strings and the dynamic
// BuildSafeQueryWithOrderBy output applied to each base string) is accepted
// by SQLite's parser against a real migrated schema. This catches regressions
// where a const accidentally references a missing column or where the
// dynamic ORDER BY emission produces a syntactically invalid query.
func TestSeriesMetadataSQLConsts_PreparesSQLite(t *testing.T) {
	p, cleanup := newTestSQLiteProvider(t)
	t.Cleanup(cleanup)

	var rawDB *sql.DB
	p.WithDB(func(db *sql.DB) { rawDB = db })
	require.NotNil(t, rawDB, "WithDB must surface the underlying *sql.DB")

	type row struct {
		name string
		sql  string
	}
	staticCounts := []row{
		{"sqliteSeriesMetadataCountSQL", sqliteSeriesMetadataCountSQL},
		{"sqliteSeriesMetadataUnusedCountSQL", sqliteSeriesMetadataUnusedCountSQL},
		{"sqliteSeriesMetadataUnusedJobCountSQL", sqliteSeriesMetadataUnusedJobCountSQL},
	}
	for _, r := range staticCounts {
		t.Run("count/"+r.name, func(t *testing.T) {
			stmt, err := rawDB.PrepareContext(context.Background(), r.sql)
			require.NoError(t, err, "%s must prepare against SQLite", r.name)
			require.NoError(t, stmt.Close())
		})
	}

	// Base queries always flow through BuildSafeQueryWithOrderBy before
	// reaching the driver. Mirror the call-site arguments precisely so we
	// catch breakage in either the const or the helper.
	baseQueries := []struct {
		name     string
		built    string
		expected string
	}{
		{
			name: "sqliteSeriesMetadataBaseSQL",
			built: BuildSafeQueryWithOrderBy(sqliteSeriesMetadataBaseSQL, "c",
				" LIMIT ? OFFSET ?", "queryCount", "desc",
				ValidSeriesMetadataSortFields, "queryCount", SeriesMetadataSortAliases),
		},
		{
			name: "sqliteSeriesMetadataUnusedBaseSQL",
			built: BuildSafeQueryWithOrderBy(sqliteSeriesMetadataUnusedBaseSQL, "c",
				" LIMIT ? OFFSET ?", "name", "desc",
				ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases),
		},
		{
			name: "sqliteSeriesMetadataUnusedJobBaseSQL",
			built: BuildSafeQueryWithOrderBy(sqliteSeriesMetadataUnusedJobBaseSQL, "c",
				" LIMIT ? OFFSET ?", "name", "desc",
				ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases),
		},
	}
	for _, q := range baseQueries {
		t.Run("base/"+q.name, func(t *testing.T) {
			assert.Contains(t, q.built, "ORDER BY",
				"built query must include ORDER BY clause")
			assert.Contains(t, q.built, "LIMIT ? OFFSET ?",
				"built query must include LIMIT clause")
			stmt, err := rawDB.PrepareContext(context.Background(), q.built)
			require.NoError(t, err, "%s must prepare against SQLite", q.name)
			require.NoError(t, stmt.Close())
		})
	}
}

// TestSeriesMetadataSQLConsts_PreparesPostgreSQL mirrors the SQLite check
// against a real PostgreSQL container. Skips automatically when Docker is
// not available (matches the pattern used by the other postgresql_test.go
// suites).
func TestSeriesMetadataSQLConsts_PreparesPostgreSQL(t *testing.T) {
	p, cleanup := newTestPostgreSQLProvider(t)
	t.Cleanup(cleanup)

	var rawDB *sql.DB
	p.WithDB(func(db *sql.DB) { rawDB = db })
	require.NotNil(t, rawDB, "WithDB must surface the underlying *sql.DB")

	staticCounts := []struct {
		name string
		sql  string
	}{
		{"pgSeriesMetadataCountSQL", pgSeriesMetadataCountSQL},
		{"pgSeriesMetadataUnusedCountSQL", pgSeriesMetadataUnusedCountSQL},
		{"pgSeriesMetadataUnusedJobCountSQL", pgSeriesMetadataUnusedJobCountSQL},
	}
	for _, r := range staticCounts {
		t.Run("count/"+r.name, func(t *testing.T) {
			stmt, err := rawDB.PrepareContext(context.Background(), r.sql)
			require.NoError(t, err, "%s must prepare against PostgreSQL", r.name)
			require.NoError(t, stmt.Close())
		})
	}

	baseQueries := []struct {
		name  string
		built string
	}{
		{
			name: "pgSeriesMetadataBaseSQL",
			built: BuildSafeQueryWithOrderBy(pgSeriesMetadataBaseSQL, "c",
				" LIMIT $5 OFFSET $6", "queryCount", "desc",
				ValidSeriesMetadataSortFields, "queryCount", SeriesMetadataSortAliases),
		},
		{
			name: "pgSeriesMetadataUnusedBaseSQL",
			built: BuildSafeQueryWithOrderBy(pgSeriesMetadataUnusedBaseSQL, "c",
				" LIMIT $3 OFFSET $4", "name", "desc",
				ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases),
		},
		{
			name: "pgSeriesMetadataUnusedJobBaseSQL",
			built: BuildSafeQueryWithOrderBy(pgSeriesMetadataUnusedJobBaseSQL, "c",
				" LIMIT $4 OFFSET $5", "name", "desc",
				ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases),
		},
	}
	for _, q := range baseQueries {
		t.Run("base/"+q.name, func(t *testing.T) {
			assert.Contains(t, q.built, "ORDER BY")
			stmt, err := rawDB.PrepareContext(context.Background(), q.built)
			require.NoError(t, err, "%s must prepare against PostgreSQL", q.name)
			require.NoError(t, stmt.Close())
		})
	}
}
