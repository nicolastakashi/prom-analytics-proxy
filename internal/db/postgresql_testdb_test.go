package db

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

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// One PostgreSQL container serves the whole package, started on first use;
// each test gets its own database on it, so tests stay isolated and can run
// in parallel.
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
		// Each parallel test's provider holds its own connection pool.
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
	pgAdmin, pgServerErr = sql.Open("postgres", postgreSQLTestDSN(pgBase))
}

func postgreSQLTestDSN(c config.PostgreSQLConfig) string {
	return fmt.Sprintf("host='%s' port=%d user='%s' password='%s' dbname='%s' sslmode='disable'",
		c.Addr, c.Port, c.User, c.Password, c.Database)
}

// newTestPostgreSQLConfig creates an empty, unmigrated database on the shared
// container and returns a config pointing at it; the database is dropped when
// t ends. Skips t when Docker is unavailable.
func newTestPostgreSQLConfig(t testing.TB) config.PostgreSQLConfig {
	t.Helper()
	pgServerOnce.Do(startPostgreSQLServer)
	if pgServerErr != nil {
		t.Skipf("Skipping PostgreSQL container tests (Docker not available): %v", pgServerErr)
	}

	name := fmt.Sprintf("test_%d", pgDBSeq.Add(1))
	_, err := pgAdmin.ExecContext(context.Background(), "CREATE DATABASE "+name)
	require.NoError(t, err, "create test database")
	t.Cleanup(func() {
		_, _ = pgAdmin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	cfg := pgBase
	cfg.Database = name
	return cfg
}

// newTestPostgreSQLProvider returns a migrated Provider on its own database,
// closed when t ends.
func newTestPostgreSQLProvider(t testing.TB) Provider {
	t.Helper()
	return newTestPostgreSQLProviderWithConfig(t, newTestPostgreSQLConfig(t))
}

// newTestPostgreSQLProviderWithStatementTimeout is newTestPostgreSQLProvider
// with the given server-side StatementTimeout.
func newTestPostgreSQLProviderWithStatementTimeout(t testing.TB, timeout time.Duration) Provider {
	t.Helper()
	cfg := newTestPostgreSQLConfig(t)
	cfg.StatementTimeout = timeout
	return newTestPostgreSQLProviderWithConfig(t, cfg)
}

func newTestPostgreSQLProviderWithConfig(t testing.TB, cfg config.PostgreSQLConfig) Provider {
	t.Helper()
	p, err := NewPostgreSQLProvider(context.Background(), cfg)
	require.NoError(t, err, "init postgres provider")
	t.Cleanup(func() { _ = p.Close() })
	return p
}
