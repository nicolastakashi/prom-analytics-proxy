package leaderelection

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLockKeyFor_DeterministicAndStable(t *testing.T) {
	assert.Equal(t, lockKeyFor("some-name"), lockKeyFor("some-name"), "same input must produce the same key across calls")
	assert.NotEqual(t, lockKeyFor("name-a"), lockKeyFor("name-b"), "different names must (in practice) produce different keys")
}

// TestLockKeyFor_PinsExistingProductionKeys locks in the exact int64 lock
// keys already in use in production today (0x6d657472696373 for inventory,
// 0x726574656e74696f for retention). These must NOT be derived from a hash
// of the name: during a rolling deploy, an old-code replica and a new-code
// replica would otherwise compute different keys for the same logical lease
// and briefly both believe they're the sole leader.
func TestLockKeyFor_PinsExistingProductionKeys(t *testing.T) {
	assert.Equal(t, int64(0x6d657472696373), lockKeyFor("metric-analytics-inventory"))
	assert.Equal(t, int64(0x726574656e74696f), lockKeyFor("metric-analytics-retention"))
}

func TestLockKeyFor_FallsBackToHashForUnknownNames(t *testing.T) {
	// Any other name must still be deterministic, just not one of the pinned
	// constants above.
	key := lockKeyFor("some-future-lease-name")
	assert.NotEqual(t, int64(0x6d657472696373), key)
	assert.NotEqual(t, int64(0x726574656e74696f), key)
	assert.Equal(t, key, lockKeyFor("some-future-lease-name"))
}
