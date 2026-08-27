package leaderelection

import (
	"context"
	"math/rand/v2"
	"time"
)

// PeriodicJob adapts a plain per-cycle function into the shape Elector.Run
// needs: a ticking loop that reports each cycle to whatever's coordinating
// leadership (see CycleReporter) and runs it on a context of its own. A job
// stays an ordinary func(context.Context), never learning that leadership,
// budgets or reporting exist.
//
// Each cycle gets a fresh context, canceled as soon as that cycle returns,
// so canceling one - from the reporter's side or by the outer ctx ending -
// reaches everything that cycle started and nothing else. The loop keeps
// ticking on interval (jittered up to 20%, so replicas started together
// don't tick in lockstep) for as long as the outer ctx is alive, however
// any individual cycle ended.
//
// interval must be positive - a job's own config validation is what
// guarantees that, and a non-positive one panics here rather than ticking
// unboundedly. budget is only ever as meaningful as the reporter receiving
// it: with none, nothing reads it.
func PeriodicJob(interval, budget time.Duration, cycle func(context.Context)) func(context.Context, CycleReporter) {
	return func(ctx context.Context, reporter CycleReporter) {
		jitterBase := interval / 5
		if jitterBase <= 0 {
			jitterBase = 1
		}
		ticker := time.NewTicker(interval + time.Duration(rand.Int64N(int64(jitterBase))))
		defer ticker.Stop()

		runCycle := func() {
			cycleCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if reporter != nil {
				reporter.CycleStarted(cycleCtx, cancel, budget)
			}
			cycle(cycleCtx)
		}

		runCycle()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runCycle()
			}
		}
	}
}
