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

func TestLeaseStrategy_AcquireWhenFree_Succeeds(t *testing.T) {
	db := newTestPostgresDB(t)
	strat := newLeaseStrategy(db, time.Second, noRenewal)

	_, release, ok, err := strat.acquireOrHold(context.Background(), "lifecycle-test")
	require.NoError(t, err)
	require.True(t, ok)
	defer release()
}

// TestLeaseStrategy_AcquireOrHold_ReturnsGenuineErrorUnchanged proves
// acquireOrHold's error path (as opposed to the ordinary "held by someone
// else" ok=false/err=nil case): a real failure out of tryAcquireOrRenew —
// here, an already-canceled ctx, which QueryRowContext reports directly
// rather than as sql.ErrNoRows — must come back as a non-nil err with ctx
// and release both nil, not be mistaken for contention.
func TestLeaseStrategy_AcquireOrHold_ReturnsGenuineErrorUnchanged(t *testing.T) {
	db := newTestPostgresDB(t)
	strat := newLeaseStrategy(db, time.Second, noRenewal)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	leaderCtx, release, ok, err := strat.acquireOrHold(ctx, "canceled-ctx-test")
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Nil(t, leaderCtx)
	assert.Nil(t, release)
}

func TestLeaseStrategy_FailsWhenHeldByAnotherLiveHolder(t *testing.T) {
	db := newTestPostgresDB(t)
	holderA := newLeaseStrategy(db, time.Second, noRenewal)
	holderB := newLeaseStrategy(db, time.Second, noRenewal)

	_, release, ok, err := holderA.acquireOrHold(context.Background(), "contended-test")
	require.NoError(t, err)
	require.True(t, ok)
	defer release()

	_, _, ok, err = holderB.acquireOrHold(context.Background(), "contended-test")
	assert.NoError(t, err)
	assert.False(t, ok, "a second holder must not acquire a lease still held by someone else")
}

func TestLeaseStrategy_SucceedsAfterExpiry(t *testing.T) {
	db := newTestPostgresDB(t)
	holderA := newLeaseStrategy(db, 50*time.Millisecond, noRenewal)
	holderB := newLeaseStrategy(db, time.Second, noRenewal)

	_, _, ok, err := holderA.acquireOrHold(context.Background(), "expiry-test")
	assert.NoError(t, err)
	assert.True(t, ok)
	// holderA never renews again, and noRenewal keeps its watchdog dormant
	// for the life of this test — simulating it having disappeared.

	time.Sleep(100 * time.Millisecond)

	_, release, ok, err := holderB.acquireOrHold(context.Background(), "expiry-test")
	require.NoError(t, err)
	require.True(t, ok, "a lease must become acquirable again once it has expired")
	defer release()
}

func TestLeaseStrategy_RenewExtendsExpiryWithoutBumpingFenceToken(t *testing.T) {
	db := newTestPostgresDB(t)
	holderA := newLeaseStrategy(db, time.Second, noRenewal)

	ctx := context.Background()
	_, release, ok, err := holderA.acquireOrHold(ctx, "renew-test")
	require.NoError(t, err)
	require.True(t, ok)
	defer release()

	tokenAfterAcquire, expiresAfterAcquire := queryLease(t, db, "renew-test")

	time.Sleep(10 * time.Millisecond)
	_, release2, ok, err := holderA.acquireOrHold(ctx, "renew-test")
	require.NoError(t, err)
	require.True(t, ok)
	defer release2()

	tokenAfterRenew, expiresAfterRenew := queryLease(t, db, "renew-test")

	assert.Equal(t, tokenAfterAcquire, tokenAfterRenew, "the same holder renewing must not bump the fence token")
	assert.True(t, expiresAfterRenew.After(expiresAfterAcquire), "renewing must extend expires_at")
}

// TestLeaseStrategy_ReleaseAllowsImmediateReacquisitionByAnotherHolder
// pins graceful handoff: a holder that steps down via release() must let
// another replica take over immediately, not force it to wait out the
// full TTL as an ungraceful death would (see TestLeaseStrategy_
// FailoverAfterUngracefulDeath for that other case). The TTL here is
// deliberately long — holder B succeeding can only be explained by
// release() itself, not by expiry.
func TestLeaseStrategy_ReleaseAllowsImmediateReacquisitionByAnotherHolder(t *testing.T) {
	db := newTestPostgresDB(t)
	holderA := newLeaseStrategy(db, time.Minute, noRenewal)
	holderB := newLeaseStrategy(db, time.Minute, noRenewal)

	_, release, ok, err := holderA.acquireOrHold(context.Background(), "handoff-test")
	require.NoError(t, err)
	require.True(t, ok)

	release()

	_, _, ok, err = holderB.acquireOrHold(context.Background(), "handoff-test")
	assert.NoError(t, err)
	assert.True(t, ok, "a graceful release must let another holder acquire immediately, without waiting out the TTL")
}

// TestLeaseStrategy_Release_LogsWarningWhenExpiryUpdateFails proves
// release()'s failure path: if the proactive-expiry UPDATE itself errors —
// here, because the DB was closed out from under it — release must not
// panic or hang (relCtx is detached from the caller's ctx specifically so
// release still runs), and must log the failure rather than drop it
// silently, since the only consequence at that point is the next holder
// waiting out the full TTL instead of taking over immediately.
func TestLeaseStrategy_Release_LogsWarningWhenExpiryUpdateFails(t *testing.T) {
	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	db := newTestPostgresDB(t)
	strat := newLeaseStrategy(db, time.Second, noRenewal)

	_, release, ok, err := strat.acquireOrHold(context.Background(), "release-error-test")
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
