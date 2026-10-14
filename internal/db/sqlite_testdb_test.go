package db

import (
	"context"
	"testing"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/require"
)

var sqliteTestBackend = testBackend{
	newProvider: newTestSQLiteProvider,
	rebind:      func(query string) string { return query },
}

// newTestSQLiteProvider returns a migrated Provider, closed when t ends.
// Tests stay isolated, and so can run in parallel, only while the configured
// SQLite path is empty: each provider then gets a private temporary database.
func newTestSQLiteProvider(t testing.TB) Provider {
	t.Helper()
	require.Empty(t, config.DefaultConfig.Database.SQLite.DatabasePath,
		"a configured SQLite path would make every test share one database")
	p, err := newSqliteProvider(context.Background())
	require.NoError(t, err, "init sqlite provider")
	t.Cleanup(func() { _ = p.Close() })
	return p
}
