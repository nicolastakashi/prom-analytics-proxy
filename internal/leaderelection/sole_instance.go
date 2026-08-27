package leaderelection

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// NewSoleInstance returns an Elector for a deployment where this process is
// the only one that could run the job at all - SQLite mode, one database file
// per replica, nothing to contend over. It holds leadership from the first
// attempt for as long as its caller's context lives, so every provider has an
// elector and no caller has to special-case the one that doesn't. See
// docs/leader-election.md for what that buys beyond the branch it removes.
func NewSoleInstance(reg prometheus.Registerer) Elector {
	return newElector(soleInstanceStrategy{}, backoffConfig{}, reg)
}

// soleInstanceStrategy grants leadership without asking anything, because
// there is nothing to ask: no lock, no lease row, no table - which is why
// NewSoleInstance needs no *sql.DB, unlike New.
type soleInstanceStrategy struct{}

func (soleInstanceStrategy) acquireOrHold(ctx context.Context, _ string) (acquisition, bool, error) {
	// release is the cancel alone: no lock to hand back, and canceling is
	// what ends leaderCtx when leadership ends.
	leaderCtx, cancel := context.WithCancel(ctx)
	return acquisition{leaderCtx: leaderCtx, release: cancel}, true, nil
}
