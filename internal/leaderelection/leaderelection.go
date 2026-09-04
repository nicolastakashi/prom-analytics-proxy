// Package leaderelection decides which replica of this process may run a
// given periodic job. It exists as its own package (rather than living
// inside internal/inventory, which is where this logic used to live) because
// leader election is infrastructure shared by the inventory syncer and the
// retention worker — neither owns it.
//
// Coordination between replicas needs shared state, and both strategies that
// provide it are PostgreSQL-only. Where there is nothing to coordinate —
// SQLite, one database file per replica — NewSoleInstance holds leadership
// outright, so every caller gets an Elector and none has to special-case the
// provider it runs on.
//
// See docs/leader-election.md for the design rationale behind each strategy.
package leaderelection

import "context"

// Elector runs fn continuously whenever this process holds leadership for
// name, stepping aside as soon as leadership is lost or ctx is canceled.
//
// Run blocks until ctx is Done. While it holds leadership for name, it
// invokes fn(leaderCtx, reporter) — leaderCtx is derived from ctx and is
// canceled promptly the moment leadership is lost (even mid-fn) or ctx is
// canceled, whichever happens first. reporter lets fn hand over one cycle's
// cancel func and declared budget (see CycleReporter and PeriodicJob,
// which most callers should use to build fn rather than implementing this
// by hand); it may be nil and is safe to ignore entirely. Run returns nil
// only via ctx cancellation; every retryable error (including transient
// database errors) is retried internally with backoff and never surfaces
// as a non-nil return, so a caller can wire Run directly into an
// oklog/run.Group actor without any extra error handling:
//
//	g.Add(func() error { return elector.Run(ctx, name, fn) }, func(error) { cancel() })
type Elector interface {
	Run(ctx context.Context, name string, fn func(context.Context, CycleReporter)) error
}
