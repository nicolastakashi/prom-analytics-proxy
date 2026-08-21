package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestElector_Run_HoldsLeadershipAcrossMultipleRenewalTicks proves the
// renewal watchdog keeps renewing repeatedly for as long as leadership is
// held — the steady-state behavior that actually matters in production,
// where a sync or retention job runs far longer than one renewInterval.
// TestLeaseStrategy_RenewExtendsExpiryWithoutBumpingFenceToken proves a
// single manual renewal call extends expiry; TestElector_Run_CancelsFnContextPromptlyWhenLeaseLost
// proves the watchdog reacts correctly to losing the lease on its very
// first tick. Neither proves the watchdog keeps ticking correctly beyond
// that first renewal.
//
// The lease's TTL is deliberately shorter than the total time this test
// waits, so a rival holder polling throughout that window can only ever
// fail to acquire if the original holder's watchdog keeps renewing on
// every tick — a watchdog that renewed once and then silently stopped
// would let the lease go stale partway through, and the rival's next poll
// would take it over.
func TestElector_Run_HoldsLeadershipAcrossMultipleRenewalTicks(t *testing.T) {
	db := newTestPostgresDB(t)
	const ttl = 80 * time.Millisecond
	const renewInterval = 20 * time.Millisecond
	const holdWindow = 400 * time.Millisecond // ~5x the TTL, ~20 renewal ticks

	strat := newLeaseStrategy(db, ttl, renewInterval)
	elector := newTestElector(strat, backoffConfig{initial: 10 * time.Millisecond})

	fnCtxCh := make(chan context.Context, 1)
	ctx, cancel := context.WithTimeout(context.Background(), holdWindow+time.Second)
	defer cancel()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		_ = elector.Run(ctx, "sustained-renewal-test", func(fnCtx context.Context) {
			fnCtxCh <- fnCtx
			<-fnCtx.Done()
		})
	}()

	var fnCtx context.Context
	select {
	case fnCtx = <-fnCtxCh:
	case <-time.After(time.Second):
		t.Fatal("fn was never invoked — lease was never acquired")
	}

	// noRenewal: this rival only probes: it must never itself become a
	// second, competing renewer.
	rival := newLeaseStrategy(db, ttl, noRenewal)
	deadline := time.Now().Add(holdWindow)
	for time.Now().Before(deadline) {
		select {
		case <-fnCtx.Done():
			t.Fatal("fn's context was canceled before the hold window elapsed — leadership was lost")
		default:
		}

		_, _, ok, err := rival.acquireOrHold(context.Background(), "sustained-renewal-test")
		assert.NoError(t, err)
		assert.False(t, ok, "the lease must still be held by the original holder throughout the hold window")

		time.Sleep(renewInterval / 2)
	}

	cancel()
	<-doneCh
}
