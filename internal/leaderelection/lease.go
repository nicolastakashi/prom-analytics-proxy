package leaderelection

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// acquireOrRenewLeaseSQL atomically acquires a free or expired lease, or
// renews it if the caller is already the current holder — one round trip,
// no separate read-then-write. When the WHERE clause is false (the lease is
// both live and held by someone else), the INSERT ... ON CONFLICT DO UPDATE
// affects no row, so RETURNING yields sql.ErrNoRows: that's the "not
// acquired" signal. now() and make_interval() run server-side so every
// comparison and every writer agree on the same clock.
const acquireOrRenewLeaseSQL = `
INSERT INTO leader_leases (lease_name, holder_id, fence_token, expires_at)
VALUES ($1, $2, 1, now() + make_interval(secs => $3))
ON CONFLICT (lease_name) DO UPDATE SET
    holder_id   = EXCLUDED.holder_id,
    fence_token = CASE WHEN leader_leases.holder_id = EXCLUDED.holder_id
                        THEN leader_leases.fence_token
                        ELSE leader_leases.fence_token + 1
                   END,
    expires_at  = EXCLUDED.expires_at
WHERE leader_leases.expires_at < now()
   OR leader_leases.holder_id  = EXCLUDED.holder_id
RETURNING fence_token
`

// releaseLeaseSQL proactively expires a lease this holder still owns, so a
// graceful step-down (release() called while still leader) hands off to
// another replica immediately instead of making it wait out the full TTL,
// as an ungraceful death would. The holder_id match makes this a no-op if
// leadership was already lost and reclaimed by someone else.
const releaseLeaseSQL = `
UPDATE leader_leases SET expires_at = to_timestamp(0)
WHERE lease_name = $1 AND holder_id = $2
`

// releaseTimeout bounds the proactive-expiry call issued while releasing
// leadership. release() must run detached from the caller's ctx (the
// caller may already be shutting down), but not unboundedly — mirrors
// advisory-lock's releaseUnlockTimeout for the same reason: release runs
// on the SIGTERM path, and an unbounded call there can block process
// shutdown on the OS TCP timeout.
const releaseTimeout = 5 * time.Second

// leaseStrategy implements strategy using a lease row with a TTL, instead
// of a session-level advisory lock. A lease's validity is a plain
// timestamp comparison, decoupled from any connection's physical
// lifecycle — see docs/leader-election.md for how this compares to the
// advisory-lock strategy.
type leaseStrategy struct {
	db            *sql.DB
	ttl           time.Duration
	renewInterval time.Duration
	holderID      string
}

// newLeaseStrategy assumes the leader_leases table already exists —
// created by internal/db's own migrations (0015_leader_leases.sql), which
// always run before this is ever reachable (leaderelection.New is only
// called from inside cmd/api's dbProvider.WithDB, i.e. after the database
// provider, migrations included, has already been constructed).
func newLeaseStrategy(db *sql.DB, ttl, renewInterval time.Duration) *leaseStrategy {
	return &leaseStrategy{db: db, ttl: ttl, renewInterval: renewInterval, holderID: uuid.NewString()}
}

func (s *leaseStrategy) acquireOrHold(ctx context.Context, name string) (context.Context, func(), bool, error) {
	_, ok, err := s.tryAcquireOrRenew(ctx, name)
	if err != nil {
		return nil, nil, false, err
	}
	if !ok {
		return nil, nil, false, nil
	}

	// Leadership acquired. A background watchdog renews on renewInterval
	// for as long as leaderCtx is alive; the moment a renewal attempt
	// stops succeeding for this holder — lost to someone else, or a DB
	// error — it cancels leaderCtx immediately, rather than waiting for
	// fn to return on its own. This is what makes a lost lease actually
	// stop in-flight work, closing the gap a one-time leadership check
	// (checked once, then trusted for the rest of a run) would leave.
	leaderCtx, cancel := context.WithCancel(ctx)
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(s.renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaderCtx.Done():
				return
			case <-ticker.C:
				_, stillOK, err := s.tryAcquireOrRenew(leaderCtx, name)
				if err != nil || !stillOK {
					cancel()
					return
				}
			}
		}
	}()

	release := func() {
		cancel()
		<-watchdogDone // avoid leaking the watchdog goroutine past release

		// Proactively expire the lease so a graceful step-down hands off
		// immediately instead of making the next holder wait out the
		// full TTL. Detached from ctx (release must still run even if
		// ctx is already canceled) but bounded — see releaseTimeout.
		relCtx, relCancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer relCancel()
		if _, err := s.db.ExecContext(relCtx, releaseLeaseSQL, name, s.holderID); err != nil {
			slog.Warn("releasing lease failed; next holder will wait out the TTL", "lease", name, "err", err)
		}
	}
	return leaderCtx, release, true, nil
}

// tryAcquireOrRenew is the one-round-trip SQL call shared by the initial
// acquire and every subsequent renewal attempt. The returned fence token is
// only meaningful when ok is true.
func (s *leaseStrategy) tryAcquireOrRenew(ctx context.Context, name string) (fenceToken int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, acquireOrRenewLeaseSQL, name, s.holderID, s.ttl.Seconds()).Scan(&fenceToken)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return fenceToken, true, nil
}
