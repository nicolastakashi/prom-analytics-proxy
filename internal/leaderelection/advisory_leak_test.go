package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdvisoryStrategy_RepeatedOperations_DoNotLeakConnections groups the
// resource-outcome guarantees for acquireOrHold's two repeatable paths as
// subtests sharing one Postgres container — each uses its own lease name,
// so there's no reason to pay for a separate container per check. A
// connection checked out via db.Conn and never returned stays counted in
// db.Stats().OpenConnections forever; there is no pool timeout that
// reclaims it. Asserting that stat stays bounded after many attempts is a
// real, observable resource guarantee — not a spy on whether conn.Close()
// was called.
func TestAdvisoryStrategy_RepeatedOperations_DoNotLeakConnections(t *testing.T) {
	db := newTestPostgresDB(t)

	// Contention exercises the not-leader branch — the single
	// most-executed path in this whole package, since every failed poll
	// hits it.
	t.Run("Contention", func(t *testing.T) {
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
	})

	// AcquireReleaseCycle is the same resource-outcome guarantee for the
	// opposite path: winning, holding, and releasing. Every
	// acquire/release cycle that leaked a connection would show up as
	// OpenConnections growing roughly linearly with cycle count; a
	// correct implementation keeps it flat regardless of how many cycles
	// ran.
	t.Run("AcquireReleaseCycle", func(t *testing.T) {
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
	})
}
