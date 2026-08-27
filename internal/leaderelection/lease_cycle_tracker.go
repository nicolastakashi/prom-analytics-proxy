package leaderelection

import (
	"context"
	"sync"
	"time"
)

// cycleTracker is the lease strategy's CycleReporter: one instance per
// acquisition, created in acquireOrHold and read by that same acquisition's
// watchdog on every tick — never shared across concurrent acquisitions of
// different lease names, since each Run call creates its own.
type cycleTracker struct {
	mu       sync.Mutex
	cycleCtx context.Context
	cancel   context.CancelFunc
	deadline time.Time
}

func (t *cycleTracker) CycleStarted(cycleCtx context.Context, cancel context.CancelFunc, budget time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cycleCtx = cycleCtx
	t.cancel = cancel
	t.deadline = time.Now().Add(budget)
}

// overBudget reports whether the cycle it was last told about is still
// running and has already passed its declared budget, and the cancel to
// call if so. over is false when no cycle has ever been reported, and false
// once a reported cycle has ended: a job whose interval is longer than its
// budget is idle past that budget on every single cycle, and an idle job
// that finished its last cycle in time has done nothing wrong.
func (t *cycleTracker) overBudget() (cancel context.CancelFunc, over bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cycleCtx == nil || t.cycleCtx.Err() != nil {
		return nil, false
	}
	return t.cancel, time.Now().After(t.deadline)
}
