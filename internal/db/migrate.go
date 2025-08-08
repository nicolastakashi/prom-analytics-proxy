package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	goose "github.com/pressly/goose/v3"
)

// Embedded migrations for both PostgreSQL and SQLite.
//
// The directory structure is:
// - migrations/postgresql/*.sql
// - migrations/sqlite/*.sql
//
//go:embed migrations/**/*.sql
var migrationsFS embed.FS

// runMigrations applies all up migrations for the selected engine using the embedded FS.
func runMigrations(ctx context.Context, db *sql.DB, engine string) error {
	goose.SetBaseFS(migrationsFS)

	var (
		dialect string
		dir     string
	)

	switch engine {
	case "postgres", "postgresql":
		dialect = "postgres"
		dir = "migrations/postgresql"
	case "sqlite", "sqlite3":
		// Goose uses the dialect name "sqlite3" regardless of the driver package
		dialect = "sqlite3"
		dir = "migrations/sqlite"
	default:
		return fmt.Errorf("unsupported database engine for migrations: %s", engine)
	}

	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
