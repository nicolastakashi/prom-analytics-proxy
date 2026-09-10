package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/oklog/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/leaderelection"
)

// testBudget is deliberately unlike any interval these tests pass, so a
// budget that reached the scheduler as an interval (or the reverse) shows up
// as a failure rather than as an unnoticed swap.
const testBudget = 90 * time.Second

// fakePeriodicJob is periodicJob's test double: counts the cycles it was
// asked to run, and closes ran on the first of them when a test needs to
// wait for one rather than race it.
type fakePeriodicJob struct {
	runs int
	once sync.Once
	ran  chan struct{}
}

func (f *fakePeriodicJob) RunOnce(context.Context) {
	f.runs++
	if f.ran != nil {
		f.once.Do(func() { close(f.ran) })
	}
}

// fakeElector is leaderelection.Elector's test double. It records the lease
// name it was called with, then invokes fn itself so a test can observe
// what actually got scheduled through it. fn is a scheduling loop that
// returns only once its context ends, so this hands it a context of its
// own that expires rather than one that never does.
type fakeElector struct {
	name string
	err  error
	// budgets, when set, makes this elector report cycles to itself, so a
	// test can see what the scheduler declared.
	budgets chan time.Duration
}

func (f *fakeElector) Run(ctx context.Context, name string, fn func(context.Context, leaderelection.CycleReporter)) error {
	f.name = name
	fnCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	var reporter leaderelection.CycleReporter
	if f.budgets != nil {
		reporter = f
	}
	fn(fnCtx, reporter)
	return f.err
}

func (f *fakeElector) CycleStarted(_ context.Context, _ context.CancelFunc, budget time.Duration) {
	select {
	case f.budgets <- budget:
	default:
	}
}

func TestAddPeriodicJob_SchedulesCyclesThroughTheElectorUnderTheGivenLeaseName(t *testing.T) {
	var g run.Group
	job := &fakePeriodicJob{}
	elector := &fakeElector{err: context.Canceled}

	addPeriodicJob(&g, elector, "test-lease", time.Hour, testBudget, job)

	err := g.Run()
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "test-lease", elector.name, "must elect under the lease name the caller gave it, not a hardcoded one")
	assert.GreaterOrEqual(t, job.runs, 1, "leadership must schedule the job's cycles, not merely be acquired")
}

// TestAddPeriodicJob_SoleInstanceElectorSchedulesCyclesToo covers the
// deployment that used to be wired a second way: a sole instance holds
// leadership outright, so the same single path schedules its cycles.
func TestAddPeriodicJob_SoleInstanceElectorSchedulesCyclesToo(t *testing.T) {
	var g run.Group
	job := &fakePeriodicJob{ran: make(chan struct{})}

	addPeriodicJob(&g, leaderelection.NewSoleInstance(prometheus.NewRegistry()), "sole-lease", time.Hour, testBudget, job)
	// End the group only once a cycle has actually run: an elector hands
	// leadership to nobody if its context is already canceled, so racing
	// cancellation against the first cycle would prove nothing either way.
	g.Add(func() error { <-job.ran; return errors.New("stop") }, func(error) {})

	require.Error(t, g.Run())
	assert.GreaterOrEqual(t, job.runs, 1, "a sole instance must get its cycles scheduled like any other leader")
}

// blockingPeriodicJob's RunOnce blocks until its context ends, recording
// that it actually observed cancellation - proving addPeriodicJob's
// interrupt function reaches the context the job's cycle was handed, not
// just that run.Group's execute eventually returns some way or other.
type blockingPeriodicJob struct {
	started  chan struct{}
	canceled chan struct{}
}

func (b *blockingPeriodicJob) RunOnce(ctx context.Context) {
	close(b.started)
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

	blocking := &blockingPeriodicJob{started: make(chan struct{}), canceled: make(chan struct{})}
	addPeriodicJob(&g, leaderelection.NewSoleInstance(prometheus.NewRegistry()), "blocking-lease", time.Hour, testBudget, blocking)

	// A sibling actor that fails once the job is genuinely mid-cycle: that
	// failure is what makes g invoke every other actor's interrupt, and
	// waiting for the cycle first keeps the two from racing.
	g.Add(func() error { <-blocking.started; return errors.New("sibling actor failed") }, func(error) {})

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

// TestAddPeriodicJob_DeclaresTheBudgetItWasGivenNotTheInterval proves the two
// durations don't get crossed on the way through: the scheduler reports the
// caller's budget, and the interval it ticks on stays the interval. Nothing
// else here would notice a swap, since a job's cycles run the same either
// way until something enforces the budget.
func TestAddPeriodicJob_DeclaresTheBudgetItWasGivenNotTheInterval(t *testing.T) {
	var g run.Group
	elector := &fakeElector{err: context.Canceled, budgets: make(chan time.Duration, 1)}
	const interval = 12 * time.Hour

	addPeriodicJob(&g, elector, "budget-lease", interval, testBudget, &fakePeriodicJob{})

	require.ErrorIs(t, g.Run(), context.Canceled)
	select {
	case got := <-elector.budgets:
		assert.Equal(t, testBudget, got, "the declared budget must be the caller's budget, not its interval")
		assert.NotEqual(t, interval, got)
	default:
		t.Fatal("no cycle was ever reported, so nothing was proven about the budget it declared")
	}
}
