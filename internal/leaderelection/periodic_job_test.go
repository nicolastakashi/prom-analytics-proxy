package leaderelection

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPeriodicJob_CallsCycleImmediatelyThenOnEachTick proves the loop
// doesn't wait out the first interval before doing any work, then keeps
// calling cycle repeatedly for as long as ctx is alive.
func TestPeriodicJob_CallsCycleImmediatelyThenOnEachTick(t *testing.T) {
	var calls atomic.Int32
	job := PeriodicJob(10*time.Millisecond, time.Hour, func(context.Context) {
		calls.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	job(ctx, nil)

	assert.GreaterOrEqual(t, calls.Load(), int32(3), "must call cycle immediately and then repeatedly on tick, not just once")
}

// TestPeriodicJob_ReportsEachCycleToReporter proves every single cycle
// invocation reports itself, with a usable cancel and the configured
// budget - not just the first one.
func TestPeriodicJob_ReportsEachCycleToReporter(t *testing.T) {
	reporter := &recordingReporter{}
	job := PeriodicJob(10*time.Millisecond, 42*time.Second, func(context.Context) {})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()
	job(ctx, reporter)

	reported := reporter.reported()
	require.GreaterOrEqual(t, len(reported), 3, "every cycle must report itself, not just the first")
	for i, c := range reported {
		assert.Equal(t, 42*time.Second, c.budget, "cycle %d: the declared budget must reach the reporter unchanged", i)
	}
}

// TestPeriodicJob_CancelingOneCycleDoesNotStopTheLoop proves cycles are
// independent: externally canceling one stuck cycle's context (the exact
// action a budget-enforcing reporter takes) unblocks that cycle and lets
// the loop continue on to later ticks, rather than the whole loop being
// wedged forever. PeriodicJob runs cycles synchronously, one at a time -
// a cycle that never returns on its own genuinely blocks all future
// ticks until something cancels it, which is exactly what this proves
// recovers cleanly.
func TestPeriodicJob_CancelingOneCycleDoesNotStopTheLoop(t *testing.T) {
	var calls atomic.Int32
	firstCycleCanceled := make(chan struct{})
	firstCycleStarted := make(chan context.CancelFunc, 1)

	job := PeriodicJob(10*time.Millisecond, time.Hour, func(cycleCtx context.Context) {
		if calls.Add(1) == 1 {
			<-cycleCtx.Done() // simulates a stuck first cycle; only an external cancel ends this
		}
		// every other cycle returns immediately, proving the loop is healthy again
	})

	reporter := &recordingReporter{onStart: func(cancel context.CancelFunc) {
		select {
		case firstCycleStarted <- cancel:
		default:
		}
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		firstCancel := <-firstCycleStarted
		time.Sleep(30 * time.Millisecond) // let the loop sit genuinely stuck on cycle 1 for a while before unblocking it
		firstCancel()
		close(firstCycleCanceled)
	}()

	job(ctx, reporter)

	select {
	case <-firstCycleCanceled:
	default:
		t.Fatal("test ended before the external cancel ever fired - not a valid run")
	}
	assert.GreaterOrEqual(t, calls.Load(), int32(2), "canceling the stuck first cycle must unblock it and let later ticks still run")
}

// TestPeriodicJob_NilReporter_DoesNotPanic proves nil is a genuinely valid
// reporter, not something callers must guard against themselves.
func TestPeriodicJob_NilReporter_DoesNotPanic(t *testing.T) {
	job := PeriodicJob(10*time.Millisecond, time.Hour, func(context.Context) {})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	assert.NotPanics(t, func() { job(ctx, nil) })
}

// TestPeriodicJob_StopsWhenOuterCtxCanceled proves the loop actually exits
// (doesn't leak) once its outer ctx ends.
func TestPeriodicJob_StopsWhenOuterCtxCanceled(t *testing.T) {
	job := PeriodicJob(5*time.Millisecond, time.Hour, func(context.Context) {})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		job(ctx, nil)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PeriodicJob did not return after its outer ctx was canceled")
	}
}

// reportedCycle is one CycleStarted call as recordingReporter saw it,
// including the reported context's state at that moment.
type reportedCycle struct {
	ctx         context.Context
	errAtReport error
	budget      time.Duration
}

// recordingReporter is CycleReporter's test double: it records every cycle
// reported to it, and hands each cycle's cancel to onStart when set, for
// tests that act on one specific cycle from outside cycle itself.
type recordingReporter struct {
	mu      sync.Mutex
	cycles  []reportedCycle
	onStart func(cancel context.CancelFunc)
}

func (r *recordingReporter) CycleStarted(cycleCtx context.Context, cancel context.CancelFunc, budget time.Duration) {
	r.mu.Lock()
	r.cycles = append(r.cycles, reportedCycle{ctx: cycleCtx, errAtReport: cycleCtx.Err(), budget: budget})
	onStart := r.onStart
	r.mu.Unlock()

	// Outside the lock: onStart is test-supplied and may report back in.
	if onStart != nil {
		onStart(cancel)
	}
}

func (r *recordingReporter) reported() []reportedCycle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reportedCycle(nil), r.cycles...)
}

