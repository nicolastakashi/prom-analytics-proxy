package api

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/oklog/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

// fakePeriodicJob is periodicJob's test double: counts the cycles it was
// asked to run.
type fakePeriodicJob struct {
	runs int
}

func (f *fakePeriodicJob) RunOnce(context.Context) { f.runs++ }

// fakeElector is leaderelection.Elector's test double. It records the lease
// name it was called with, then invokes fn itself so a test can observe
// what actually got scheduled through it. fn is a scheduling loop that
// returns only once its context ends, so this hands it a context of its
// own that expires rather than one that never does.
type fakeElector struct {
	name string
	err  error
}

func (f *fakeElector) Run(ctx context.Context, name string, fn func(context.Context)) error {
	f.name = name
	fnCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	fn(fnCtx)
	return f.err
}

// stopGroup returns an actor that ends g as soon as it runs, so every other
// actor's interrupt fires and the scheduling loops under test return.
func stopGroup() (func() error, func(error)) {
	return func() error { return errors.New("stop") }, func(error) {}
}

func TestAddPeriodicJob_PostgreSQL_SchedulesCyclesThroughElectorUnderTheGivenLeaseName(t *testing.T) {
	var g run.Group
	job := &fakePeriodicJob{}
	elector := &fakeElector{err: context.Canceled}

	addPeriodicJob(&g, db.PostGreSQL, elector, "test-lease", time.Hour, job)

	err := g.Run()
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "test-lease", elector.name, "must elect under the lease name the caller gave it, not a hardcoded one")
	assert.GreaterOrEqual(t, job.runs, 1, "leadership must schedule the job's cycles, not merely be acquired")
}

func TestAddPeriodicJob_NonPostgreSQL_SchedulesCyclesWithoutTouchingTheElector(t *testing.T) {
	var g run.Group
	job := &fakePeriodicJob{}

	// A nil elector would panic if addPeriodicJob ever called a method on
	// it on this path - proving the leaderless path never touches it.
	addPeriodicJob(&g, db.SQLite, nil, "unused-lease", time.Hour, job)
	g.Add(stopGroup())

	require.Error(t, g.Run())
	assert.GreaterOrEqual(t, job.runs, 1, "a non-PostgreSQL provider must still get its cycles scheduled")
}

// blockingPeriodicJob's RunOnce blocks until its context ends, recording
// that it actually observed cancellation - proving addPeriodicJob's
// interrupt function reaches the context the job's cycle was handed, not
// just that run.Group's execute eventually returns some way or other.
type blockingPeriodicJob struct {
	canceled chan struct{}
}

func (b *blockingPeriodicJob) RunOnce(ctx context.Context) {
	<-ctx.Done()
	close(b.canceled)
}

// TestAddPeriodicJob_InterruptCancelsTheContextPassedToTheJob proves the
// cancel func addPeriodicJob wires into g's interrupt actually reaches the
// context the job's cycle was handed - the mechanism run.Group depends on
// to stop every actor once any one of them fails. Hence the two jobs: a
// sibling actor failing is what makes g invoke every other actor's
// interrupt.
func TestAddPeriodicJob_InterruptCancelsTheContextPassedToTheJob(t *testing.T) {
	var g run.Group

	failing := &fakePeriodicJob{}
	addPeriodicJob(&g, db.PostGreSQL, &fakeElector{err: errors.New("boom")}, "failing-lease", time.Hour, failing)

	blocking := &blockingPeriodicJob{canceled: make(chan struct{})}
	addPeriodicJob(&g, db.SQLite, nil, "unused-lease", time.Hour, blocking)

	// g.Run() itself blocks until every actor's execute function returns -
	// if the interrupt below never reaches blocking, g.Run() never
	// returns either, so both waits need their own bound rather than
	// letting a broken interrupt hang the whole test binary.
	runErr := make(chan error, 1)
	go func() { runErr <- g.Run() }()

	select {
	case <-blocking.canceled:
	case <-time.After(time.Second):
		t.Fatal("the blocking job's context was never canceled after a sibling actor failed - addPeriodicJob's interrupt isn't reaching the job")
	}

	select {
	case err := <-runErr:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("g.Run() never returned after the blocking job's context was canceled")
	}
}

// TestRunPeriodically_RunsOneCycleImmediatelyThenOnePerJitteredInterval
// pins the whole schedule the one shared scheduler produces: a cycle at
// once, then one per interval, spaced by no less than interval and no more
// than 20% beyond it, until its context ends. The synctest bubble's clock
// makes those spacings exact rather than approximate, so the jitter's upper
// bound is checkable at a realistic interval instead of inferred from a
// tiny one.
func TestRunPeriodically_RunsOneCycleImmediatelyThenOnePerJitteredInterval(t *testing.T) {
	const (
		interval = 10 * time.Minute
		cycles   = 3
		// One schedule draws its jitter once, so it samples the range once;
		// repeated schedules make a draw outside the bound very unlikely to
		// go unnoticed. Fake time makes the repetition free.
		schedules = 20
	)

	synctest.Test(t, func(t *testing.T) {
		for s := range schedules {
			ctx, cancel := context.WithCancel(context.Background())

			start := time.Now()
			var ranAt []time.Duration
			returned := make(chan struct{})
			go func() {
				defer close(returned)
				runPeriodically(ctx, interval, func(context.Context) {
					ranAt = append(ranAt, time.Since(start))
					if len(ranAt) == cycles {
						cancel()
					}
				})
			}()
			<-returned
			cancel()

			require.Len(t, ranAt, cycles, "the scheduler must keep running cycles until its context ends")
			assert.Zero(t, ranAt[0], "the first cycle runs immediately, not after a full interval")
			for i := 1; i < len(ranAt); i++ {
				gap := ranAt[i] - ranAt[i-1]
				assert.GreaterOrEqual(t, gap, interval, "schedule %d cycle %d ran early: jitter must only ever delay a tick", s, i)
				assert.Less(t, gap, interval+interval/5, "schedule %d cycle %d ran late: jitter must stay within 20%% of the interval", s, i)
			}
		}
	})
}

// TestRunPeriodically_IntervalTooSmallToJitter covers the jitter floor: an
// interval below 5ns leaves 20% of it under a nanosecond, and the floor is
// what keeps that from panicking.
func TestRunPeriodically_IntervalTooSmallToJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runs := 0
		returned := make(chan struct{})
		go func() {
			defer close(returned)
			runPeriodically(ctx, 4*time.Nanosecond, func(context.Context) {
				runs++
				if runs == 2 {
					cancel()
				}
			})
		}()
		<-returned

		assert.Equal(t, 2, runs)
	})
}
