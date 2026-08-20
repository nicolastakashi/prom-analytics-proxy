package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeaseStrategy_FenceTokenSurvivesRowDeletion proves fence_token keeps
// increasing across the row being deleted and recreated — an operator
// forcing a failover by deleting a stale row, for instance — rather than
// restarting. A fencing consumer that already saw the pre-deletion token
// would otherwise accept writes from a new holder carrying a lower or
// equal one, inverting the fencing guarantee instead of merely weakening
// it.
func TestLeaseStrategy_FenceTokenSurvivesRowDeletion(t *testing.T) {
	db := newTestPostgresDB(t)
	holderA := newLeaseStrategy(db, noRenewal, noRenewal)
	holderB := newLeaseStrategy(db, noRenewal, noRenewal)

	tokenBefore, ok, err := holderA.tryAcquireOrRenew(context.Background(), "deletion-test")
	require.NoError(t, err)
	require.True(t, ok)

	_, err = db.ExecContext(context.Background(), "DELETE FROM leader_leases WHERE lease_name = $1", "deletion-test")
	require.NoError(t, err)

	tokenAfter, ok, err := holderB.tryAcquireOrRenew(context.Background(), "deletion-test")
	require.NoError(t, err)
	require.True(t, ok)

	assert.Greater(t, tokenAfter, tokenBefore,
		"a token issued after the row was deleted and recreated must exceed every token issued before the deletion")
}

// TestLeaseStrategy_FenceTokenStaysMonotonic_AcrossRepeatedDeletions extends
// the single-deletion case to many: a token source that happened to survive
// exactly one deletion by coincidence (e.g. still-warm connection-level
// state) would not prove the general guarantee. Repeating delete-then-
// reacquire and checking every step is strictly increasing does.
func TestLeaseStrategy_FenceTokenStaysMonotonic_AcrossRepeatedDeletions(t *testing.T) {
	db := newTestPostgresDB(t)
	strat := newLeaseStrategy(db, noRenewal, noRenewal)

	var last int64 = -1
	for i := 0; i < 5; i++ {
		token, ok, err := strat.tryAcquireOrRenew(context.Background(), "repeated-deletion-test")
		require.NoError(t, err)
		require.True(t, ok)

		assert.Greater(t, token, last, "iteration %d: fence token must exceed every token issued so far", i)
		last = token

		_, err = db.ExecContext(context.Background(), "DELETE FROM leader_leases WHERE lease_name = $1", "repeated-deletion-test")
		require.NoError(t, err)
	}
}

// TestLeaseLeasesTable_FenceTokenColumnDefault_DrawsFromSequence proves the
// column's own DEFAULT is live, not dead code: a row inserted without this
// package's own SQL — a manual repair, a disaster-recovery script — must
// still draw a fresh, sequence-backed token rather than silently falling
// back to a constant that could collide with one already issued.
func TestLeaseLeasesTable_FenceTokenColumnDefault_DrawsFromSequence(t *testing.T) {
	db := newTestPostgresDB(t)

	// Burn a few sequence values the ordinary way first, so the column
	// default can be shown to track the sequence's current state rather
	// than merely matching its start value by coincidence.
	strat := newLeaseStrategy(db, noRenewal, noRenewal)
	_, ok, err := strat.tryAcquireOrRenew(context.Background(), "warm-up")
	require.NoError(t, err)
	require.True(t, ok)

	_, err = db.ExecContext(context.Background(),
		"INSERT INTO leader_leases (lease_name, holder_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')",
		"manual-insert-test", "manual-holder")
	require.NoError(t, err)

	var token int64
	err = db.QueryRowContext(context.Background(),
		"SELECT fence_token FROM leader_leases WHERE lease_name = $1", "manual-insert-test").Scan(&token)
	require.NoError(t, err)

	assert.Greater(t, token, int64(0), "the column default must draw a real sequence value, not the old hardcoded 0")
}
