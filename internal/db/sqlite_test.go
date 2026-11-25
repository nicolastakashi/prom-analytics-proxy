package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewSqliteProvider_FailureClosesPool verifies a failed construction
// leaves no connection pool open, and reports a database that cannot be
// opened as a connection error and a failed migration as not one.
func TestNewSqliteProvider_FailureClosesPool(t *testing.T) {
	// Not parallel: swaps the configured SQLite path and counts the process's
	// connection pools.
	for _, tc := range []struct {
		name           string
		path           func(t *testing.T) string
		connectionFail bool
	}{
		{name: "cannot open", path: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing", "db.sqlite")
		}, connectionFail: true},
		{name: "migration fails", path: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "db.sqlite")
			createConflictingMigrationTable(t, "sqlite", path)
			return path
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path(t)
			saved := config.DefaultConfig.Database.SQLite.DatabasePath
			config.DefaultConfig.Database.SQLite.DatabasePath = path
			t.Cleanup(func() { config.DefaultConfig.Database.SQLite.DatabasePath = saved })
			before := openPools()

			p, err := newSqliteProvider(context.Background())

			require.Error(t, err)
			assert.Nil(t, p)
			assert.Equal(t, tc.connectionFail, errors.Is(err, ErrDatabaseConnection), "connection error: %v", err)
			assertNoPoolLeft(t, before)
		})
	}
}
