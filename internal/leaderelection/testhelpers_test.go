package leaderelection

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestPostgresDB spins up a disposable PostgreSQL using Testcontainers and
// returns a live *sql.DB: this package has no dependency on internal/db, so
// there's no Provider abstraction to build here, just the raw connection.
func newTestPostgresDB(t *testing.T) *sql.DB {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		// Docker not available in this environment; skip tests gracefully.
		t.Skipf("Skipping leaderelection PostgreSQL tests (Docker not available): %v", err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	host, err := pgContainer.Host(ctx)
	require.NoError(t, err, "container host") // every subsequent step needs a valid host
	port, err := pgContainer.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err, "container port")

	dsn := fmt.Sprintf("host=%s port=%s user=testuser password=testpass dbname=testdb sslmode=disable", host, port.Port())
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open postgres connection")
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.PingContext(ctx), "ping postgres") // a test built on a dead db is not worth continuing
	return db
}

// newTestElector builds an elector against a fresh, throwaway registry —
// every test needs its own registry (the same metric names registered
// twice against one shared registry panics), so this is the one place
// that decision lives rather than being repeated at every call site.
func newTestElector(strat strategy, backoff backoffConfig) *elector {
	e, _ := newTestElectorWithRegistry(strat, backoff)
	return e
}

// newTestElectorWithRegistry is newTestElector's variant for tests that
// need to Gather() the registry directly — e.g. to prove a gauge's series
// exists without the test itself creating it via WithLabelValues, which
// would happen if it read the gauge instead.
func newTestElectorWithRegistry(strat strategy, backoff backoffConfig) (*elector, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return newElector(strat, backoff, reg), reg
}
