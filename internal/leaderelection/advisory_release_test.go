package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAdvisoryStrategy_ReleasesLockExplicitly_NotJustViaConnClose pins the
// invariant that release() explicitly issues pg_advisory_unlock rather than
// relying on conn.Close() alone. conn.Close() returns the connection to the
// pool but does not guarantee the underlying Postgres backend session is
// torn down — session-level advisory locks are scoped to that session, not
// the Go handle — so a release path that skips the explicit unlock would
// leave the lock held indefinitely. This test proves the unlock actually
// happens, by querying pg_locks through a *second* connection from the
// *same still-open* *sql.DB — a release relying only on conn.Close() would
// still show the lock held at this point.
func TestAdvisoryStrategy_ReleasesLockExplicitly_NotJustViaConnClose(t *testing.T) {
	db := newTestPostgresDB(t)
	strat := newAdvisoryLockStrategy(db)

	ctx := context.Background()
	_, release, ok, err := strat.acquireOrHold(ctx, "release-test")
	assert.NoError(t, err)
	assert.True(t, ok)

	release()

	var count int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "advisory lock must be released via pg_advisory_unlock, not left to connection-pool GC")
}
