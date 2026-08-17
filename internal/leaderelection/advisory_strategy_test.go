package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAdvisoryStrategy_MutualExclusion_ConcurrentGoroutines is the
// concurrency requirement applied first at the low level: N goroutines
// hammer acquireOrHold against one real Postgres instance, and mutual
// exclusion must hold throughout.
//
// The invariant is checked *inside* the critical section under the same
// mutex that gates it, rather than via post-hoc interval-overlap math —
// simpler, race-free by construction, and fails immediately and loudly the
// instant two goroutines are ever concurrently "in". t.Fatal/t.FailNow is
// never called from inside a goroutine (unsafe in Go's testing package) —
// violations are only ever appended to a mutex-guarded slice, asserted on
// the test's own goroutine after wg.Wait().
//
// Two more mutex-guarded slices/counters extend that same discipline to the
// error path: db.SetMaxOpenConns bounds concurrent connections well under
// Postgres's default max_connections so pool exhaustion doesn't turn every
// acquireOrHold into an error, but errors are still recorded (asserted
// empty) and successful acquisitions counted (asserted past a floor) rather
// than silently discarded — a test that can pass with zero acquisitions
// ever entering the critical section proves nothing about mutual exclusion.
func TestAdvisoryStrategy_MutualExclusion_ConcurrentGoroutines(t *testing.T) {
	db := newTestPostgresDB(t)
	db.SetMaxOpenConns(30)
	strat := newAdvisoryLockStrategy(db)

	const n = 16
	const testDuration = 500 * time.Millisecond

	var mu sync.Mutex
	currentHolder := -1
	var violations []string
	var acquireErrs []error
	var acquisitions int64

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ctx.Err() == nil {
				_, release, ok, err := strat.acquireOrHold(ctx, "mutex-test")
				if err != nil {
					// ctx expiring mid-attempt right at the test's own
					// deadline is expected shutdown noise, not a failure of
					// acquireOrHold under contention.
					if !errors.Is(err, context.DeadlineExceeded) {
						mu.Lock()
						acquireErrs = append(acquireErrs, err)
						mu.Unlock()
					}
					continue
				}
				if !ok {
					continue
				}
				atomic.AddInt64(&acquisitions, 1)

				mu.Lock()
				if currentHolder != -1 {
					violations = append(violations, fmt.Sprintf("goroutine %d acquired while %d still holds", id, currentHolder))
				}
				currentHolder = id
				mu.Unlock()

				time.Sleep(5 * time.Millisecond) // widen the window a real bug would need to slip through

				mu.Lock()
				if currentHolder != id {
					violations = append(violations, fmt.Sprintf("goroutine %d found holder changed to %d before it released", id, currentHolder))
				}
				currentHolder = -1
				mu.Unlock()

				release()
			}
		}(i)
	}
	wg.Wait()

	assert.Empty(t, violations, "advisory lock must guarantee mutual exclusion")
	assert.Empty(t, acquireErrs, "acquireOrHold must not error under ordinary contention")
	assert.Greater(t, acquisitions, int64(10), "the critical section must actually have been entered repeatedly for this test to prove anything")
}
