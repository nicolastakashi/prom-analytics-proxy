package db

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"time"
)

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
