package leaderelection

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
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
	pgBase       config.PostgreSQLConfig
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
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		pgServerErr = err
		return
	}
	pgBase = config.PostgreSQLConfig{
		Addr:        host,
		Port:        portNum,
		User:        "testuser",
		Password:    "testpass",
		Database:    "postgres",
		SSLMode:     "disable",
		DialTimeout: 5 * time.Second,
	}
	pgAdmin, pgServerErr = sql.Open("postgres", fmt.Sprintf("host=%s port=%d user=testuser password=testpass dbname=postgres sslmode=disable", host, portNum))
}

// newTestPostgresDB returns the connection pool of a PostgreSQL provider on
// a fresh database on the shared container, migrated by the provider as in
// production; closed and dropped when t ends. Skips t when Docker is
// unavailable.
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

	cfg := pgBase
	cfg.Database = name
	provider, err := db.NewPostgreSQLProvider(ctx, cfg)
	require.NoError(t, err, "init postgres provider")
	t.Cleanup(func() { _ = provider.Close() })

	var pool *sql.DB
	provider.WithDB(func(d *sql.DB) { pool = d })
	return pool
}
