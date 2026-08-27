package leaderelection

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestElector_Run_PeriodicJob_CycleOverBudget_CancelsAndStepsDown is the
// enforcement side of job-cycle budgets (see docs/leader-election.md,
// "Reporting job cycles"): a PeriodicJob cycle that runs past its declared
// budget must be treated exactly like losing the lease to another holder -
// canceled and stepped down through the existing, already-tested
// graceful-release path, not a new one. Goes through the real
// PeriodicJob wiring (not a hand-rolled fn calling CycleStarted directly)
// since that's what every actual caller uses.
func TestElector_Run_PeriodicJob_CycleOverBudget_CancelsAndStepsDown(t *testing.T) {
	db := newTestPostgresDB(t)
	const ttl = time.Hour // never approached: isolates the budget check from ordinary TTL expiry
	const renewInterval = 5 * time.Millisecond
	const budget = 20 * time.Millisecond
	const leaseName = "cycle-budget-test"

	strat := newLeaseStrategy(db, ttl, renewInterval)
	el := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond, max: 15 * time.Millisecond})

	cycleCanceled := make(chan struct{})
	var once sync.Once // self-retry means the job may run again immediately once this process re-wins its own just-relinquished lease - closing an unguarded channel twice would panic, not prove anything extra

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	job := PeriodicJob(time.Hour, budget, func(cycleCtx context.Context) {
		<-cycleCtx.Done()
		once.Do(func() {
			close(cycleCanceled)
			cancel() // stop the outer ctx now: self-retry would otherwise keep re-winning and re-exceeding this same short budget for the rest of the test's window, proving nothing more than the first cancellation already did
		})
	})

	runErr := make(chan error, 1)
	go func() { runErr <- el.Run(ctx, leaseName, job) }()

	// Bounded well under the outer ctx's 1s timeout: the outer ctx expiring
	// on its own would *also* cascade into cycleCtx.Done() (it's a child
	// context), which would look identical to a prompt budget-driven
	// cancellation if this wait weren't strictly shorter - it must fire
	// because of the ~20ms budget, not because the whole test ran out of
	// time.
	select {
	case <-cycleCanceled:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("the cycle's own context was never canceled after its budget elapsed")
	}
	assert.NoError(t, <-runErr, "Run must still return nil (only ctx cancellation ends it) after a budget-driven step-down, same as any other retryable event")
}

// TestElector_Run_PeriodicJob_CycleWithinBudget_IsNotCanceled proves the
// check above isn't just "cancel eventually regardless" - a cycle that
// finishes comfortably inside its declared budget must not be touched.
func TestElector_Run_PeriodicJob_CycleWithinBudget_IsNotCanceled(t *testing.T) {
	db := newTestPostgresDB(t)
	const ttl = time.Hour
	const renewInterval = 5 * time.Millisecond
	const budget = time.Hour // never exceeded during this short test

	strat := newLeaseStrategy(db, ttl, renewInterval)
	el := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond, max: 15 * time.Millisecond})

	var cycleCanceled bool
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	job := PeriodicJob(time.Hour, budget, func(cycleCtx context.Context) {
		select {
		case <-cycleCtx.Done():
			cycleCanceled = true
		case <-time.After(50 * time.Millisecond):
		}
		// End the test here regardless of outcome - see the sibling test
		// above for why letting self-retry continue would confound this.
		cancel()
	})

	_ = el.Run(ctx, "cycle-within-budget-test", job)

	assert.False(t, cycleCanceled, "a cycle well within its declared budget must not be canceled")
}

