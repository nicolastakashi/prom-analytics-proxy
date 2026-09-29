package api

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/oklog/run"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/leaderelection"
)

// periodicJob is the shape cmd/api needs from every job it schedules: one
// cycle per call, bounded by the context it's given, neither looping nor
// aware of who's calling it - see docs/jobs.md.
type periodicJob interface {
	RunOnce(ctx context.Context)
}

// addPeriodicJob wires job into g under leaseName, scheduling its cycles
// for as long as elector says this replica holds that lease. Every provider
// has an elector - a sole instance holds leadership unconditionally where
// there's nothing to coordinate - so leadership governs who runs cycles and
// never how often they run, and there is one path here rather than one per
// provider.
func addPeriodicJob(g *run.Group, elector leaderelection.Elector, leaseName string, interval time.Duration, job periodicJob) {
	ctx, cancel := context.WithCancel(context.Background())
	loop := func(loopCtx context.Context) { runPeriodically(loopCtx, interval, job.RunOnce) }

	g.Add(func() error {
		return elector.Run(ctx, leaseName, loop)
	}, func(err error) { cancel() })
}

// runPeriodically calls runOnce once immediately, then once per interval
// until ctx ends. It's the only scheduler any periodic job gets, so no job
// implements ticking of its own; interval is jittered up to 20% so
// replicas started together don't run their cycles in lockstep.
//
// interval must be positive; the jitter floor below only covers an
// interval too small for 20% of it to be a whole nanosecond, not a zero
// one.
func runPeriodically(ctx context.Context, interval time.Duration, runOnce func(context.Context)) {
	jitterBase := interval / 5
	if jitterBase <= 0 {
		jitterBase = 1
	}
	ticker := time.NewTicker(interval + time.Duration(rand.Int64N(int64(jitterBase))))
	defer ticker.Stop()

	runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce(ctx)
		}
	}
}
