package leaderelection

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLeaseStrategy_ConcurrentGoroutines_FenceTokensNeverReusedAcrossHolders
// is the concurrency requirement for the lease strategy, and uses a
// different invariant than the advisory-lock strategy's mutual-exclusion
// test: fence-token uniqueness, rather than checking a "who's currently
// in" tracker.
//
// This is the right primary invariant for a lease specifically because
// it's exactly the mechanism a real downstream consumer would use to
// reject stale-leader writes (the fencing-token pattern) — proving it
// directly proves the guarantee this design promises, not just an
// incidental timing property of the test. A same-holder renewal correctly
// keeps returning the same token (see
// TestLeaseStrategy_RenewExtendsExpiryWithoutBumpingFenceToken); only a
// token ever claimed by two *different* holder IDs is a violation.
//
// Calls tryAcquireOrRenew directly rather than acquireOrHold: this test
// only cares about the raw acquire/token semantics, not the watchdog or
// release machinery, so there's no lease to release between attempts.
//
// A winner backs off well past the TTL before its next attempt, instead
// of retrying immediately: acquireOrRenewLeaseSQL's same-holder branch
// lets whoever currently holds the lease renew unconditionally, so a
// goroutine that retries right away wins every time and a genuine
// cross-holder handover — the case this test exists to catch a violation
// in — would otherwise almost never occur. The final assertion that more
// than one distinct holder actually won is what makes that failure mode
// visible instead of silently passing vacuously.
func TestLeaseStrategy_ConcurrentGoroutines_FenceTokensNeverReusedAcrossHolders(t *testing.T) {
	db := newTestPostgresDB(t)

	const n = 12
	const ttl = 40 * time.Millisecond
	var mu sync.Mutex
	tokenOwner := map[int64]string{}
	holdersSeen := map[string]bool{}
	var violations []string

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		strat := newLeaseStrategy(db, ttl, noRenewal)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				token, ok, err := strat.tryAcquireOrRenew(ctx, "fence-test")
				if err != nil || !ok {
					time.Sleep(5 * time.Millisecond) // poll fast: maximize contention once the lease expires
					continue
				}

				mu.Lock()
				if owner, seen := tokenOwner[token]; seen && owner != strat.holderID {
					violations = append(violations, fmt.Sprintf("fence token %d reused: first held by %s, now %s", token, owner, strat.holderID))
				}
				tokenOwner[token] = strat.holderID
				holdersSeen[strat.holderID] = true
				mu.Unlock()

				time.Sleep(ttl + ttl/2) // step back past the TTL so the other n-1 goroutines get a real window to take over
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, violations, "a fence token must never be claimed by two different holders")
	assert.Greater(t, len(holdersSeen), 1, "test setup must actually exercise a cross-holder handover, or the invariant above is unproven")
}
