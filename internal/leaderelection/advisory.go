package leaderelection

import (
	"context"
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// connGetter is the minimal seam acquireOrHold needs from *sql.DB — a
// dedicated interface (rather than depending on *sql.DB directly) purely so
// tests can inject exactly one transient connection failure
// deterministically without needing to break a real Postgres connection.
// *sql.DB already satisfies this trivially via its own Conn method.
type connGetter interface {
	Conn(ctx context.Context) (*sql.Conn, error)
}

// NewAdvisoryElector returns an Elector backed by PostgreSQL session-level
// advisory locks (pg_try_advisory_lock). db may be nil only if the caller
// never invokes Run with a context that isn't already canceled. reg may be
// nil, in which case metrics register against prometheus.DefaultRegisterer,
// matching the convention used by inventory.NewSyncer/retention.NewWorker.
func NewAdvisoryElector(db *sql.DB, reg prometheus.Registerer) Elector {
	return newElector(newAdvisoryLockStrategy(db), backoffConfig{}, reg)
}

// advisoryLockStrategy implements strategy using PostgreSQL session-level
// advisory locks. A session-level lock is tied to the physical backend
// session behind a *sql.Conn, not to the Go-level handle — which is exactly
// why release() below explicitly unlocks rather than relying on conn.Close()
// alone (see docs/leader-election.md).
type advisoryLockStrategy struct {
	db connGetter
}

func newAdvisoryLockStrategy(db connGetter) *advisoryLockStrategy {
	return &advisoryLockStrategy{db: db}
}

func (s *advisoryLockStrategy) acquireOrHold(ctx context.Context, name string) (context.Context, func(), bool, error) {
	key := lockKeyFor(name)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, false, err
	}

	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, nil, false, err
	}
	if !got {
		_ = conn.Close()
		return nil, nil, false, nil
	}

	leaderCtx, cancel := context.WithCancel(ctx)
	release := func() {
		cancel()
		// Explicit unlock: the lock is scoped to this physical
		// connection, not to conn.Close()'s pool-return semantics, so
		// release must say so explicitly. context.Background() is
		// deliberate too — this is unconditional cleanup, not
		// cancelable work, and must still run even if ctx is already
		// canceled.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	}
	return leaderCtx, release, true, nil
}
