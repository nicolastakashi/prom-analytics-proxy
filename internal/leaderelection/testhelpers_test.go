package leaderelection

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestPostgresDB spins up a disposable PostgreSQL using Testcontainers and
// returns a live *sql.DB, mirroring internal/db/postgresql_test.go's
// newTestPostgreSQLProvider pattern but returning a raw *sql.DB: this package
// has no dependency on internal/db, so there's no Provider to build here.
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
	assert.NoError(t, err, "container host")
	port, err := pgContainer.MappedPort(ctx, "5432/tcp")
	assert.NoError(t, err, "container port")

	dsn := fmt.Sprintf("host=%s port=%s user=testuser password=testpass dbname=testdb sslmode=disable", host, port.Port())
	db, err := sql.Open("postgres", dsn)
	assert.NoError(t, err, "open postgres connection")
	t.Cleanup(func() { _ = db.Close() })

	assert.NoError(t, db.PingContext(ctx), "ping postgres")
	return db
}

// newTestElector builds an elector against a fresh, throwaway registry —
// every test needs its own registry (the same metric names registered
// twice against one shared registry panics), so this is the one place
// that decision lives rather than being repeated at every call site.
func newTestElector(strat strategy, backoff backoffConfig) *elector {
	return newElector(strat, backoff, prometheus.NewRegistry())
}
