package leaderelection

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// backoffConfig controls how quickly elector retries after either "not
// currently leader" or a transient error from acquireOrHold. Backoff starts
// at initial and doubles up to max, resetting to initial the moment
// leadership is (re)acquired.
type backoffConfig struct {
	initial time.Duration
	max     time.Duration
}

func (b backoffConfig) orDefault() backoffConfig {
	if b.initial <= 0 {
		b.initial = 2 * time.Second // matches the fixed poll interval this replaces
	}
	if b.max <= 0 || b.max < b.initial {
		b.max = 10 * time.Second
	}
	return b
}

// elector is the generic, strategy-agnostic Elector implementation: it owns
// the retry-loop-with-backoff and the Prometheus metrics, so every strategy
// gets both for free without duplicating either.
type elector struct {
	strat   strategy
	backoff backoffConfig
	m       *metrics
}

func newElector(strat strategy, backoff backoffConfig, reg prometheus.Registerer) *elector {
	return &elector{strat: strat, backoff: backoff.orDefault(), m: newMetrics(reg)}
}

func (e *elector) Run(ctx context.Context, name string, fn func(context.Context)) error {
	wait := e.backoff.initial
	for {
		if ctx.Err() != nil {
			return nil
		}

		leaderCtx, release, ok, err := e.strat.acquireOrHold(ctx, name)
		if err != nil {
			// Must never cause a silent, permanent return — the only event
			// allowed to stop this loop is ctx being canceled — but must
			// also not go unlogged, or a persistently broken election
			// (bad credentials, exhausted pool) is indistinguishable from
			// the ordinary "another replica is leader".
			slog.Warn("leader election attempt failed", "lease", name, "err", err)
			if !e.sleep(ctx, jitter(wait)) {
				return nil
			}
			wait = growBackoff(wait, e.backoff.max)
			continue
		}
		if !ok {
			// "Held by someone else" is the steady state for every
			// follower, not a failure — back off here and every standby
			// walks its wait toward max over time, regressing failover
			// latency well past the fixed poll interval this replaces.
			// Poll at the fixed initial interval for as long as
			// contention persists.
			if !e.sleep(ctx, e.backoff.initial) {
				return nil
			}
			continue
		}

		wait = e.backoff.initial // reset now that leadership was acquired

		e.m.isLeader.WithLabelValues(name).Set(1)
		e.m.transitions.WithLabelValues(name, "leader").Inc()

		fn(leaderCtx)
		release()

		e.m.isLeader.WithLabelValues(name).Set(0)
		e.m.transitions.WithLabelValues(name, "follower").Inc()
		// fn returning means ctx was canceled or leadership was lost
		// mid-run (a lease strategy may cancel leaderCtx on its own);
		// looping back lets the checks above sort out which happened.
	}
}

// sleep waits for d or ctx cancellation, whichever comes first, reporting
// which one happened so callers can distinguish "waited it out" from "must
// stop".
func (e *elector) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// growBackoff doubles wait, capped at max.
func growBackoff(wait, max time.Duration) time.Duration {
	wait *= 2
	if wait > max {
		wait = max
	}
	return wait
}

// jitter adds up to ±20% randomness to d so replicas started by the same
// rollout don't all retry against Postgres in lockstep.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := time.Duration(rand.Int64N(int64(d)/5 + 1))
	if rand.IntN(2) == 0 {
		return d - delta
	}
	return d + delta
}
