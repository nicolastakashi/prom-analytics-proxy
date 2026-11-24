package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lib/pq"
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

// TestDatabaseGate_CircuitOpensAfterConsecutiveFailures verifies the circuit
// opens on the third consecutive failure saying the database is unreachable
// or timed out, counted across lookups and upserts: that call returns its own
// error, and later calls fail at once with the unavailable error without
// reaching the database.
func TestDatabaseGate_CircuitOpensAfterConsecutiveFailures(t *testing.T) {
	statementCanceledAtDeadline := func(ctx context.Context) error {
		<-ctx.Done()
		return errStatementCanceled
	}
	for _, tc := range []struct {
		name    string
		answer  func(ctx context.Context) error
		wantErr error
	}{
		{name: "connection error", answer: func(context.Context) error { return errRefused }, wantErr: errRefused},
		{name: "network error", answer: func(context.Context) error { return errNetwork }, wantErr: errNetwork},
		{name: "connection exception", answer: func(context.Context) error { return errConnectionFailure }, wantErr: errConnectionFailure},
		{name: "deadline exceeded", answer: func(context.Context) error { return context.DeadlineExceeded }, wantErr: context.DeadlineExceeded},
		{name: "statement canceled at the deadline", answer: statementCanceledAtDeadline, wantErr: errStatementCanceled},
	} {
		for _, calls := range [][]gateCall{{lookup, upsert, lookup}, {upsert, lookup, upsert}} {
			t.Run(tc.name+"/"+calls[2].name+" opens", func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					provider := newFakeProvider()
					gate, stop := connectedGate(t, provider)
					defer stop()
					provider.setAnswer(tc.answer)

					for i, c := range calls {
						ctx, cancel := context.WithTimeout(t.Context(), time.Second)
						err := c.call(ctx, gate)
						cancel()
						require.ErrorIs(t, err, tc.wantErr, "call %d", i+1)
						require.NotErrorIs(t, err, errDatabaseUnavailable, "call %d", i+1)
					}
					assert.Equal(t, int64(3), provider.calls.Load())

					for _, c := range gateCalls {
						require.ErrorIs(t, c.call(t.Context(), gate), errDatabaseUnavailable, c.name)
					}
					assert.Equal(t, int64(3), provider.calls.Load())
				})
			})
		}
	}
}

// answeredErrors are the results of a call the database answered: success,
// or an error not saying it is unreachable.
var answeredErrors = []struct {
	name string
	err  error
}{
	{name: "success"},
	{name: "statement timeout", err: &pq.Error{Code: "57014"}},
	{name: "deadlock", err: &pq.Error{Code: "40P01"}},
	{name: "other error", err: assert.AnError},
}

// TestDatabaseGate_AnsweredCallResetsFailures verifies a call the database
// answered resets the consecutive failure count and never opens the circuit.
func TestDatabaseGate_AnsweredCallResetsFailures(t *testing.T) {
	for _, tc := range answeredErrors {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()

				for i, err := range []error{tc.err, tc.err, tc.err, errRefused, errRefused, tc.err, errRefused, errRefused, tc.err} {
					provider.failWith(err)
					got := lookup.call(t.Context(), gate)
					if err == nil {
						require.NoError(t, got, "call %d", i+1)
					} else {
						require.ErrorIs(t, got, err, "call %d", i+1)
					}
				}
				assert.Equal(t, int64(9), provider.calls.Load())
			})
		})
	}
}

// TestDatabaseGate_SuccessAfterCallerEndedResetsFailures verifies a call the
// database answered successfully after its caller's ctx ended resets the
// consecutive failure count.
func TestDatabaseGate_SuccessAfterCallerEndedResetsFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(context.Context) (context.Context, context.CancelFunc)
	}{
		{name: "deadline", end: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, time.Second)
		}},
		{name: "cancellation", end: func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return ctx, cancel
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()
				provider.failWith(errRefused)
				for i := range 2 {
					require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused, "call %d before", i+1)
				}

				ctx, cancel := tc.end(t.Context())
				defer cancel()
				provider.setAnswer(func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				})
				require.NoError(t, lookup.call(ctx, gate))

				provider.failWith(errRefused)
				for i := range 2 {
					require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused, "call %d after", i+1)
				}
				assert.Equal(t, int64(5), provider.calls.Load())
			})
		})
	}
}

