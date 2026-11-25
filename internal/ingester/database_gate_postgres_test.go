package ingester

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	otlppkg "github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDatabaseGate_DeadlineCanceledStatement_PostgreSQL verifies a lookup
// PostgreSQL cancels at the caller's deadline fails with SQLSTATE 57014, and
// the gate counts it as a failure: the third opens the circuit, refusing the
// next lookup although the database would answer it.
func TestDatabaseGate_DeadlineCanceledStatement_PostgreSQL(t *testing.T) {
	prov, cleanup := runPostgres(t)
	defer cleanup()
	gate := otlppkg.NewDatabaseGate(func(context.Context) (db.Provider, error) { return prov, nil })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- gate.Run(ctx) }()
	defer func() {
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	}()
	lookup := func(ctx context.Context) error {
		_, err := gate.GetSeriesMetadataByNames(ctx, []string{"m"}, "")
		return err
	}
	require.Eventually(t, func() bool { return lookup(t.Context()) == nil }, 10*time.Second, 10*time.Millisecond)

	var sqlDB *sql.DB
	prov.WithDB(func(d *sql.DB) { sqlDB = d })
	tx, err := sqlDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(t.Context(), "LOCK TABLE metrics_catalog IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)

	for i := range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		err := lookup(ctx)
		cancel()
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr, "lookup %d", i+1)
		assert.Equal(t, "57014", string(pqErr.Code), "lookup %d", i+1)
	}
	require.NoError(t, tx.Rollback())

	require.EqualError(t, lookup(t.Context()), "database unavailable")
}
