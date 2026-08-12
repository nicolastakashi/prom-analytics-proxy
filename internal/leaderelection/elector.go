package leaderelection

import (
	"context"
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
		if err != nil || !ok {
			// Both "held by someone else" (err == nil) and a transient
			// error retry identically here: an error from acquireOrHold
			// must never cause a silent, permanent return — the only
			// event allowed to stop this loop is ctx being canceled.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			if wait < e.backoff.max {
				wait *= 2
				if wait > e.backoff.max {
					wait = e.backoff.max
				}
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