// TestElector_Run_RawFnIgnoringReporter_IsUnaffectedByBudgetEnforcement
// proves the mechanism is strictly opt-in: a caller that bypasses
// PeriodicJob and calls Run with a raw fn that never reports a cycle gets
// exactly today's behavior - the watchdog has nothing to compare against,
// so a hung fn's lease still renews for as long as the process is alive,
// unaffected by anything in this file.
func TestElector_Run_RawFnIgnoringReporter_IsUnaffectedByBudgetEnforcement(t *testing.T) {
	db := newTestPostgresDB(t)
	const ttl = 60 * time.Millisecond
	const renewInterval = 15 * time.Millisecond
	const leaseName = "no-report-test"

	strat := newLeaseStrategy(db, ttl, renewInterval)
	el := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond, max: 15 * time.Millisecond})

	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = el.Run(ctx, leaseName, func(fnCtx context.Context, _ CycleReporter) {
			close(entered)
			<-make(chan struct{}) // permanently hung, and never calls CycleStarted
		})
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("fn never started")
	}

	var fenceAfterAcquire int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT fence_token FROM leader_leases WHERE lease_name = $1`, leaseName).Scan(&fenceAfterAcquire))

	time.Sleep(ttl * 10) // long enough that, if renewal genuinely depended on progress, the lease would have expired many times over

	var fenceNow int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT fence_token FROM leader_leases WHERE lease_name = $1`, leaseName).Scan(&fenceNow))

	assert.Equal(t, fenceAfterAcquire, fenceNow, "fence_token unchanged - same holder, still renewing: a job that never reports a cycle is unaffected by budget enforcement, exactly as documented")
}

// TestWatchdog_FinishedCycleIdlePastItsBudget_KeepsLeadership proves a
// cycle that ended is never mistaken for one that overran. A job whose
// interval is longer than its budget - the inventory syncer's default
// shape, a 10m interval around a 300s budget - is idle past that budget on
// every single cycle, and stepping down each time would flap leadership
// indefinitely while running extra cycles on every re-acquisition.
func TestWatchdog_FinishedCycleIdlePastItsBudget_KeepsLeadership(t *testing.T) {
	const renewInterval = 5 * time.Millisecond
	const ttl = time.Hour // never approached: isolates the budget check
	const budget = 20 * time.Millisecond

	renewer := &fakeLeaseRenewer{script: []renewOutcome{{ok: true}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := &cycleTracker{}
	done := watchdog(leaderCtx, cancel, renewer, "finished-cycle-idle-test", renewInterval, ttl, tracker)

	// One cycle reported and then finished inside its budget - PeriodicJob's
	// exact sequence, including canceling that cycle's context on the way
	// out.
	cycleCtx, cycleCancel := context.WithCancel(leaderCtx)
	tracker.CycleStarted(cycleCtx, cycleCancel, budget)
	cycleCancel()

	select {
	case <-leaderCtx.Done():
		t.Fatal("leadership was given up while the job sat idle between cycles, having finished the last one inside its budget")
	case <-time.After(5 * budget):
	}

	cancel()
	<-done
	assert.Greater(t, renewer.callCount(), 1, "the watchdog must have gone on renewing throughout")
}

// TestWatchdog_RunningCycleOverBudget_CancelsAndStepsDown is the counterpart
// that has to keep holding: a cycle still running past its declared budget
// is exactly what this check exists to catch, and the distinction from the
// finished cycle above is the reported cycle's own context.
func TestWatchdog_RunningCycleOverBudget_CancelsAndStepsDown(t *testing.T) {
	const renewInterval = 5 * time.Millisecond
	const ttl = time.Hour
	const budget = 20 * time.Millisecond

	renewer := &fakeLeaseRenewer{script: []renewOutcome{{ok: true}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := &cycleTracker{}
	done := watchdog(leaderCtx, cancel, renewer, "running-cycle-over-budget-test", renewInterval, ttl, tracker)

	// A cycle that never returns on its own: reported, and left running.
	cycleCtx, cycleCancel := context.WithCancel(leaderCtx)
	defer cycleCancel()
	tracker.CycleStarted(cycleCtx, cycleCancel, budget)

	select {
	case <-cycleCtx.Done():
	case <-time.After(20 * budget):
		t.Fatal("a cycle still running past its declared budget was never canceled")
	}
	assert.Error(t, leaderCtx.Err(), "exceeding the budget must also step down, not just cancel the cycle")
	<-done
}
