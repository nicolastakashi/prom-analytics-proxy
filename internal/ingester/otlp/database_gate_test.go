package otlp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errRefused = db.ConnectionError(errors.New("connection refused"), "PostgreSQL", "failed to ping database")

// TestDatabaseGate_RefusesUntilConnected verifies store calls fail at once
// with the unavailable error while the gate connects, and reach the database
// once it has connected.
func TestDatabaseGate_RefusesUntilConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := newFakeProvider()
		release := make(chan struct{})
		gate := NewDatabaseGate(func(ctx context.Context) (db.Provider, error) {
			select {
			case <-release:
				return provider, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		stop := runGate(t, gate)
		defer stop()
		synctest.Wait()

		_, err := gate.GetSeriesMetadataByNames(t.Context(), []string{"m"}, "")
		require.ErrorIs(t, err, errDatabaseUnavailable)
		require.ErrorIs(t, gate.UpsertMetricsCatalog(t.Context(), []db.MetricCatalogItem{{Name: "m"}}), errDatabaseUnavailable)
		assert.Empty(t, provider.upsertedNames())

		close(release)
		synctest.Wait()

		metas, err := gate.GetSeriesMetadataByNames(t.Context(), []string{"m"}, "")
		require.NoError(t, err)
		assert.Equal(t, []models.MetricMetadata{{Name: "m"}}, metas)
		require.NoError(t, gate.UpsertMetricsCatalog(t.Context(), []db.MetricCatalogItem{{Name: "m"}}))
		assert.Equal(t, [][]string{{"m"}}, provider.upsertedNames())
	})
}

// TestDatabaseGate_Metrics verifies the up gauge reads 0 until the gate
// connects and 1 after, and each attempt is counted by its result.
func TestDatabaseGate_Metrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failures := 2
		gate := NewDatabaseGate(func(context.Context) (db.Provider, error) {
			if failures > 0 {
				failures--
				return nil, errRefused
			}
			return newFakeProvider(), nil
		})
		success := metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "success"})
		failure := metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"})
		assert.Zero(t, metricValue(t, "ingester_database_up", map[string]string{}))

		stop := runGate(t, gate)
		defer stop()
		synctest.Wait()
		assert.Zero(t, metricValue(t, "ingester_database_up", map[string]string{}))
		assert.Equal(t, failure+1, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"}))

		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, 1.0, metricValue(t, "ingester_database_up", map[string]string{}))
		assert.Equal(t, success+1, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "success"}))
		assert.Equal(t, failure+2, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"}))
	})
}

// TestDatabaseGate_RetriesConnectionFailures verifies a connection failure
// is retried after a delay that doubles from 1s to a 15s cap, jittered
// between half the delay and all of it.
func TestDatabaseGate_RetriesConnectionFailures(t *testing.T) {
	sec := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
	for _, tc := range []struct {
		name       string
		randInt64N func(int64) int64
		want       []time.Duration
	}{
		{name: "least jitter", randInt64N: func(int64) int64 { return 0 },
			want: []time.Duration{sec(0.5), sec(1), sec(2), sec(4), sec(7.5), sec(7.5), sec(7.5)}},
		{name: "most jitter", randInt64N: func(n int64) int64 { return n - 1 },
			want: []time.Duration{sec(1), sec(2), sec(4), sec(8), sec(15), sec(15), sec(15)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gaps := connectGaps(t, tc.randInt64N, len(tc.want))
			assert.Equal(t, tc.want, gaps)
		})
	}
	t.Run("random jitter", func(t *testing.T) {
		gaps := connectGaps(t, nil, 7)
		floors, ceilings := 0, 0
		for i, max := range []time.Duration{sec(1), sec(2), sec(4), sec(8), sec(15), sec(15), sec(15)} {
			assert.GreaterOrEqual(t, gaps[i], max/2, "retry %d", i+1)
			assert.LessOrEqual(t, gaps[i], max, "retry %d", i+1)
			switch gaps[i] {
			case max / 2:
				floors++
			case max:
				ceilings++
			}
		}
		assert.Less(t, floors, len(gaps), "every retry waited the least jitter")
		assert.Less(t, ceilings, len(gaps), "every retry waited the most jitter")
	})
}

