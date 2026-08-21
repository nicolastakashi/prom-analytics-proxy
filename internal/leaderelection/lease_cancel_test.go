package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestElector_Run_CancelsFnContextPromptlyWhenLeaseLost proves the
// renewal watchdog actually stops in-flight work once a renewal stops
// succeeding, rather than only checking leadership once at the start of a
// run — insufficient for a lease that can be lost mid-run.
//
// The lease loss is forced directly via SQL — stealing the row as a
// different holder — rather than waiting for a real TTL expiry, keeping
// the test fast and deterministic instead of timing-dependent.
func TestElector_Run_CancelsFnContextPromptlyWhenLeaseLost(t *testing.T) {
	db := newTestPostgresDB(t)
	const renewInterval = 20 * time.Millisecond

	strat := newLeaseStrategy(db, 80*time.Millisecond, renewInterval)
	elector := newTestElector(strat, backoffConfig{initial: 10 * time.Millisecond})

	fnCtxCh := make(chan context.Context, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		_ = elector.Run(ctx, "cancel-test", func(fnCtx context.Context) {
			fnCtxCh <- fnCtx
			<-fnCtx.Done() // fn must exit once fnCtx is canceled
		})
	}()

	var fnCtx context.Context
	select {
	case fnCtx = <-fnCtxCh:
	case <-time.After(time.Second):
		t.Fatal("fn was never invoked — lease was never acquired")
	}

	// Force lease loss: steal the row as a different holder, past expiry,
	// simulating this holder's next renewal losing the race.
	_, err := db.ExecContext(context.Background(),
		"UPDATE leader_leases SET holder_id = 'intruder', fence_token = fence_token + 1, expires_at = now() + interval '1 hour' WHERE lease_name = $1",
		"cancel-test")
	assert.NoError(t, err)

	select {
	case <-fnCtx.Done():
		// success: the watchdog noticed and canceled promptly.
	case <-time.After(renewInterval * 10):
		t.Fatal("fn's context was not canceled promptly after the lease was lost")
	}

	cancel()
	<-doneCh
}
