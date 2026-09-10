package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLeaseStrategy_FailoverAfterUngracefulDeath pins the lease strategy's
// central advantage over the advisory-lock strategy: a holder that stops
// renewing without ever releasing (an ungraceful death — the exact scenario
// a session-level lock leaves permanently stuck, since nothing physically
// closes the connection that held it) must not block leadership forever
// here. A second holder must fail immediately (well within the TTL) and
// succeed shortly after it elapses.
//
// assert.Eventually polls rather than sleeping a fixed duration close to
// the TTL: it succeeds as soon as possible after real expiry (typically
// just over ttl) while tolerating scheduler jitter up to the bound before
// failing — a fixed sleep near the TTL boundary would be exactly the kind
// of test that flakes under CI load.
func TestLeaseStrategy_FailoverAfterUngracefulDeath(t *testing.T) {
	db := newTestPostgresDB(t)
	const ttl = 150 * time.Millisecond

	// noRenewal on both: the whole point is to simulate holder A stopping
	// renewal entirely — a live watchdog would keep it alive regardless of
	// whether the test calls acquireOrHold again.
	holderA := newLeaseStrategy(db, ttl, noRenewal)
	holderB := newLeaseStrategy(db, ttl, noRenewal)

	ctx := context.Background()
	_, ok, err := holderA.acquireOrHold(ctx, "failover-test")
	assert.NoError(t, err)
	assert.True(t, ok)
	// holderA "dies" here: no further acquireOrHold or release calls.

	_, ok, err = holderB.acquireOrHold(ctx, "failover-test")
	assert.NoError(t, err)
	assert.False(t, ok, "the lease must not transfer before the TTL elapses")

	assert.Eventually(t, func() bool {
		_, ok, err := holderB.acquireOrHold(ctx, "failover-test")
		return err == nil && ok
	}, ttl*4, 10*time.Millisecond, "holder B should acquire the lease shortly after holder A's TTL expires")
}
