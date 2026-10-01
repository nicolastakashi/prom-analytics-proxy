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
//
// fence_token draws from leader_lease_fence_token_seq rather than being
// incremented in place — see docs/leader-election.md for why a per-row
// counter can't give the same monotonicity guarantee. Every execution,
// including a same-holder renewal, burns one sequence value via
// nextval() in the VALUES clause: Postgres evaluates it to build EXCLUDED
// even when the ON CONFLICT branch is what actually runs, and the CASE
// below discards it whenever the token itself doesn't change.
const acquireOrRenewLeaseSQL = `
INSERT INTO leader_leases (lease_name, holder_id, fence_token, expires_at)
VALUES ($1, $2, nextval('leader_lease_fence_token_seq'), now() + make_interval(secs => $3))
ON CONFLICT (lease_name) DO UPDATE SET
    holder_id   = EXCLUDED.holder_id,
    fence_token = CASE WHEN leader_leases.holder_id = EXCLUDED.holder_id
                        THEN leader_leases.fence_token
                        ELSE EXCLUDED.fence_token
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

// acquireOrHold's acquisition carries a cycleTracker as its CycleReporter,
// scoped to this one term (see docs/leader-election.md, "Reporting job
// cycles").
func (s *leaseStrategy) acquireOrHold(ctx context.Context, name string) (acquisition, bool, error) {
	_, ok, err := s.tryAcquireOrRenew(ctx, name)
	if err != nil {
		return acquisition{}, false, err
	}
	if !ok {
		return acquisition{}, false, nil
	}

	// Leadership acquired. A background watchdog renews on renewInterval
	// for as long as leaderCtx is alive — see watchdog for how it decides
	// when a renewal failure actually means leadership is gone. tracker is
	// this acquisition's own CycleReporter — scoped to this one Run call,
	// never shared across concurrent acquisitions of other lease names —
	// and the same watchdog tick that renews also checks it, so a cycle
	// exceeding its declared budget is treated exactly like losing the
	// lease outright (see watchdog).
	leaderCtx, cancel := context.WithCancel(ctx)
	tracker := &cycleTracker{}
	watchdogDone := watchdog(leaderCtx, cancel, s, name, s.renewInterval, s.ttl, tracker)

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
	return acquisition{leaderCtx: leaderCtx, release: release, reporter: tracker}, true, nil
}

// leaseRenewer is the seam watchdog needs to attempt a renewal — a
// dedicated interface (parallel to advisoryConn's rationale) purely so
// tests can inject deterministic transient-then-recovered failure
// sequences that a real Postgres connection can't be forced into on
// demand. *leaseStrategy satisfies this trivially via tryAcquireOrRenew.
type leaseRenewer interface {
	tryAcquireOrRenew(ctx context.Context, name string) (fenceToken int64, ok bool, err error)
}

// watchdog renews the lease via r every renewInterval for as long as
// leaderCtx is alive (via runCancelWatchdog), canceling it the moment
// leadership can no longer be trusted:
//   - a reported cycle (via tracker) has run longer than its own declared
//     budget — checked first, before ever touching the renewal round trip
//     at all: cancels that cycle's own context (the job may be blocked on
//     something that would otherwise never notice) and steps down exactly
//     as if the lease had been lost to another holder, since a job this
//     far past its own declared budget can no longer be trusted either
//     way (see docs/leader-election.md, "Reporting job cycles"). tracker
//     may be nil, or may simply have never received a report — nothing
//     to check yet, not a violation.
//   - !stillOK (err == nil): the lease is confirmed held by another
//     holder — authoritative, cancels immediately.
//   - err != nil: transient (e.g. a dropped connection); retried on the
//     next tick. Cancels only once time since the last successful renewal
//     reaches ttl, since by then the lease may genuinely have expired
//     server-side.
func watchdog(leaderCtx context.Context, cancel context.CancelFunc, r leaseRenewer, name string, renewInterval, ttl time.Duration, tracker *cycleTracker) chan struct{} {
	lastSuccess := time.Now()
	return runCancelWatchdog(leaderCtx, cancel, renewInterval, func(renewCtx context.Context) bool {
		if tracker != nil {
			if cycleCancel, over := tracker.overBudget(); over {
				slog.Warn("job cycle exceeded its declared budget; canceling and stepping down", "lease", name)
				cycleCancel()
				return false
			}
		}

		_, stillOK, err := r.tryAcquireOrRenew(renewCtx, name)
		switch {
		case err == nil && stillOK:
			lastSuccess = time.Now()
			return true
		case err == nil && !stillOK:
			slog.Warn("lease lost to another holder; stepping down", "lease", name)
			return false
		case time.Since(lastSuccess) >= ttl:
			slog.Warn("lease renewal failing past TTL margin; stepping down", "lease", name, "err", err)
			return false
		default:
			slog.Warn("lease renewal failed; retrying within TTL margin", "lease", name, "err", err)
			return true
		}
	})
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
