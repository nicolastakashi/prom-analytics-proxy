package leaderelection

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseTrackingStrategy always grants leadership immediately and records
// whether release() has run — enough seam to observe release ordering
// relative to a panic in fn without needing a real Postgres connection.
type releaseTrackingStrategy struct {
	released atomic.Bool
}

func (s *releaseTrackingStrategy) acquireOrHold(ctx context.Context, _ string) (context.Context, func(), bool, error) {
	leaderCtx, cancel := context.WithCancel(ctx)
	release := func() {
		cancel()
		s.released.Store(true)
	}
	return leaderCtx, release, true, nil
}

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
		_ = elector.Run(ctx, "panic-test", func(context.Context) {
			panic("simulated fn panic")
		})
		t.Fatal("Run must not return normally when fn panics")
	}()

	require.NotNil(t, recovered, "the panic must still propagate out of Run, not be swallowed")
	assert.True(t, strat.released.Load(), "release() must run before the panic propagates out of Run")
}
