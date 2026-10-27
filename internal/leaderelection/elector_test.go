package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestElector_Run_RunsFnWhileTheLeaseIsHeld pins that fn runs between
// acquisition and release, with a live leaderCtx: an fn started after
// release would run while another replica may hold the lease.
func TestElector_Run_RunsFnWhileTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	strat := &releaseTrackingStrategy{}
	elector := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var releasedAtEntry bool
	var leaderCtxErrAtEntry error
	_ = elector.Run(ctx, "held-test", func(fnCtx context.Context, _ CycleReporter) {
		releasedAtEntry = strat.released.Load()
		leaderCtxErrAtEntry = fnCtx.Err()
		cancel()
	})

	assert.False(t, releasedAtEntry, "fn must start before release")
	assert.NoError(t, leaderCtxErrAtEntry, "fn must start with a live leaderCtx")
	assert.True(t, strat.released.Load(), "release must run once fn returns")
}
