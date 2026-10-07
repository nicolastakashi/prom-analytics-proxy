package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

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

// runMigrations applies all up migrations for the selected engine using the
// embedded FS. Safe to call concurrently: it uses a goose Provider per call
// rather than goose's package-level state.
func runMigrations(ctx context.Context, db *sql.DB, engine string) error {
	var (
		dialect goose.Dialect
		dir     string
	)

	switch engine {
	case "postgres", "postgresql":
		dialect = goose.DialectPostgres
		dir = "migrations/postgresql"
	case "sqlite", "sqlite3":
		dialect = goose.DialectSQLite3
		dir = "migrations/sqlite"
	default:
		return fmt.Errorf("unsupported database engine for migrations: %s", engine)
	}

	migrations, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return fmt.Errorf("scope migrations fs: %w", err)
	}

	provider, err := goose.NewProvider(dialect, db, migrations)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
