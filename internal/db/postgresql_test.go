package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertConcurrentOverlappingUpsertsDoNotDeadlock races two concurrent
// calls to upsert - each given the same itemsPerCall items, built by
// buildItem, but in exactly reversed relative order - and asserts neither
// ever fails. Two callers upserting overlapping rows via ON CONFLICT DO
// UPDATE in opposite orders is Postgres's own documented deadlock
// precondition (see
// https://www.postgresql.org/docs/current/explicit-locking.html#LOCKING-DEADLOCKS):
// "the best defense against deadlocks is generally to avoid them by being
// certain that all applications ... acquire locks ... in a consistent
// order."
//
// This repeats the attempt many times: a deadlock only happens if the two
// calls' row-lock acquisition genuinely overlaps in time, which goroutine
// scheduling doesn't guarantee on any single attempt - one try could pass
// by luck even against code that doesn't sort before upserting.
func assertConcurrentOverlappingUpsertsDoNotDeadlock[T any](
	t *testing.T,
	itemsPerCall, attempts int,
	buildItem func(i int) T,
	upsert func(context.Context, []T) error,
) {
	t.Helper()

	ascending := make([]T, itemsPerCall)
	descending := make([]T, itemsPerCall)
	for i := 0; i < itemsPerCall; i++ {
		item := buildItem(i)
		ascending[i] = item
		descending[itemsPerCall-1-i] = item
	}

	for attempt := 0; attempt < attempts; attempt++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		ready := make(chan struct{})

		for _, items := range [][]T{ascending, descending} {
			items := items
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				errs <- upsert(context.Background(), items)
			}()
		}
		close(ready)
		wg.Wait()
		close(errs)

		for err := range errs {
			assert.NoError(t, err, "attempt %d: concurrent overlapping upserts must not fail", attempt)
		}
	}
}

// TestNewPostgreSQLProvider_DoesNotMutateGlobalConfig verifies that
// NewPostgreSQLProvider uses only the supplied config and leaves
// config.DefaultConfig untouched.
func TestNewPostgreSQLProvider_DoesNotMutateGlobalConfig(t *testing.T) {
	t.Parallel()
	prov := newTestPostgreSQLProvider(t)

	before := config.DefaultConfig.Database
	// Round-trip a trivial query to confirm the provider is functional.
	_, err := prov.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if config.DefaultConfig.Database != before {
		t.Fatal("NewPostgreSQLProvider must not mutate config.DefaultConfig")
	}
}

// TestPostgreSQL_UpsertMetricsCatalog_LastSyncedAtIsUTC guards last_synced_at
// staying in the same clock domain as the $1 bound RefreshMetricsUsageSummary
// compares it against (tr.From.UTC(), via PrepareTimeRange): last_synced_at is
// TIMESTAMP WITHOUT TIME ZONE, so a bare NOW() lands in the writing session's
// TimeZone instead of UTC, silently turning the freshness filter into a
// permanent no-op on any server whose TimeZone isn't UTC. The server's
// sessions default to TimeZone=UTC - which would let the bug pass
// vacuously - so the test database's default is pinned to a fixed, DST-free
// non-UTC offset before the provider (and its connection pool) ever connects.
func TestPostgreSQL_UpsertMetricsCatalog_LastSyncedAtIsUTC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := newTestPostgreSQLConfig(t)

	bootstrap, err := sql.Open("postgres", postgreSQLTestDSN(cfg))
	require.NoError(t, err, "open bootstrap connection")
	_, err = bootstrap.ExecContext(ctx, `ALTER DATABASE `+cfg.Database+` SET TIME ZONE 'Etc/GMT+5'`)
	require.NoError(t, err, "pin database default TimeZone to a fixed non-UTC offset")
	require.NoError(t, bootstrap.Close())

	p := newTestPostgreSQLProviderWithConfig(t, cfg)

	require.NoError(t, p.UpsertMetricsCatalog(ctx, []MetricCatalogItem{{Name: "tz_metric", Type: "gauge", Help: "h"}}))

	var rawDB *sql.DB
	p.WithDB(func(d *sql.DB) { rawDB = d })

	var lastSynced time.Time
	row := rawDB.QueryRowContext(ctx, `SELECT last_synced_at FROM metrics_catalog WHERE name = $1`, "tz_metric")
	require.NoError(t, row.Scan(&lastSynced), "scan last_synced_at")

	assert.WithinDuration(t, time.Now().UTC(), lastSynced, 10*time.Second,
		"last_synced_at must be written in UTC regardless of the writing session's TimeZone (pinned to Etc/GMT+5 here); "+
			"a bare NOW() would drift by the session's offset instead")
}

