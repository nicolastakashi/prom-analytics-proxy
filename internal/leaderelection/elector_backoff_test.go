package leaderelection

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// recordingFailingStrategy always fails to acquire and records the
// wall-clock time of each attempt. The elector's retry-pacing behavior
// doesn't depend on which strategy is plugged in, so this avoids needing a
// real database for a test that's purely about timing.
type recordingFailingStrategy struct {
	mu    sync.Mutex
	times []time.Time
}

func (s *recordingFailingStrategy) acquireOrHold(ctx context.Context, name string) (context.Context, func(), bool, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	return nil, nil, false, nil
}

func (s *recordingFailingStrategy) snapshot() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

// TestElector_Run_BacksOffThenCapsRetryRate pins the observable retry-pacing
// contract without asserting on the internal wait variable or its doubling
// formula: retrying against a strategy that never grants leadership must
// (a) not busy-loop — the first retry waits roughly the configured initial
// backoff, (b) actually grow from that initial value, and (c) reach, but
// never wildly exceed, the configured cap. Any of these failing would mean
// either a retry storm against the database or a recovery that gets slower
// and slower without bound — both real operational problems, not just
// internal bookkeeping.
func TestElector_Run_BacksOffThenCapsRetryRate(t *testing.T) {
	strat := &recordingFailingStrategy{}
	const initial = 20 * time.Millisecond
	const max = 100 * time.Millisecond
	elector := newTestElector(strat, backoffConfig{initial: initial, max: max})

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	err := elector.Run(ctx, "backoff-pacing-test", func(context.Context) {
		t.Fatal("fn must never run: this strategy never grants leadership")
	})
	assert.NoError(t, err)

	times := strat.snapshot()
	if !assert.GreaterOrEqual(t, len(times), 4, "must retry multiple times within the window") {
		return
	}

	gaps := make([]time.Duration, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps[i-1] = times[i].Sub(times[i-1])
	}

	assert.InDelta(t, float64(initial), float64(gaps[0]), float64(15*time.Millisecond),
		"the first retry must wait roughly the initial backoff, not busy-loop")

	maxGap := gaps[0]
	for _, g := range gaps {
		if g > maxGap {
			maxGap = g
		}
	}
	assert.InDelta(t, float64(max), float64(maxGap), float64(40*time.Millisecond),
		"backoff must actually reach its configured cap, and never exceed it by much")

	assert.Greater(t, gaps[len(gaps)-1], gaps[0]*2,
		"backoff must grow from its initial value over repeated failures, not stay flat")
}