// TestDatabaseGate_CanceledCallNeitherCountsNorResets verifies a call whose
// caller gave up, whatever its error, or that failed with context.Canceled,
// leaves the consecutive failure count as it was.
func TestDatabaseGate_CanceledCallNeitherCountsNorResets(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		live bool // caller's ctx not canceled
	}{
		{name: "context error", err: context.Canceled},
		{name: "statement canceled", err: &pq.Error{Code: "57014"}},
		{name: "network error", err: errNetwork},
		{name: "deadline exceeded", err: fmt.Errorf("query: %w", context.DeadlineExceeded)},
		{name: "context error, live caller", err: context.Canceled, live: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()
				canceled, cancel := context.WithCancel(t.Context())
				cancel()
				if tc.live {
					canceled = t.Context()
				}

				for i, step := range []struct {
					ctx context.Context
					err error
				}{
					{canceled, tc.err}, {canceled, tc.err}, {canceled, tc.err},
					{t.Context(), errRefused}, {t.Context(), errRefused},
					{canceled, tc.err},
					{t.Context(), errRefused},
				} {
					provider.failWith(step.err)
					require.ErrorIs(t, lookup.call(step.ctx, gate), step.err, "call %d", i+1)
				}
				assert.Equal(t, int64(7), provider.calls.Load())

				require.ErrorIs(t, lookup.call(t.Context(), gate), errDatabaseUnavailable)
				assert.Equal(t, int64(7), provider.calls.Load())
			})
		})
	}
}

// TestDatabaseGate_TrialAfterDelay verifies an open circuit lets one trial
// call through once its delay has elapsed: a failed trial reopens it with the
// backoff doubled from 1s to a 15s cap, jittered between half the backoff
// and all of it, and a successful one closes it, resetting the failure count
// and the backoff.
func TestDatabaseGate_TrialAfterDelay(t *testing.T) {
	sec := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
	for _, tc := range []struct {
		name       string
		randInt64N func(int64) int64
		want       []time.Duration
	}{
		{name: "least jitter", randInt64N: func(int64) int64 { return 0 },
			want: []time.Duration{sec(0.5), sec(1), sec(2), sec(4), sec(7.5), sec(7.5)}},
		{name: "most jitter", randInt64N: func(n int64) int64 { return n - 1 },
			want: []time.Duration{sec(1), sec(2), sec(4), sec(8), sec(15), sec(15)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()
				gate.randInt64N = tc.randInt64N
				openCircuit(t, gate, provider)

				for i, delay := range tc.want {
					require.ErrorIs(t, trialAt(t, gate, provider, delay, "trial %d", i+1), errRefused, "trial %d", i+1)
				}

				provider.failWith(nil)
				time.Sleep(tc.want[len(tc.want)-1])
				require.NoError(t, lookup.call(t.Context(), gate))

				openCircuit(t, gate, provider)
				require.ErrorIs(t, trialAt(t, gate, provider, tc.want[0], "after closing"), errRefused)
			})
		})
	}
}

// TestDatabaseGate_AnsweredTrialCloses verifies a trial the database
// answered, successfully or with an error not saying it is unreachable,
// closes the circuit.
func TestDatabaseGate_AnsweredTrialCloses(t *testing.T) {
	for _, tc := range answeredErrors {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()
				openCircuit(t, gate, provider)

				provider.failWith(tc.err)
				err := trialAt(t, gate, provider, time.Second, "trial")
				if tc.err == nil {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, tc.err)
				}
				assert.Equal(t, 1.0, metricValue(t, "ingester_database_up", map[string]string{}))

				provider.failWith(errRefused)
				for i := range 2 {
					require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused, "call %d after the trial", i+1)
				}
			})
		})
	}
}

// TestDatabaseGate_RefusesCallsDuringTrial verifies calls made while a trial
// is in progress fail at once with the unavailable error, and calls reach the
// database again once the trial succeeds.
func TestDatabaseGate_RefusesCallsDuringTrial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := newFakeProvider()
		gate, stop := connectedGate(t, provider)
		defer stop()
		openCircuit(t, gate, provider)
		time.Sleep(time.Second)

		release := make(chan struct{})
		provider.setAnswer(func(context.Context) error {
			<-release
			return nil
		})
		trial := make(chan error, 1)
		go func() { trial <- lookup.call(t.Context(), gate) }()
		synctest.Wait()

		for _, c := range gateCalls {
			require.ErrorIs(t, c.call(t.Context(), gate), errDatabaseUnavailable, c.name)
		}
		assert.Equal(t, int64(4), provider.calls.Load())
		assert.Zero(t, metricValue(t, "ingester_database_up", map[string]string{}))

		close(release)
		require.NoError(t, <-trial)
		assert.Equal(t, 1.0, metricValue(t, "ingester_database_up", map[string]string{}))
		for _, c := range gateCalls {
			require.NoError(t, c.call(t.Context(), gate), c.name)
		}
		assert.Equal(t, int64(6), provider.calls.Load())
	})
}

