package leaderelection

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestElector_Run_ConcurrentGoroutines groups the exported Elector API's
// concurrency guarantees — safety and liveness — as subtests sharing one
// Postgres container, each against its own lease name.
func TestElector_Run_ConcurrentGoroutines(t *testing.T) {
	db := newTestPostgresDB(t)

	// MutualExclusion proves the exported Elector API enforces exclusion
	// end-to-end, not just the raw acquireOrHold SQL (already covered by
	// TestAdvisoryStrategy_MutualExclusion_ConcurrentGoroutines, which
	// also documents why exclusion is checked inside the critical
	// section and why t.Fatal is never called from a goroutine here
	// either). N goroutines share one Elector and call Run concurrently
	// for the same lease name; fn bumps an atomic counter on entry and
	// decrements via defer on exit — the counter must never be observed
	// above 1.
	t.Run("MutualExclusion", func(t *testing.T) {
		elector := newTestElector(newAdvisoryLockStrategy(db), backoffConfig{initial: 5 * time.Millisecond, max: 20 * time.Millisecond})

		const n = 10
		var active atomic.Int32
		var violated atomic.Bool

		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()

		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = elector.Run(ctx, "full-stack-mutex-test", func(fnCtx context.Context) {
					if active.Add(1) > 1 {
						violated.Store(true)
					}
					defer active.Add(-1)
					select {
					case <-fnCtx.Done():
					case <-time.After(20 * time.Millisecond):
					}
				})
			}()
		}
		wg.Wait()

		assert.False(t, violated.Load(), "at most one Elector.Run's fn may be active at a time for the same lease name")
	})

	// HandsOffLeadershipManyTimes proves liveness, which the
	// mutual-exclusion subtest above deliberately doesn't: safety alone
	// ("never two holders at once") is satisfied just as well by a
	// system that grants leadership once and then gets stuck forever as
	// by one that hands off correctly — a version of Run that never
	// releases would still show zero conflicts in that subtest, since
	// nobody else would ever get a turn to conflict with. Here, N
	// goroutines contend for the same lease name and every entry into fn
	// is counted; a healthy elector must let many different attempts
	// through over the run, not just the first one.
	t.Run("HandsOffLeadershipManyTimes", func(t *testing.T) {
		elector := newTestElector(newAdvisoryLockStrategy(db), backoffConfig{initial: 5 * time.Millisecond, max: 20 * time.Millisecond})

		const n = 10
		var totalEntries atomic.Int32

		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()

		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = elector.Run(ctx, "handoff-liveness-test", func(fnCtx context.Context) {
					totalEntries.Add(1)
					select {
					case <-fnCtx.Done():
					case <-time.After(10 * time.Millisecond):
					}
				})
			}()
		}
		wg.Wait()

		// A healthy run of this length, with this many contenders and
		// this short a hold time, hands off far more than this in
		// practice — the low bound is chosen to comfortably separate
		// "leadership actually cycles" from "granted once, then stuck,"
		// not to pin an exact count.
		assert.Greater(t, int(totalEntries.Load()), 5, "leadership must be handed off repeatedly over the run, not granted once and then stuck")
	})
}
