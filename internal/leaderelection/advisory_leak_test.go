package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdvisoryStrategy_RepeatedContentionDoesNotLeakConnections tests the
// resource outcome that matters when acquireOrHold repeatedly loses the
// race for a lease already held by someone else — the not-leader branch,
// the single most-executed path in this whole package (every failed poll
// hits it). A connection checked out via db.Conn and never returned stays
// counted in db.Stats().OpenConnections forever; there is no pool timeout
// that reclaims it. Asserting that stat stays bounded after many failed
// attempts is a real, observable resource guarantee — not a spy on
// whether conn.Close() was called.
func TestAdvisoryStrategy_RepeatedContentionDoesNotLeakConnections(t *testing.T) {
	db := newTestPostgresDB(t)
	winner := newAdvisoryLockStrategy(db)
	loser := newAdvisoryLockStrategy(db)

	ctx := context.Background()
	_, release, ok, err := winner.acquireOrHold(ctx, "leak-contention-test")
	require.NoError(t, err) // release() below would nil-panic on failure to acquire
	require.True(t, ok)
	defer release()

	const attempts = 50
	for i := 0; i < attempts; i++ {
		_, _, ok, err := loser.acquireOrHold(ctx, "leak-contention-test")
		assert.NoError(t, err)
		assert.False(t, ok, "still held by winner")
	}

	assert.LessOrEqual(t, db.Stats().OpenConnections, 5,
		"repeatedly losing the race must keep OpenConnections flat, not grow with attempts")
}

// TestAdvisoryStrategy_RepeatedAcquireReleaseDoesNotLeakConnections is the
// same resource-outcome guarantee for the opposite path: winning, holding,
// and releasing. Every acquire/release cycle that leaked a connection
// would show up as OpenConnections growing roughly linearly with cycle
// count; a correct implementation keeps it flat regardless of how many
// cycles ran.
func TestAdvisoryStrategy_RepeatedAcquireReleaseDoesNotLeakConnections(t *testing.T) {
	db := newTestPostgresDB(t)
	strat := newAdvisoryLockStrategy(db)
	ctx := context.Background()

	const cycles = 50
	for i := 0; i < cycles; i++ {
		_, release, ok, err := strat.acquireOrHold(ctx, "leak-cycle-test")
		require.NoError(t, err) // release() below would nil-panic on failure to acquire
		require.True(t, ok)
		release()
	}

	assert.LessOrEqual(t, db.Stats().OpenConnections, 5,
		"repeated acquire/release cycles must keep OpenConnections flat, not grow with cycle count")
}
