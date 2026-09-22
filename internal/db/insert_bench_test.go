package db

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startBenchPostgres spins up a disposable PostgreSQL for benchmarking and
// skips gracefully when Docker isn't available, matching the pattern used by
// the routes package's own PostgreSQL benchmarks.
func startBenchPostgres(b *testing.B) (Provider, func()) {
	b.Helper()
	ctx := context.Background()

	pgc, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		b.Skipf("PostgreSQL container unavailable (Docker not running?): %v", err)
	}

	host, err := pgc.Host(ctx)
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("container host: %v", err)
	}
	port, err := pgc.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("container port: %v", err)
	}
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("parse port: %v", err)
	}

	prov, err := NewPostgreSQLProvider(ctx, config.PostgreSQLConfig{
		Addr:        host,
		Port:        portNum,
		User:        "testuser",
		Password:    "testpass",
		Database:    "testdb",
		SSLMode:     "disable",
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		_ = pgc.Terminate(ctx)
		b.Fatalf("db provider: %v", err)
	}
	return prov, func() {
		_ = prov.Close()
		_ = pgc.Terminate(ctx)
	}
}

func makeBenchQueries(n int) []Query {
	now := time.Now().UTC()
	qs := make([]Query, 0, n)
	for i := 0; i < n; i++ {
		qs = append(qs, Query{
			TS:                    now.Add(time.Duration(i) * time.Millisecond),
			QueryParam:            fmt.Sprintf("rate(http_requests_total{job=\"svc-%d\"}[5m])", i%50),
			TimeParam:             now,
			Duration:              10 * time.Millisecond,
			StatusCode:            200,
			BodySize:              512,
			LabelMatchers:         LabelMatchers{{"__name__": "http_requests_total"}},
			Fingerprint:           fmt.Sprintf("fp-%d", i),
			Type:                  QueryTypeRange,
			Step:                  15,
			Start:                 now.Add(-time.Hour),
			End:                   now,
			TotalQueryableSamples: 1000,
			PeakSamples:           2000,
			HTTPHeaders:           map[string]string{"X-Bench": "1"},
		})
	}
	return qs
}

// BenchmarkPostgreSQLInsert measures PostGreSQLProvider.Insert (pq.CopyIn
// bulk load) wall-clock cost for batch sizes matching the acceptance
// criteria in #542 - 100 and 1000 rows.
func BenchmarkPostgreSQLInsert(b *testing.B) {
	prov, cleanup := startBenchPostgres(b)
	defer cleanup()

	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("batch%d", n), func(b *testing.B) {
			queries := makeBenchQueries(n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := prov.Insert(context.Background(), queries); err != nil {
					b.Fatalf("Insert: %v", err)
				}
			}
		})
	}
}