// TestDatabaseGate_CanceledTrial verifies a trial whose caller gave up, or
// that failed with context.Canceled, leaves the circuit open with the same
// backoff, the next call being the trial.
func TestDatabaseGate_CanceledTrial(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool // caller's ctx not canceled
	}{
		{name: "canceled caller"},
		{name: "live caller", live: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := newFakeProvider()
				gate, stop := connectedGate(t, provider)
				defer stop()
				openCircuit(t, gate, provider)
				time.Sleep(time.Second)

				ctx, cancel := context.WithCancel(t.Context())
				if !tc.live {
					cancel()
				}
				defer cancel()
				provider.failWith(context.Canceled)
				require.ErrorIs(t, lookup.call(ctx, gate), context.Canceled)
				assert.Zero(t, metricValue(t, "ingester_database_up", map[string]string{}))

				provider.failWith(errRefused)
				require.ErrorIs(t, upsert.call(t.Context(), gate), errRefused)
				assert.Equal(t, int64(5), provider.calls.Load())
				require.ErrorIs(t, trialAt(t, gate, provider, 2*time.Second, "after the canceled trial"), errRefused)
			})
		})
	}
}

// TestDatabaseGate_IgnoresCallsAdmittedBeforeOpening verifies calls admitted
// while the circuit was closed and finishing after it opened change neither
// its state nor its backoff.
func TestDatabaseGate_IgnoresCallsAdmittedBeforeOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := captureLogs(t)
		provider := newFakeProvider()
		gate, stop := connectedGate(t, provider)
		defer stop()

		var mu sync.Mutex
		var held []chan struct{} // one per call, in the order calls arrive
		provider.setAnswer(func(context.Context) error {
			release := make(chan struct{})
			mu.Lock()
			held = append(held, release)
			mu.Unlock()
			<-release
			return errRefused
		})
		results := make(chan error, 6)
		finish := func(i int) error {
			mu.Lock()
			close(held[i])
			mu.Unlock()
			return <-results
		}
		for range 5 {
			go func() { results <- lookup.call(t.Context(), gate) }()
		}
		synctest.Wait()
		for i := range 3 {
			require.ErrorIs(t, finish(i), errRefused)
		}
		require.ErrorIs(t, lookup.call(t.Context(), gate), errDatabaseUnavailable)

		time.Sleep(time.Second)
		go func() { results <- lookup.call(t.Context(), gate) }()
		synctest.Wait()
		require.ErrorIs(t, finish(5), errRefused, "trial")
		for i := 3; i < 5; i++ {
			require.ErrorIs(t, finish(i), errRefused)
		}

		provider.failWith(errRefused)
		require.ErrorIs(t, trialAt(t, gate, provider, 2*time.Second, "after the late failures"), errRefused)
		refused := "failed to connect to PostgreSQL database: connection refused: failed to ping database"
		assert.Equal(t, []map[string]any{
			{"level": "WARN", "msg": "ingester.database.circuit_opened", "err": refused, "retry_in": float64(time.Second)},
			{"level": "WARN", "msg": "ingester.database.circuit_opened", "err": refused, "retry_in": float64(2 * time.Second)},
			{"level": "WARN", "msg": "ingester.database.circuit_opened", "err": refused, "retry_in": float64(4 * time.Second)},
		}, circuitLogs(t, logs))
	})
}

// TestDatabaseGate_CircuitStateReported verifies the up gauge reads 1 while
// the circuit is closed and 0 while open, and only a change of state is
// logged.
func TestDatabaseGate_CircuitStateReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := captureLogs(t)
		provider := newFakeProvider()
		gate, stop := connectedGate(t, provider)
		defer stop()
		up := func() float64 { return metricValue(t, "ingester_database_up", map[string]string{}) }
		assert.Equal(t, 1.0, up())

		provider.failWith(errRefused)
		for range 2 {
			require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused)
		}
		assert.Equal(t, 1.0, up())
		assert.Empty(t, circuitLogs(t, logs))

		require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused)
		assert.Zero(t, up())
		require.ErrorIs(t, lookup.call(t.Context(), gate), errDatabaseUnavailable)
		time.Sleep(time.Second)
		require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused)
		assert.Zero(t, up())

		provider.failWith(nil)
		time.Sleep(2 * time.Second)
		require.NoError(t, lookup.call(t.Context(), gate))
		assert.Equal(t, 1.0, up())
		require.NoError(t, lookup.call(t.Context(), gate))

		refused := "failed to connect to PostgreSQL database: connection refused: failed to ping database"
		assert.Equal(t, []map[string]any{
			{"level": "WARN", "msg": "ingester.database.circuit_opened", "err": refused, "retry_in": float64(time.Second)},
			{"level": "WARN", "msg": "ingester.database.circuit_opened", "err": refused, "retry_in": float64(2 * time.Second)},
			{"level": "INFO", "msg": "ingester.database.circuit_closed"},
		}, circuitLogs(t, logs))
	})
}

