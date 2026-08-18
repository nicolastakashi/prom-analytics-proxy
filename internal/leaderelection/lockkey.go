package leaderelection

import "github.com/cespare/xxhash/v2"

// pinnedLockKeys are the exact advisory-lock keys already in use in
// production, kept stable rather than derived from a hash of the name. See
// TestLockKeyFor_PinsExistingProductionKeys for why this matters during a
// rolling deploy.
var pinnedLockKeys = map[string]int64{
	"metric-analytics-inventory": 0x6d657472696373,
	"metric-analytics-retention": 0x726574656e74696f,
}

// lockKeyFor derives the int64 advisory-lock key for a lease name. Known
// production names resolve to their pinned constants; any other name falls
// back to a deterministic hash so new callers don't need a code change here.
func lockKeyFor(name string) int64 {
	if key, ok := pinnedLockKeys[name]; ok {
		return key
	}
	return int64(xxhash.Sum64String(name))
}
