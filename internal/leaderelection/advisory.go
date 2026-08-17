package leaderelection

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log/slog"
	"time"

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

// releaseUnlockTimeout bounds the pg_advisory_unlock call issued while
// releasing leadership. release() must run detached from the caller's ctx
// (see newHeldLease), but not unboundedly: release runs on the SIGTERM path,
// and an unbounded call there can block process shutdown on the OS TCP
// timeout.
const releaseUnlockTimeout = 5 * time.Second

// advisoryConn is the seam newHeldLease needs from the physical connection
// holding the lock — a dedicated interface (parallel to connGetter's
// rationale) so tests can inject deterministic unlock outcomes that would
// otherwise require forcing a real Postgres backend into that exact state.
// pqAdvisoryConn is the only production implementation, backed by a real
// *sql.Conn.
type advisoryConn interface {
	// unlock issues pg_advisory_unlock and reports whether it actually
	// released the lock — session-scoped locks make this a real possible
	// false, not just a defensive check.
	unlock(ctx context.Context, key int64) (unlocked bool, err error)
	// discard marks the connection unfit for pool reuse; the next
	// operation against it physically closes it instead of pooling it.
	discard()
	// close returns the connection to the pool for reuse.
	close() error
}

type pqAdvisoryConn struct{ c *sql.Conn }

func (p *pqAdvisoryConn) unlock(ctx context.Context, key int64) (bool, error) {
	var unlocked bool
	err := p.c.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked)
	return unlocked, err
}

func (p *pqAdvisoryConn) discard() { _ = p.c.Raw(func(any) error { return driver.ErrBadConn }) }

func (p *pqAdvisoryConn) close() error { return p.c.Close() }

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

	leaderCtx, release := newHeldLease(ctx, &pqAdvisoryConn{c: conn}, key, name)
	return leaderCtx, release, true, nil
}

// newHeldLease builds the leaderCtx/release pair for a lock already held on
// ac.
func newHeldLease(ctx context.Context, ac advisoryConn, key int64, name string) (context.Context, func()) {
	leaderCtx, cancel := context.WithCancel(ctx)

	release := func() {
		cancel()
		// Detached from ctx (release must still run even if ctx is already
		// canceled) but bounded by releaseUnlockTimeout, not unbounded —
		// see its doc comment.
		relCtx, relCancel := context.WithTimeout(context.Background(), releaseUnlockTimeout)
		defer relCancel()
		unlocked, err := ac.unlock(relCtx, key)
		if err != nil || !unlocked {
			// Session-scoped: a connection returned to the pool with the
			// unlock unresolved would stay leader-locked forever, reused by
			// ordinary queries. Discarding it forces Postgres to tear down
			// the session, which releases the lock as a side effect.
			slog.Warn("advisory unlock failed; discarding connection", "lease", name, "unlocked", unlocked, "err", err)
			ac.discard()
			return
		}
		_ = ac.close()
	}
	return leaderCtx, release
}
