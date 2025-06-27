package db

import (
	"errors"
	"fmt"
)

// Define standard error types for the db package
var (
	// ErrNoResults is returned when a query returns no results
	ErrNoResults = errors.New("no results found")

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

// ConnectionError wraps connection errors with context
func ConnectionError(err error, dbType string, details string) error {
	baseErr := fmt.Errorf("failed to connect to %s database: %w", dbType, err)
	if details != "" {
		return fmt.Errorf("%s: %s", baseErr.Error(), details)
	}
	return baseErr
}

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
