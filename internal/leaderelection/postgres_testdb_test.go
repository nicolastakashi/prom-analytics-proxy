package leaderelection

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// One PostgreSQL container serves the whole package, started on first use;
// each test gets its own database on it, so tests stay isolated and can run
// in parallel. Advisory locks are scoped to a database, so they stay
// isolated too.
var (
	pgServerOnce sync.Once
	pgServer     *postgres.PostgresContainer
	pgAdmin      *sql.DB
	pgDSN        func(dbname string) string
	pgServerErr  error
	pgDBSeq      atomic.Int64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pgAdmin != nil {
		_ = pgAdmin.Close()
	}
	if pgServer != nil {
		_ = pgServer.Terminate(context.Background())
	}
	os.Exit(code)
}

func startPostgreSQLServer() {
	ctx := context.Background()
	pgServer, pgServerErr = postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		// Each parallel test holds its own connection pool.
		testcontainers.WithCmdArgs("-c", "max_connections=500"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if pgServerErr != nil {
		return
	}
	host, err := pgServer.Host(ctx)
	if err != nil {
		pgServerErr = err
		return
	}
	port, err := pgServer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		pgServerErr = err
		return
	}
	pgDSN = func(dbname string) string {
		return fmt.Sprintf("host=%s port=%s user=testuser password=testpass dbname=%s sslmode=disable", host, port.Port(), dbname)
	}
	pgAdmin, pgServerErr = sql.Open("postgres", pgDSN("postgres"))
}

// newTestPostgresDB returns a connection to a fresh database on the shared
// container, with leader_leases created, closed and dropped when t ends.
// Skips t when Docker is unavailable.
func newTestPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	pgServerOnce.Do(startPostgreSQLServer)
	if pgServerErr != nil {
		t.Skipf("Skipping leaderelection PostgreSQL tests (Docker not available): %v", pgServerErr)
	}

	ctx := context.Background()
	name := fmt.Sprintf("test_%d", pgDBSeq.Add(1))
	_, err := pgAdmin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err, "create test database")
	t.Cleanup(func() {
		_, _ = pgAdmin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	db, err := sql.Open("postgres", pgDSN(name))
	require.NoError(t, err, "open postgres connection")
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx), "ping postgres") // a test built on a dead db is not worth continuing

	// Mirrors the production leader_leases schema, which internal/db's
	// migrations own; this package does not depend on internal/db. Keep it in
	// sync with those migrations.
	_, err = db.ExecContext(ctx, `
		CREATE SEQUENCE IF NOT EXISTS leader_lease_fence_token_seq;
		CREATE TABLE IF NOT EXISTS leader_leases (
			lease_name  TEXT PRIMARY KEY,
			holder_id   TEXT NOT NULL,
			fence_token BIGINT NOT NULL DEFAULT nextval('leader_lease_fence_token_seq'),
			expires_at  TIMESTAMPTZ NOT NULL
		);
		ALTER SEQUENCE leader_lease_fence_token_seq OWNED BY leader_leases.fence_token`)
	require.NoError(t, err, "create leader_leases table")

	return db
}
