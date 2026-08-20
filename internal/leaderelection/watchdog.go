package leaderelection

import (
	"context"
	"time"
)

// runCancelWatchdog is the ticker-driven background-goroutine skeleton
// shared by both strategies' leaderCtx-cancellation guarantee (advisory's
// connection-liveness ping, lease's renewal): call check every interval
// for as long as leaderCtx is alive, and cancel it the instant check
// reports false. Each strategy supplies its own check — what a failure
// means, and whether/how it's logged, differs between them — this only
// owns the ticking, the exit conditions, and leak-safety.
//
// The returned channel is closed when the goroutine exits, so a release()
// built on this can join on it before touching the connection/resource
// the goroutine was checking — release() must never race the last
// in-flight check against its own cleanup.
func runCancelWatchdog(leaderCtx context.Context, cancel context.CancelFunc, interval time.Duration, check func(ctx context.Context) bool) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-leaderCtx.Done():
				return
			case <-ticker.C:
				if !check(leaderCtx) {
					cancel()
					return
				}
			}
		}
	}()
	return done
}