// TestPostgreSQL_InsertRulesUsage_ConcurrentOverlappingUpsertsDoNotDeadlock
// verifies InsertRulesUsage tolerates concurrent calls upserting
// overlapping rows in different orders without deadlocking (#594).
func TestPostgreSQL_InsertRulesUsage_ConcurrentOverlappingUpsertsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProvider(t)

	assertConcurrentOverlappingUpsertsDoNotDeadlock(t, 40, 25,
		func(i int) RulesUsage {
			return RulesUsage{
				Serie:     fmt.Sprintf("deadlock_serie_%02d", i),
				GroupName: "g", Name: "r", Expression: "e", Kind: string(RuleUsageKindAlert),
			}
		},
		p.InsertRulesUsage,
	)
}

// TestPostgreSQL_InsertDashboardUsage_ConcurrentOverlappingUpsertsDoNotDeadlock
// verifies InsertDashboardUsage tolerates concurrent calls upserting
// overlapping rows in different orders without deadlocking (#595).
func TestPostgreSQL_InsertDashboardUsage_ConcurrentOverlappingUpsertsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProvider(t)

	assertConcurrentOverlappingUpsertsDoNotDeadlock(t, 40, 25,
		func(i int) DashboardUsage {
			return DashboardUsage{Id: fmt.Sprintf("deadlock_dash_%02d", i), Serie: "m", Name: "n", URL: "u"}
		},
		p.InsertDashboardUsage,
	)
}

// TestPostgreSQL_UpsertMetricsCatalog_ConcurrentOverlappingUpsertsDoNotDeadlock
// verifies UpsertMetricsCatalog tolerates concurrent calls upserting
// overlapping rows in different orders without deadlocking (#592).
func TestPostgreSQL_UpsertMetricsCatalog_ConcurrentOverlappingUpsertsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProvider(t)

	assertConcurrentOverlappingUpsertsDoNotDeadlock(t, 40, 25,
		func(i int) MetricCatalogItem {
			return MetricCatalogItem{Name: fmt.Sprintf("deadlock_metric_%02d", i), Type: "gauge", Help: "h"}
		},
		p.UpsertMetricsCatalog,
	)
}

// TestPostgreSQL_UpsertMetricsJobIndex_ConcurrentOverlappingUpsertsDoNotDeadlock
// verifies UpsertMetricsJobIndex tolerates concurrent calls upserting
// overlapping rows in different orders without deadlocking (#593).
func TestPostgreSQL_UpsertMetricsJobIndex_ConcurrentOverlappingUpsertsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProvider(t)

	assertConcurrentOverlappingUpsertsDoNotDeadlock(t, 40, 25,
		func(i int) MetricJobIndexItem {
			return MetricJobIndexItem{Name: fmt.Sprintf("deadlock_metric_%02d", i), Job: "shared_job"}
		},
		p.UpsertMetricsJobIndex,
	)
}

func TestPostgreSQL_StatementTimeoutAborts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := newTestPostgreSQLProviderWithStatementTimeout(t, 500*time.Millisecond)

	// Sanity: a fast query still succeeds.
	var fast int
	err := p.(*PostGreSQLProvider).db.QueryRowContext(ctx, "SELECT 1").Scan(&fast)
	assert.NoError(t, err, "fast query under the budget should succeed")
	assert.Equal(t, 1, fast)

	// Now a query that deliberately sleeps past the configured timeout.
	// PostgreSQL should abort it server-side with SQLSTATE 57014
	// (query_canceled), surfaced by lib/pq with the canonical message
	// "pq: canceling statement due to statement timeout".
	_, err = p.(*PostGreSQLProvider).db.ExecContext(ctx, "SELECT pg_sleep(2)")
	assert.Error(t, err, "query exceeding statement_timeout should fail")
	assert.Contains(t, err.Error(), "statement timeout",
		"error should identify the server-side timeout source")
}

