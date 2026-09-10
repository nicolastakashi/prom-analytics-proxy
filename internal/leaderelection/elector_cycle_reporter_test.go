package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reporterStrategy hands out one leadership term with a reporter of its own
// choosing, then reports contention forever, so Run calls fn exactly once.
type reporterStrategy struct {
	reporter CycleReporter
	granted  bool
}

func (s *reporterStrategy) acquireOrHold(ctx context.Context, _ string) (acquisition, bool, error) {
	if s.granted {
		return acquisition{}, false, nil
	}
	s.granted = true
	leaderCtx, cancel := context.WithCancel(ctx)
	return acquisition{leaderCtx: leaderCtx, release: cancel, reporter: s.reporter}, true, nil
}

// stubReporter is a CycleReporter that records nothing: this test cares only
// about which instance reaches fn.
type stubReporter struct{}

func (stubReporter) CycleStarted(context.Context, context.CancelFunc, time.Duration) {}

// TestElector_Run_HandsTheStrategysReporterToFn proves the reporter a
// strategy returns from acquireOrHold is the one fn receives. Without it a
// strategy could coordinate cycles it never hears about: the whole path
// exists so a strategy that acts on reported cycles gets to see them, and
// nothing else in this package would notice the plumbing being cut.
func TestElector_Run_HandsTheStrategysReporterToFn(t *testing.T) {
	want := stubReporter{}
	el := newTestElector(&reporterStrategy{reporter: want}, backoffConfig{initial: time.Millisecond, max: 2 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan CycleReporter, 1)
	require.NoError(t, el.Run(ctx, "reporter-plumbing-test", func(_ context.Context, reporter CycleReporter) {
		got <- reporter
		cancel() // one term is all this needs
	}))

	select {
	case reporter := <-got:
		assert.Equal(t, want, reporter, "fn must receive the reporter the strategy returned, not a substitute")
	default:
		t.Fatal("fn was never called, so nothing was proven about the reporter it would have received")
	}
}

// TestElector_Run_NilReporterFromStrategyReachesFnAsNil proves a strategy
// with nothing to coordinate (advisory-lock today) leaves fn with a nil
// reporter rather than anything it would have to guard against.
func TestElector_Run_NilReporterFromStrategyReachesFnAsNil(t *testing.T) {
	el := newTestElector(&reporterStrategy{reporter: nil}, backoffConfig{initial: time.Millisecond, max: 2 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := false
	require.NoError(t, el.Run(ctx, "nil-reporter-test", func(_ context.Context, reporter CycleReporter) {
		called = true
		assert.Nil(t, reporter)
		cancel()
	}))
	assert.True(t, called, "fn must have run for this to prove anything")
}
