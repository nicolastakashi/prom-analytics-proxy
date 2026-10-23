package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdvisoryStrategy_DeniesWhileHeld_GrantsAfterRelease pins mutual
// exclusion between sessions: while one holds the advisory lock every
// other is denied, and release makes it grantable again.
func TestAdvisoryStrategy_DeniesWhileHeld_GrantsAfterRelease(t *testing.T) {
	t.Parallel()
	db := newTestPostgresDB(t)
	holder := newAdvisoryLockStrategy(db)
	contender := newAdvisoryLockStrategy(db)
	ctx := context.Background()

	acq, ok, err := holder.acquireOrHold(ctx, "exclusion-test")
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = contender.acquireOrHold(ctx, "exclusion-test")
	require.NoError(t, err)
	assert.False(t, ok, "a second session must be denied while the advisory lock is held")

	acq.release()

	acq, ok, err = contender.acquireOrHold(ctx, "exclusion-test")
	require.NoError(t, err)
	require.True(t, ok, "a released advisory lock must be grantable to another session")
	acq.release()
}