// assertStatementLevelTriggerAbortsAndRollsBack attaches a BEFORE INSERT ...
// FOR EACH STATEMENT trigger to targetTable that sleeps for 1 second before
// letting the statement proceed. A statement-level trigger fires exactly
// once per statement regardless of how many rows it affects, so this holds
// even for a single-row batch. With a StatementTimeout well under that
// (configured by the caller via newTestPostgreSQLProviderWithStatementTimeout),
// PostgreSQL aborts the statement with SQLSTATE 57014 before it can insert
// anything - insertFn's transaction must then observe the error and roll
// back cleanly, not commit whatever COPY data was already buffered or leave
// the transaction open.
func assertStatementLevelTriggerAbortsAndRollsBack(
	t *testing.T,
	p Provider,
	targetTable string,
	insertFn func(ctx context.Context) error,
) {
	t.Helper()

	ctx := context.Background()
	pg := p.(*PostGreSQLProvider)

	triggerFn := fmt.Sprintf("test_sleep_trigger_%s", targetTable)
	_, err := pg.db.ExecContext(ctx, fmt.Sprintf(`
        CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $$
        BEGIN
            PERFORM pg_sleep(1);
            RETURN NULL;
        END;
        $$ LANGUAGE plpgsql
    `, triggerFn))
	require.NoError(t, err, "create trigger function")

	_, err = pg.db.ExecContext(ctx, fmt.Sprintf(`
        CREATE TRIGGER %s_trg
        BEFORE INSERT ON %s
        FOR EACH STATEMENT EXECUTE FUNCTION %s()
    `, targetTable, targetTable, triggerFn))
	require.NoError(t, err, "create trigger")

	err = insertFn(ctx)
	assert.Error(t, err, "insert must fail when the statement timeout aborts the statement mid-flight")

	var count int
	err = pg.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", targetTable)).Scan(&count)
	require.NoError(t, err, "count rows in %s", targetTable)
	assert.Equal(t, 0, count, "no rows should be committed in %s - the transaction must have rolled back", targetTable)
}

// TestPostgreSQL_Insert_StatementTimeoutRollsBack guards the acceptance
// criterion from #542 ("partial failures still roll back the transaction")
// for the COPY-based Insert path added by this PR.
func TestPostgreSQL_Insert_StatementTimeoutRollsBack(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProviderWithStatementTimeout(t, 100*time.Millisecond)

	now := time.Now().UTC()
	assertStatementLevelTriggerAbortsAndRollsBack(t, p, "queries", func(ctx context.Context) error {
		return p.Insert(ctx, []Query{{
			TS:            now,
			QueryParam:    "up",
			TimeParam:     now,
			Duration:      10 * time.Millisecond,
			StatusCode:    200,
			BodySize:      10,
			LabelMatchers: LabelMatchers{{"__name__": "up"}},
			Type:          QueryTypeInstant,
		}})
	})
}

// TestPostgreSQL_InsertRulesUsage_StatementTimeoutRollsBack is
// TestPostgreSQL_Insert_StatementTimeoutRollsBack's counterpart for
// InsertRulesUsage, whose final INSERT ... SELECT ... ON CONFLICT lands on
// RulesUsage.
func TestPostgreSQL_InsertRulesUsage_StatementTimeoutRollsBack(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProviderWithStatementTimeout(t, 100*time.Millisecond)

	assertStatementLevelTriggerAbortsAndRollsBack(t, p, "rulesusage", func(ctx context.Context) error {
		return p.InsertRulesUsage(ctx, []RulesUsage{{
			Serie: "up", GroupName: "g1", Name: "r1", Expression: "expr1",
			Kind: string(RuleUsageKindAlert), Labels: []string{"l1"},
		}})
	})
}

// TestPostgreSQL_InsertDashboardUsage_StatementTimeoutRollsBack is
// TestPostgreSQL_Insert_StatementTimeoutRollsBack's counterpart for
// InsertDashboardUsage, whose final INSERT ... SELECT ... ON CONFLICT lands
// on DashboardUsage.
func TestPostgreSQL_InsertDashboardUsage_StatementTimeoutRollsBack(t *testing.T) {
	t.Parallel()
	p := newTestPostgreSQLProviderWithStatementTimeout(t, 100*time.Millisecond)

	assertStatementLevelTriggerAbortsAndRollsBack(t, p, "dashboardusage", func(ctx context.Context) error {
		return p.InsertDashboardUsage(ctx, []DashboardUsage{{
			Id: "d1", Serie: "m1", Name: "Dash 1", URL: "http://d/1",
		}})
	})
}