// connectGaps returns the time between consecutive attempts of a gate whose
// first `failures` connection attempts fail, jittered by randInt64N (nil: the
// default).
func connectGaps(t *testing.T, randInt64N func(int64) int64, failures int) []time.Duration {
	var gaps []time.Duration
	synctest.Test(t, func(t *testing.T) {
		var attempts []time.Time
		gate := NewDatabaseGate(func(context.Context) (db.Provider, error) {
			attempts = append(attempts, time.Now())
			if len(attempts) <= failures {
				return nil, errRefused
			}
			return newFakeProvider(), nil
		})
		if randInt64N != nil {
			gate.randInt64N = randInt64N
		}
		stop := runGate(t, gate)
		time.Sleep(time.Duration(failures) * 15 * time.Second)
		synctest.Wait()
		_, err := gate.GetSeriesMetadataByNames(t.Context(), nil, "")
		require.NoError(t, err, "not connected after the last retry")
		stop()

		for i := 1; i < len(attempts); i++ {
			gaps = append(gaps, attempts[i].Sub(attempts[i-1]))
		}
	})
	return gaps
}

// TestDatabaseGate_FatalErrorStopsRun verifies an error other than a
// connection failure, such as a failed migration, ends Run with that error
// without a retry.
func TestDatabaseGate_FatalErrorStopsRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		migration := db.SchemaError(errors.New("syntax error"), "migration", "postgres")
		attempts := 0
		gate := NewDatabaseGate(func(context.Context) (db.Provider, error) {
			attempts++
			return nil, migration
		})
		failure := metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"})

		err := gate.Run(t.Context())

		require.ErrorIs(t, err, migration)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, failure+1, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"}))
		_, err = gate.GetSeriesMetadataByNames(t.Context(), nil, "")
		require.ErrorIs(t, err, errDatabaseUnavailable)
	})
}

// TestDatabaseGate_RunStopsWhenDone verifies Run keeps running until ctx is
// done, whether connected or not, and then returns ctx's error without
// another attempt or counting one, even when ctx ends an attempt with an
// error that is not a connection failure.
func TestDatabaseGate_RunStopsWhenDone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		connect func(ctx context.Context) (db.Provider, error)
	}{
		{name: "connected", connect: func(context.Context) (db.Provider, error) { return newFakeProvider(), nil }},
		{name: "retrying", connect: func(context.Context) (db.Provider, error) { return nil, errRefused }},
		{name: "attempting", connect: func(ctx context.Context) (db.Provider, error) {
			<-ctx.Done()
			return nil, db.SchemaError(errors.New("driver: bad connection"), "migration", "postgres")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts atomic.Int64
				gate := NewDatabaseGate(func(ctx context.Context) (db.Provider, error) {
					attempts.Add(1)
					return tc.connect(ctx)
				})
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- gate.Run(ctx) }()

				time.Sleep(time.Hour)
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("Run returned before ctx was done: %v", err)
				default:
				}

				n := attempts.Load()
				success := metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "success"})
				failure := metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"})
				cancel()
				require.ErrorIs(t, <-done, context.Canceled)
				time.Sleep(time.Hour)
				assert.Equal(t, n, attempts.Load())
				assert.Equal(t, success, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "success"}))
				assert.Equal(t, failure, metricValue(t, "ingester_database_connect_attempts_total", map[string]string{"result": "failure"}))
			})
		})
	}
}

// TestDatabaseGate_Close verifies Close closes the database once connected,
// returning its error, and does nothing before.
func TestDatabaseGate_Close(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := newFakeProvider()
		provider.closeErr = assert.AnError
		release := make(chan struct{})
		gate := NewDatabaseGate(func(ctx context.Context) (db.Provider, error) {
			select {
			case <-release:
				return provider, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		stop := runGate(t, gate)
		defer stop()

		require.NoError(t, gate.Close())
		assert.Zero(t, provider.closed.Load())

		close(release)
		synctest.Wait()
		require.ErrorIs(t, gate.Close(), assert.AnError)
		assert.Equal(t, int64(1), provider.closed.Load())
	})
}

// runGate runs gate until the returned stop is called, which cancels Run
// and waits for it to return.
func runGate(t *testing.T, gate *DatabaseGate) (stop func()) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- gate.Run(ctx) }()
	return func() {
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	}
}

// fakeProvider is a database answering every usage lookup with a record per
// name and recording catalog upserts. Methods not overridden panic.
type fakeProvider struct {
	db.Provider
	store    fakeStore
	closeErr error
	closed   atomic.Int64
}

func newFakeProvider() *fakeProvider { return &fakeProvider{} }

func (p *fakeProvider) GetSeriesMetadataByNames(_ context.Context, names []string, _ string) ([]models.MetricMetadata, error) {
	metas := make([]models.MetricMetadata, 0, len(names))
	for _, n := range names {
		metas = append(metas, models.MetricMetadata{Name: n})
	}
	return metas, nil
}

func (p *fakeProvider) UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error {
	return p.store.UpsertMetricsCatalog(ctx, items)
}

func (p *fakeProvider) upsertedNames() [][]string { return p.store.upsertedNames() }

func (p *fakeProvider) Close() error {
	p.closed.Add(1)
	return p.closeErr
}
