package leaderelection

import "context"

// strategy is the seam between the generic elector wrapper (retry loop +
// metrics, shared by every strategy) and a concrete leadership mechanism
// (advisory locks today, a lease/TTL row later).
type strategy interface {
	// acquireOrHold attempts to become/remain leader for name for one
	// attempt. Return values:
	//   - ok=false, err=nil: currently held by someone else; caller backs
	//     off and retries.
	//   - ok=false, err!=nil: a transient error (e.g. a connection error);
	//     caller backs off and retries — err is logged, never returned up
	//     through Elector.Run. No error from acquireOrHold may ever cause
	//     a silent, permanent stop; only ctx cancellation may.
	//   - ok=true: caller now holds leadership. leaderCtx is derived from
	//     ctx and MUST be canceled — by release(), or internally by the
	//     strategy the moment leadership is lost — the instant leadership
	//     ends, even mid-fn. release() must be safe to call exactly once
	//     and must physically release the underlying lock/lease itself,
	//     never relying on connection-pool GC to do so eventually.
	acquireOrHold(ctx context.Context, name string) (leaderCtx context.Context, release func(), ok bool, err error)
}
