package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestElector_Run_ReleasesLock_WhenFnPanics pins the guarantee that a panic
// in fn must not skip release(): release() physically unlocks over a
// connection that, at the moment of the panic, is still alive — it does not
// depend on Postgres ever detecting a dead session, unlike the process-death
// path (SIGKILL, OOM-kill, a host disappearing), which no amount of
// recover() can reach. Run is expected to still crash on an fn panic — this
// only guarantees the lock is released first, not that panics are
// swallowed.
func TestElector_Run_ReleasesLock_WhenFnPanics(t *testing.T) {
	strat := &releaseTrackingStrategy{}
	elector := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = elector.Run(ctx, "panic-test", func(context.Context, CycleReporter) {
			panic("simulated fn panic")
		})
		t.Fatal("Run must not return normally when fn panics")
	}()

	require.NotNil(t, recovered, "the panic must still propagate out of Run, not be swallowed")
	assert.True(t, strat.released.Load(), "release() must run before the panic propagates out of Run")
}
