package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/lib/pq"
)

// Define standard error types for the db package
var (
	// ErrNoResults is returned when a query returns no results
	ErrNoResults = errors.New("no results found")
	// ErrTimeRangeRequired is returned by a range method given no TimeRange.
	ErrTimeRangeRequired = errors.New("time range required")

	// ErrInvalidScan is returned when row scanning fails
	ErrInvalidScan = errors.New("invalid row scan")

	// ErrInvalidQuery is returned for invalid SQL queries
	ErrInvalidQuery = errors.New("invalid query")

	// ErrDatabaseConnection is returned for database connection issues
	ErrDatabaseConnection = errors.New("database connection error")

	// ErrQueryExecution is returned for query execution failures
	ErrQueryExecution = errors.New("query execution failed")
)

// ErrorWithOperation wraps an error with operation context
func ErrorWithOperation(err error, operation string) error {
	if err == nil {
		return fmt.Errorf("%s: <nil>", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// QueryError creates a structured query error with context
func QueryError(err error, operation string, details string) error {
	if details != "" {
		return fmt.Errorf("%s: %s: %w", operation, details, err)
	}
	return ErrorWithOperation(err, operation)
}

// ConnectionError wraps a failure to reach the database with context. The
// result matches both ErrDatabaseConnection and err.
func ConnectionError(err error, dbType string, details string) error {
	msg := fmt.Sprintf("failed to connect to %s database: %v", dbType, err)
	if details != "" {
		msg += ": " + details
	}
	return &connectionError{msg: msg, err: err}
}

type connectionError struct {
	msg string
	err error
}

func (e *connectionError) Error() string { return e.msg }

func (e *connectionError) Unwrap() []error { return []error{ErrDatabaseConnection, e.err} }

// SchemaError wraps schema-related errors
func SchemaError(err error, operation string, table string) error {
	return fmt.Errorf("schema %s failed for table %s: %w", operation, table, err)
}

// ValidationError returns a structured validation error
func ValidationError(what string, reason string) error {
	return fmt.Errorf("validation error for %s: %s", what, reason)
}

// IsNoResults checks if the error is a "no results" error
func IsNoResults(err error) bool {
	return errors.Is(err, ErrNoResults)
}

// IsTimeout reports whether err is a timeout: an expired context deadline, or
// a PostgreSQL statement canceled by statement_timeout or a cancel request
// (SQLSTATE 57014).
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "57014"
}

// IsUnreachable reports whether err says the database could not be reached or
// dropped the connection: ErrDatabaseConnection, a network error, a bad driver
// connection, an EOF or unexpected EOF, or a PostgreSQL connection exception
// (SQLSTATE class 08) or server shutdown (57P01, 57P02, 57P03). An expired
// context deadline alone is not.
func IsUnreachable(err error) bool {
	if errors.Is(err, ErrDatabaseConnection) || errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// context.DeadlineExceeded is itself a net.Error.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr != context.DeadlineExceeded {
		return true
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	switch pqErr.Code {
	case "57P01", "57P02", "57P03":
		return true
	}
	return pqErr.Code.Class() == "08"
}
