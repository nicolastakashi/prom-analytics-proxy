// Package leaderelection provides single-leader coordination across multiple
// replicas of this process sharing a PostgreSQL database. It exists as its
// own package (rather than living inside internal/inventory, which is where
// this logic used to live) because leader election is infrastructure shared
// by the inventory syncer and the retention worker — neither owns it.
//
// See docs/leader-election.md for why this exists, why it's PostgreSQL-only,
// and the design rationale behind the advisory-lock strategy.
package leaderelection

import "context"

// Elector runs fn continuously whenever this process holds leadership for
// name, stepping aside as soon as leadership is lost or ctx is canceled.
//
// Run blocks until ctx is Done. While it holds leadership for name, it
// invokes fn(leaderCtx) — leaderCtx is derived from ctx and is canceled
// promptly the moment leadership is lost (even mid-fn) or ctx is canceled,
// whichever happens first. Run returns nil only via ctx cancellation; every
// retryable error (including transient database errors) is retried
// internally with backoff and never surfaces as a non-nil return, so a
// caller can wire Run directly into an oklog/run.Group actor without any
// extra error handling:
//
//	g.Add(func() error { return elector.Run(ctx, name, fn) }, func(error) { cancel() })
type Elector interface {
	Run(ctx context.Context, name string, fn func(context.Context)) error
}
