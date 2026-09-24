package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// QueryBuildingContext holds the context for building SQL queries
type QueryBuildingContext struct {
	Dialect       string
	PlaceholderFn func(int) string
}

// NewPostgreSQLQueryContext creates a query context for PostgreSQL
func NewPostgreSQLQueryContext() *QueryBuildingContext {
	return &QueryBuildingContext{
		Dialect: "postgresql",
		PlaceholderFn: func(i int) string {
			return fmt.Sprintf("$%d", i)
		},
	}
}

// NewSQLiteQueryContext creates a query context for SQLite
func NewSQLiteQueryContext() *QueryBuildingContext {
	return &QueryBuildingContext{
		Dialect: "sqlite",
		PlaceholderFn: func(i int) string {
			return "?"
		},
	}
}

// CreateInsertPlaceholders builds the appropriate placeholders for an INSERT statement
func (qc *QueryBuildingContext) CreateInsertPlaceholders(columns, rows int) (string, []interface{}, error) {
	if qc.Dialect == "postgresql" {
		return createPostgreSQLInsertPlaceholders(columns, rows)
	}
	return createSQLiteInsertPlaceholders(columns, rows)
}

// ParseJSONParams is a helper for handling JSON parameters
func ParseJSONParams(params interface{}) (string, error) {
	if params == nil {
		return "", nil
	}
	jsonData, err := json.Marshal(params)
	if err != nil {
		return "", ErrorWithOperation(err, "marshaling JSON params")
	}
	return string(jsonData), nil
}

// Helper for SQLite insert placeholders
func createSQLiteInsertPlaceholders(columns, rows int) (string, []interface{}, error) {
	placeholders := ""
	values := make([]interface{}, 0, columns*rows)

	singleRowPlaceholders := "(" + "?, "
	for i := 1; i < columns; i++ {
		if i == columns-1 {
			singleRowPlaceholders += "?)"
		} else {
			singleRowPlaceholders += "?, "
		}
	}

	for i := 0; i < rows; i++ {
		placeholders += singleRowPlaceholders
		if i < rows-1 {
			placeholders += ", "
		}
	}

	return placeholders, values, nil
}

// Helper for PostgreSQL insert placeholders
func createPostgreSQLInsertPlaceholders(columns, rows int) (string, []interface{}, error) {
	placeholders := ""
	values := make([]interface{}, 0, columns*rows)

	for i := 0; i < rows; i++ {
		placeholders += "("
		for j := 0; j < columns; j++ {
			placeholders += fmt.Sprintf("$%d", i*columns+j+1)
			if j < columns-1 {
				placeholders += ", "
			}
		}
		placeholders += ")"

		if i < rows-1 {
			placeholders += ", "
		}
	}

	return placeholders, values, nil
}

// ExecuteQuery is a helper function to execute a query with error handling
func ExecuteQuery(ctx context.Context, db *sql.DB, query string, args ...interface{}) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, QueryError(err, "executing query", query)
	}
	return rows, nil
}

// CloseResource safely closes a resource and logs any errors
func CloseResource(closer io.Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		// Log the error but don't panic
		slog.Error("Error closing resource", "error", err)
	}
}

// ScanSingleRow scans a single row with proper error handling
func ScanSingleRow(rows *sql.Rows, dest ...interface{}) error {
	defer CloseResource(rows)

	if !rows.Next() {
		return ErrNoResults
	}

	if err := rows.Scan(dest...); err != nil {
		return ErrorWithOperation(err, "scanning row")
	}

	if err := rows.Err(); err != nil {
		return ErrorWithOperation(err, "row iteration")
	}

	return nil
}

// countMatching runs a standalone COUNT query and returns the scalar result.
// A paginated method must run this independently of its paged query - a
// count read off a paged row is wrong once LIMIT/OFFSET selects zero rows
// (a page past the last one), since there is then no row left to read it
// from.
func countMatching(ctx context.Context, db *sql.DB, countQuery string, args ...interface{}) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// PrepareTimeRange formats time range parameters based on the database dialect
func PrepareTimeRange(tr TimeRange, dialect string) (string, string) {
	if dialect == "postgresql" {
		return tr.Format(ISOTimeFormat)
	}
	return tr.Format(SQLiteTimeFormat)
}

// warnIfSummaryRefreshWasNoOp logs when RefreshMetricsUsageSummary's INSERT
// touched zero rows while metrics_catalog is non-empty. That combination
// means every catalog row failed the last_synced_at freshness filter (see
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/579) - most
// likely metadata sync has stopped advancing last_synced_at (disabled, or a
// seen_ttl raised above inventory.time_window) - which otherwise surfaces as
// a silently frozen summary with no error, the same failure class that issue
// was about.
func warnIfSummaryRefreshWasNoOp(ctx context.Context, db *sql.DB, rowsAffected int64, dialect string) {
	if rowsAffected > 0 {
		return
	}
	var catalogNonEmpty bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metrics_catalog)`).Scan(&catalogNonEmpty); err != nil {
		return // best-effort diagnostic; don't fail the refresh over it
	}
	if catalogNonEmpty {
		slog.Warn("inventory: refresh summary touched no rows despite a non-empty catalog; "+
			"every row failed the last_synced_at freshness filter - check that metadata sync is advancing last_synced_at",
			"dialect", dialect)
	}
}

// GetInterval returns the appropriate interval string for time-based queries
func GetInterval(from, to time.Time, dialect string) string {
	duration := to.Sub(from)
	var interval string

	switch {
	case duration <= 2*time.Hour:
		interval = "1 minute"
	case duration <= 6*time.Hour:
		interval = "5 minutes"
	case duration <= 24*time.Hour:
		interval = "15 minutes"
	case duration <= 7*24*time.Hour:
		interval = "1 hour"
	case duration <= 30*24*time.Hour:
		interval = "6 hours"
	case duration <= 90*24*time.Hour:
		interval = "1 day"
	default:
		interval = "1 day"
	}

	if dialect == "postgresql" {
		return interval
	}

	// SQLite has a different interval syntax
	switch interval {
	case "1 minute":
		return "+1 minutes"
	case "5 minutes":
		return "+5 minutes"
	case "15 minutes":
		return "+15 minutes"
	case "1 hour":
		return "+1 hours"
	case "6 hours":
		return "+6 hours"
	case "1 day":
		return "+1 days"
	default:
		return "+1 days"
	}
}
