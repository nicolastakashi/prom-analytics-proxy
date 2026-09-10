package leaderelection

import (
	"context"
	"time"
)

// CycleReporter lets a periodic job hand over one cycle - its context, its
// cancel func and its declared budget - to whatever holds leadership on its
// behalf, so a cycle running past that budget can be told to stop instead of
// running forever unnoticed (see docs/leader-election.md, "Reporting job
// cycles"). A nil CycleReporter is valid, and means the same as never
// reporting at all: a strategy with nothing to coordinate hands one over.
//
// cycleCtx ends the moment its cycle does, which is how an implementation
// tells a cycle still running from one that already returned - a job stuck
// mid-cycle from one merely idle between cycles.
type CycleReporter interface {
	CycleStarted(cycleCtx context.Context, cancel context.CancelFunc, budget time.Duration)
}
