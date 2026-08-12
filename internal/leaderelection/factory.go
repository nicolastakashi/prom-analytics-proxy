package leaderelection

import (
	"database/sql"
	"fmt"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Strategy identifies a leader-election mechanism. config.LeaderElectionConfig.Strategy
// stores this as a plain string — flag.StringVar only binds to *string, and
// internal/config can't import this package back to reference these
// constants for a default value (this package already imports
// internal/config for the config struct itself) — so the raw string lives
// in config, and this named type exists for everything that branches on
// it: New's switch below, its error messages, and this package's own
// tests. Mirrors db.DatabaseProvider (internal/db/model.go) for exactly
// the same reason.
type Strategy string

const (
	AdvisoryLock Strategy = "advisory-lock"
	Lease        Strategy = "lease"
)

// New constructs the Elector selected by cfg.Strategy. Both call sites that
// need leader election (the inventory syncer and the retention worker)
// should share a single Elector returned from one New call — constructing
// two would register the same metric names against reg twice and panic.
//
// Strategy is matched case-sensitively and not normalized: an operator
// typo in the flag/config value should surface as a startup error, not
// silently fall back to a default.
func New(cfg config.LeaderElectionConfig, db *sql.DB, reg prometheus.Registerer) (Elector, error) {
	switch Strategy(cfg.Strategy) {
	case "", AdvisoryLock:
		return NewAdvisoryElector(db, reg), nil
	case Lease:
		if cfg.LeaseTTL <= 0 {
			return nil, fmt.Errorf("leaderelection: %s strategy requires a positive lease_ttl, got %s", Lease, cfg.LeaseTTL)
		}
		if cfg.RenewInterval <= 0 {
			return nil, fmt.Errorf("leaderelection: %s strategy requires a positive renew_interval, got %s", Lease, cfg.RenewInterval)
		}
		if cfg.RenewInterval >= cfg.LeaseTTL {
			return nil, fmt.Errorf("leaderelection: renew_interval (%s) must be less than lease_ttl (%s), or a healthy holder can lose its own lease", cfg.RenewInterval, cfg.LeaseTTL)
		}
		strat := newLeaseStrategy(db, cfg.LeaseTTL, cfg.RenewInterval)
		return newElector(strat, backoffConfig{}, reg), nil
	default:
		return nil, fmt.Errorf("leaderelection: unknown strategy %q, supported: %s, %s", cfg.Strategy, AdvisoryLock, Lease)
	}
}