var (
	errNetwork           = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	errConnectionFailure = &pq.Error{Code: "08006"}
	errStatementCanceled = &pq.Error{Code: "57014"}
)

// gateCall is a store call through the gate.
type gateCall struct {
	name string
	call func(ctx context.Context, gate *DatabaseGate) error
}

var (
	lookup = gateCall{name: "lookup", call: func(ctx context.Context, gate *DatabaseGate) error {
		_, err := gate.GetSeriesMetadataByNames(ctx, []string{"m"}, "")
		return err
	}}
	upsert = gateCall{name: "upsert", call: func(ctx context.Context, gate *DatabaseGate) error {
		return gate.UpsertMetricsCatalog(ctx, []db.MetricCatalogItem{{Name: "m"}})
	}}
	gateCalls = []gateCall{lookup, upsert}
)

// connectedGate returns a gate connected to provider, whose open delay is
// the whole backoff, and the stop of its Run.
func connectedGate(t *testing.T, provider *fakeProvider) (*DatabaseGate, func()) {
	gate := NewDatabaseGate(func(context.Context) (db.Provider, error) { return provider, nil })
	gate.randInt64N = func(n int64) int64 { return n - 1 }
	stop := runGate(t, gate)
	synctest.Wait()
	return gate, stop
}

// openCircuit opens gate's closed circuit with three connection failures.
func openCircuit(t *testing.T, gate *DatabaseGate, provider *fakeProvider) {
	t.Helper()
	provider.failWith(errRefused)
	for range 3 {
		require.ErrorIs(t, lookup.call(t.Context(), gate), errRefused)
	}
	require.ErrorIs(t, lookup.call(t.Context(), gate), errDatabaseUnavailable)
}

// trialAt asserts gate's open circuit refuses lookups until delay has
// elapsed and lets one reach the database at delay, and returns its error.
func trialAt(t *testing.T, gate *DatabaseGate, provider *fakeProvider, delay time.Duration, msgAndArgs ...any) error {
	t.Helper()
	calls := provider.calls.Load()
	time.Sleep(delay - time.Nanosecond)
	require.ErrorIs(t, lookup.call(t.Context(), gate), errDatabaseUnavailable, msgAndArgs...)
	require.Equal(t, calls, provider.calls.Load(), msgAndArgs...)
	time.Sleep(time.Nanosecond)
	err := lookup.call(t.Context(), gate)
	require.Equal(t, calls+1, provider.calls.Load(), msgAndArgs...)
	return err
}

// captureLogs sends the default logger's records, at debug level and up, as
// JSON without time to the returned buffer until t ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// circuitLogs returns the circuit's log records in logs.
func circuitLogs(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.Lines(logs.String()) {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, "ingester.database.circuit_") {
			records = append(records, record)
		}
	}
	return records
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
// name and recording catalog upserts, unless setAnswer makes its lookups and
// upserts fail. Methods not overridden panic.
type fakeProvider struct {
	db.Provider
	store    fakeStore
	closeErr error
	closed   atomic.Int64
	calls    atomic.Int64 // lookups and upserts

	mu     sync.Mutex
	answer func(ctx context.Context) error
}

func newFakeProvider() *fakeProvider { return &fakeProvider{} }

// setAnswer makes later lookups and upserts return answer(ctx); nil: succeed.
func (p *fakeProvider) setAnswer(answer func(ctx context.Context) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = answer
}

// failWith makes later lookups and upserts fail with err; nil: succeed.
func (p *fakeProvider) failWith(err error) {
	p.setAnswer(func(context.Context) error { return err })
}

func (p *fakeProvider) respond(ctx context.Context) error {
	p.calls.Add(1)
	p.mu.Lock()
	answer := p.answer
	p.mu.Unlock()
	if answer == nil {
		return nil
	}
	return answer(ctx)
}

func (p *fakeProvider) GetSeriesMetadataByNames(ctx context.Context, names []string, _ string) ([]models.MetricMetadata, error) {
	if err := p.respond(ctx); err != nil {
		return nil, err
	}
	metas := make([]models.MetricMetadata, 0, len(names))
	for _, n := range names {
		metas = append(metas, models.MetricMetadata{Name: n})
	}
	return metas, nil
}

func (p *fakeProvider) UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error {
	if err := p.respond(ctx); err != nil {
		return err
	}
	return p.store.UpsertMetricsCatalog(ctx, items)
}

func (p *fakeProvider) upsertedNames() [][]string { return p.store.upsertedNames() }

func (p *fakeProvider) Close() error {
	p.closed.Add(1)
	return p.closeErr
}