// TestPeriodicJob_TickStaysWithinTwentyPercentJitterOfTheInterval pins the
// spacing the loop produces: a cycle at once, then one per interval, never
// earlier than the interval and never more than 20% beyond it. The synctest
// bubble's clock makes those spacings exact, so this can assert the bound at
// a realistic interval rather than infer it from a tiny one - and since one
// loop draws its jitter once, repeated schedules are what make a draw
// outside the bound unlikely to go unnoticed. Fake time makes the
// repetition free.
func TestPeriodicJob_TickStaysWithinTwentyPercentJitterOfTheInterval(t *testing.T) {
	const (
		interval  = 10 * time.Minute
		cycles    = 3
		schedules = 20
	)

	synctest.Test(t, func(t *testing.T) {
		for s := range schedules {
			ctx, cancel := context.WithCancel(context.Background())

			start := time.Now()
			var ranAt []time.Duration
			job := PeriodicJob(interval, time.Hour, func(context.Context) {
				ranAt = append(ranAt, time.Since(start))
				if len(ranAt) == cycles {
					cancel()
				}
			})

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				job(ctx, nil)
			}()
			<-returned
			cancel()

			require.Len(t, ranAt, cycles, "the loop must keep running cycles until its context ends")
			assert.Zero(t, ranAt[0], "the first cycle runs immediately, not after a full interval")
			for i := 1; i < len(ranAt); i++ {
				gap := ranAt[i] - ranAt[i-1]
				assert.GreaterOrEqual(t, gap, interval, "schedule %d cycle %d ran early: jitter must only ever delay a tick", s, i)
				assert.Less(t, gap, interval+interval/5, "schedule %d cycle %d ran late: jitter must stay within 20%% of the interval", s, i)
			}
		}
	})
}

// TestPeriodicJob_IntervalTooSmallToJitter covers the jitter floor: an
// interval below 5ns leaves 20% of it under a nanosecond, and the floor is
// what keeps that from panicking.
func TestPeriodicJob_IntervalTooSmallToJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runs := 0
		job := PeriodicJob(4*time.Nanosecond, time.Hour, func(context.Context) {
			runs++
			if runs == 2 {
				cancel()
			}
		})

		returned := make(chan struct{})
		go func() {
			defer close(returned)
			job(ctx, nil)
		}()
		<-returned

		assert.Equal(t, 2, runs)
	})
}

// TestPeriodicJob_ReportedContextEndsExactlyWhenItsCycleDoes proves the
// reported context is what marks a cycle live or over: alive while its
// cycle runs, canceled once that cycle returns, and independently per
// cycle. A reporter enforcing a budget depends on this - without it, a
// cycle that finished long ago is indistinguishable from one still
// running, and any budget check on the finished one fires against a job
// that is doing nothing wrong.
func TestPeriodicJob_ReportedContextEndsExactlyWhenItsCycleDoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reporter := &recordingReporter{}
		const cycles = 3

		var seen int
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		job := PeriodicJob(time.Minute, time.Hour, func(cycleCtx context.Context) {
			seen++
			assert.NoError(t, cycleCtx.Err(), "cycle %d must run on a live context", seen)
			if seen == cycles {
				cancel()
			}
		})

		returned := make(chan struct{})
		go func() {
			defer close(returned)
			job(ctx, reporter)
		}()
		<-returned

		reported := reporter.reported()
		require.Len(t, reported, cycles)
		for i, c := range reported {
			assert.NoError(t, c.errAtReport, "cycle %d was reported as already over before it ran", i)
			assert.Error(t, c.ctx.Err(), "cycle %d's reported context must end when that cycle returns, so a finished cycle can't be mistaken for a running one", i)
		}
	})
}

// TestPeriodicJob_PanickingCycleEndsItsReportedContextAndPropagates proves
// the two things a panicking cycle must not break: its reported context is
// canceled on the way out, so nothing downstream keeps treating that cycle
// as still running, and the panic itself is not swallowed by the loop -
// Elector.Run's release-then-repanic path is what handles it, and it can
// only do that if the panic reaches it.
func TestPeriodicJob_PanickingCycleEndsItsReportedContextAndPropagates(t *testing.T) {
	reporter := &recordingReporter{}
	job := PeriodicJob(time.Hour, time.Minute, func(context.Context) {
		panic("cycle blew up")
	})

	assert.PanicsWithValue(t, "cycle blew up", func() {
		job(context.Background(), reporter)
	}, "the loop must not swallow a cycle's panic")

	reported := reporter.reported()
	require.Len(t, reported, 1, "the cycle must have been reported before it ran")
	assert.Error(t, reported[0].ctx.Err(),
		"a panicking cycle's reported context must end like any other, or a budget check would keep watching a cycle that is long gone")
}
