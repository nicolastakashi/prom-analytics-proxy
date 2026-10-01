package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSoleInstance_HoldsLeadershipUntilItsContextEnds proves the whole
// contract this Elector exists for: fn runs on the first attempt, with no
// contention to wait out and nothing to acquire, until the caller's context
// ends - at which point Run returns nil, the same as any other Elector
// stopping for that reason.
func TestSoleInstance_HoldsLeadershipUntilItsContextEnds(t *testing.T) {
	el := NewSoleInstance(prometheus.NewRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	running := make(chan context.Context, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- el.Run(ctx, "sole-instance-test", func(fnCtx context.Context, reporter CycleReporter) {
			assert.Nil(t, reporter, "a sole instance has no leadership to lose mid-cycle, so it reports to nobody")
			running <- fnCtx
			<-fnCtx.Done()
		})
	}()

	var fnCtx context.Context
	select {
	case fnCtx = <-running:
	case <-time.After(time.Second):
		t.Fatal("fn never ran: a sole instance must win leadership on its first attempt, without waiting for anything")
	}
	assert.NoError(t, fnCtx.Err(), "fn must run on a live leadership context")

	cancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err, "Run must return nil once its context ends, like every other Elector")
	case <-time.After(time.Second):
		t.Fatal("Run never returned after its context was canceled")
	}
	assert.Error(t, fnCtx.Err(), "leadership must end when the caller's context does")
}

// TestSoleInstance_ReportsLeadershipInMetrics proves a deployment with
// nothing to elect still exports the leadership series - the point of
// routing it through an Elector at all, rather than calling the job
// directly. A fleet-wide "no leader for this lease" panel has to see these
// replicas too.
func TestSoleInstance_ReportsLeadershipInMetrics(t *testing.T) {
	el := NewSoleInstance(prometheus.NewRegistry())
	e, ok := el.(*elector)
	require.True(t, ok, "NewSoleInstance must build on the shared elector, which is what owns these metrics")

	ctx, cancel := context.WithCancel(context.Background())
	held := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = el.Run(ctx, "sole-metrics-test", func(fnCtx context.Context, _ CycleReporter) {
			close(held)
			<-fnCtx.Done()
		})
	}()
	<-held

	assert.Equal(t, float64(1), testutil.ToFloat64(e.m.isLeader.WithLabelValues("sole-metrics-test")),
		"a sole instance is the leader, and has to say so")
	assert.Equal(t, float64(1), testutil.ToFloat64(e.m.transitions.WithLabelValues("sole-metrics-test", "leader")))

	cancel()
	<-done
	assert.Equal(t, float64(0), testutil.ToFloat64(e.m.isLeader.WithLabelValues("sole-metrics-test")),
		"and has to stop saying so once it steps down")
}

// TestSoleInstance_ReacquiresWithoutBackoffUntilItsContextEnds proves
// leadership isn't a one-shot grant: fn returning early (a job loop that
// exits on its own) leaves Run free to hand leadership straight back,
// since there is never anything to contend with.
func TestSoleInstance_ReacquiresWithoutBackoffUntilItsContextEnds(t *testing.T) {
	el := NewSoleInstance(prometheus.NewRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runs := 0
	require.NoError(t, el.Run(ctx, "sole-reacquire-test", func(context.Context, CycleReporter) {
		runs++
		if runs == 3 {
			cancel()
		}
	}))
	assert.Equal(t, 3, runs, "each return must be followed by another term until the caller's context ends")
}
