package leaderelection

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeaseStrategy_Lifecycle groups the core acquire/renew/release
// invariants as subtests sharing one Postgres container: each already
// operates on its own lease name, so nothing here needs isolation beyond
// that, and there's no reason to pay for a separate container per check.
func TestLeaseStrategy_Lifecycle(t *testing.T) {
	db := newTestPostgresDB(t)

	t.Run("AcquireWhenFree_Succeeds", func(t *testing.T) {
		strat := newLeaseStrategy(db, time.Second, noRenewal)

		acq, ok, err := strat.acquireOrHold(context.Background(), "lifecycle-test")

		release := acq.release
		require.NoError(t, err)
		require.True(t, ok)
		defer release()
	})

	// AcquireOrHold_ReturnsGenuineErrorUnchanged proves acquireOrHold's
	// error path (as opposed to the ordinary "held by someone else"
	// ok=false/err=nil case): a real failure out of tryAcquireOrRenew —
	// here, an already-canceled ctx, which QueryRowContext reports
	// directly rather than as sql.ErrNoRows — must come back as a
	// non-nil err with ctx and release both nil, not be mistaken for
	// contention.
	t.Run("AcquireOrHold_ReturnsGenuineErrorUnchanged", func(t *testing.T) {
		strat := newLeaseStrategy(db, time.Second, noRenewal)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		acq, ok, err := strat.acquireOrHold(ctx, "canceled-ctx-test")

		leaderCtx, release := acq.leaderCtx, acq.release
		assert.Error(t, err)
		assert.False(t, ok)
		assert.Nil(t, leaderCtx)
		assert.Nil(t, release)
	})

	t.Run("FailsWhenHeldByAnotherLiveHolder", func(t *testing.T) {
		holderA := newLeaseStrategy(db, time.Second, noRenewal)
		holderB := newLeaseStrategy(db, time.Second, noRenewal)

		acq, ok, err := holderA.acquireOrHold(context.Background(), "contended-test")

		release := acq.release
		require.NoError(t, err)
		require.True(t, ok)
		defer release()

		_, ok, err = holderB.acquireOrHold(context.Background(), "contended-test")
		assert.NoError(t, err)
		assert.False(t, ok, "a second holder must not acquire a lease still held by someone else")
	})

	t.Run("SucceedsAfterExpiry", func(t *testing.T) {
		holderA := newLeaseStrategy(db, 50*time.Millisecond, noRenewal)
		holderB := newLeaseStrategy(db, time.Second, noRenewal)

		_, ok, err := holderA.acquireOrHold(context.Background(), "expiry-test")
		assert.NoError(t, err)
		assert.True(t, ok)
		// holderA never renews again, and noRenewal keeps its watchdog
		// dormant for the life of this test — simulating it having
		// disappeared.

		time.Sleep(100 * time.Millisecond)

		acq, ok, err := holderB.acquireOrHold(context.Background(), "expiry-test")

		release := acq.release
		require.NoError(t, err)
		require.True(t, ok, "a lease must become acquirable again once it has expired")
		defer release()
	})

	t.Run("RenewExtendsExpiryWithoutBumpingFenceToken", func(t *testing.T) {
		holderA := newLeaseStrategy(db, time.Second, noRenewal)

		ctx := context.Background()
		acq, ok, err := holderA.acquireOrHold(ctx, "renew-test")
		release := acq.release
		require.NoError(t, err)
		require.True(t, ok)
		defer release()

		tokenAfterAcquire, expiresAfterAcquire := queryLease(t, db, "renew-test")

		time.Sleep(10 * time.Millisecond)
		acq2, ok, err := holderA.acquireOrHold(ctx, "renew-test")
		release2 := acq2.release
		require.NoError(t, err)
		require.True(t, ok)
		defer release2()

		tokenAfterRenew, expiresAfterRenew := queryLease(t, db, "renew-test")

		assert.Equal(t, tokenAfterAcquire, tokenAfterRenew, "the same holder renewing must not bump the fence token")
		assert.True(t, expiresAfterRenew.After(expiresAfterAcquire), "renewing must extend expires_at")
	})

	// ReleaseAllowsImmediateReacquisitionByAnotherHolder pins graceful
	// handoff: a holder that steps down via release() must let another
	// replica take over immediately, not force it to wait out the full
	// TTL as an ungraceful death would (see
	// TestLeaseStrategy_FailoverAfterUngracefulDeath for that other
	// case). The TTL here is deliberately long — holder B succeeding can
	// only be explained by release() itself, not by expiry.
	t.Run("ReleaseAllowsImmediateReacquisitionByAnotherHolder", func(t *testing.T) {
		holderA := newLeaseStrategy(db, time.Minute, noRenewal)
		holderB := newLeaseStrategy(db, time.Minute, noRenewal)

		acq, ok, err := holderA.acquireOrHold(context.Background(), "handoff-test")

		release := acq.release
		require.NoError(t, err)
		require.True(t, ok)

		release()

		_, ok, err = holderB.acquireOrHold(context.Background(), "handoff-test")
		assert.NoError(t, err)
		assert.True(t, ok, "a graceful release must let another holder acquire immediately, without waiting out the TTL")
	})
}

// TestLeaseStrategy_Release_LogsWarningWhenExpiryUpdateFails proves
// release()'s failure path: if the proactive-expiry UPDATE itself errors —
// here, because the DB was closed out from under it — release must not
// panic or hang (relCtx is detached from the caller's ctx specifically so
// release still runs), and must log the failure rather than drop it
// silently, since the only consequence at that point is the next holder
// waiting out the full TTL instead of taking over immediately.
//
// Kept out of TestLeaseStrategy_Lifecycle's shared container: this test
// closes the DB itself, which would take every other subtest down with it.
func TestLeaseStrategy_Release_LogsWarningWhenExpiryUpdateFails(t *testing.T) {
	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	db := newTestPostgresDB(t)
	strat := newLeaseStrategy(db, time.Second, noRenewal)

	acq, ok, err := strat.acquireOrHold(context.Background(), "release-error-test")

	release := acq.release
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, db.Close())

	release() // must return promptly, not hang or panic despite the DB being gone

	var found bool
	for _, r := range handler.snapshot() {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, "releasing lease failed") {
			found = true
		}
	}
	assert.True(t, found, "a failed proactive-expiry update during release must be logged, not dropped silently")
}

// queryLease is a small test helper for peeking at leader_leases state
// directly — matches the house style already used for the advisory-lock
// strategy's pg_locks assertions.
func queryLease(t *testing.T, db *sql.DB, name string) (fenceToken int64, expiresAt time.Time) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		"SELECT fence_token, expires_at FROM leader_leases WHERE lease_name = $1", name,
	).Scan(&fenceToken, &expiresAt)
	assert.NoError(t, err)
	return fenceToken, expiresAt
}
