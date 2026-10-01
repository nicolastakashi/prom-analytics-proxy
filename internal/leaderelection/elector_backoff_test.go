package leaderelection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingStrategy always fails to acquire — either plain contention
// (returnErr == nil: "someone else holds it") or a transient error
// (returnErr != nil) — and records the wall-clock time of each attempt. The
// elector's retry-pacing behavior doesn't depend on which strategy is
// plugged in, so this avoids needing a real database for tests that are
// purely about timing.
type recordingStrategy struct {
	mu        sync.Mutex
	times     []time.Time
	returnErr error
}

func (s *recordingStrategy) acquireOrHold(ctx context.Context, name string) (acquisition, bool, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	return acquisition{}, false, s.returnErr
}

func (s *recordingStrategy) snapshot() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

// TestElector_Run_BacksOffThenCapsRetryRate_OnErrors pins the observable
// retry-pacing contract for the error path without asserting on the
// internal wait variable or its doubling formula: retrying against a
// strategy that always errors must (a) not busy-loop — the first retry
// waits roughly the configured initial backoff, (b) actually grow from that
// initial value, and (c) reach, but never wildly exceed, the configured cap.
// Any of these failing would mean either a retry storm against the database
// or a recovery that gets slower and slower without bound — both real
// operational problems, not just internal bookkeeping.
func TestElector_Run_BacksOffThenCapsRetryRate_OnErrors(t *testing.T) {
	strat := &recordingStrategy{returnErr: errors.New("simulated transient error")}
	const initial = 20 * time.Millisecond
	const max = 100 * time.Millisecond
	elector := newTestElector(strat, backoffConfig{initial: initial, max: max})

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	err := elector.Run(ctx, "backoff-pacing-test", func(context.Context, CycleReporter) {
		t.Fatal("fn must never run: this strategy never grants leadership")
	})
	assert.NoError(t, err)

	times := strat.snapshot()
	require.GreaterOrEqual(t, len(times), 4, "must retry multiple times within the window") // gaps indexing below needs at least this many

	gaps := make([]time.Duration, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps[i-1] = times[i].Sub(times[i-1])
	}

	// Jitter (up to ±20%) widens the tolerance on the first-gap check, but
	// must not defeat it: a busy loop (near-zero gaps) still fails this.
	assert.InDelta(t, float64(initial), float64(gaps[0]), float64(20*time.Millisecond),
		"the first retry must wait roughly the initial backoff, not busy-loop")

	maxGap := gaps[0]
	for _, g := range gaps {
		if g > maxGap {
			maxGap = g
		}
	}
	assert.InDelta(t, float64(max), float64(maxGap), float64(45*time.Millisecond),
		"backoff must actually reach its configured cap, and never exceed it by much")

	assert.Greater(t, gaps[len(gaps)-1], gaps[0]*2,
		"backoff must grow from its initial value over repeated errors, not stay flat")
}

// TestElector_Run_ContentionPollsAtFixedIntervalWithoutBackoff pins the
// failover-latency regression this fixes: ok=false, err=nil ("someone else
// holds it") is the steady state for every follower, so growing backoff on
// plain contention means every standby walks its wait up toward the cap
// over time — and when the leader dies, takeover then takes as long as
// whatever the wait happened to grow to, not the fixed poll interval the
// system is documented to have. Contention must keep polling at
// backoff.initial for as long as it persists, never growing toward max.
func TestElector_Run_ContentionPollsAtFixedIntervalWithoutBackoff(t *testing.T) {
	strat := &recordingStrategy{} // returnErr == nil: plain contention
	const initial = 20 * time.Millisecond
	const max = 100 * time.Millisecond
	elector := newTestElector(strat, backoffConfig{initial: initial, max: max})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := elector.Run(ctx, "contention-fixed-interval-test", func(context.Context, CycleReporter) {
		t.Fatal("fn must never run: this strategy never grants leadership")
	})
	assert.NoError(t, err)

	times := strat.snapshot()
	require.GreaterOrEqual(t, len(times), 6, "must retry multiple times within the window") // loop below needs at least this many

	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		assert.InDelta(t, float64(initial), float64(gap), float64(15*time.Millisecond),
			"gap %d: plain contention must poll at the fixed initial interval, never growing toward max", i)
	}
}

// failThenSucceedOnceStrategy fails to acquire failsBeforeSuccess times
// (recording each attempt's wall-clock time), grants leadership exactly
// once on the attempt right after that, then fails on every attempt after —
// letting a test observe backoff's state exactly at the moment leadership
// is (re)acquired and immediately lost again.
type failThenSucceedOnceStrategy struct {
	mu                 sync.Mutex
	times              []time.Time
	failsBeforeSuccess int
	attempts           int
	succeeded          bool
}

func (s *failThenSucceedOnceStrategy) acquireOrHold(ctx context.Context, _ string) (acquisition, bool, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.attempts++
	attempt := s.attempts
	alreadySucceeded := s.succeeded
	s.mu.Unlock()

	if attempt == s.failsBeforeSuccess+1 && !alreadySucceeded {
		s.mu.Lock()
		s.succeeded = true
		s.mu.Unlock()
		leaderCtx, cancel := context.WithCancel(ctx)
		return acquisition{leaderCtx: leaderCtx, release: cancel}, true, nil
	}
	return acquisition{}, false, errors.New("simulated error")
}

func (s *failThenSucceedOnceStrategy) snapshot() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

// TestElector_Run_ResetsBackoffToInitial_AfterReacquiringLeadership pins
// backoffConfig's documented reset behavior ("resetting to initial the
// moment leadership is (re)acquired"), which nothing else in this suite
// exercises: every other backoff test either never succeeds or never fails
// again afterward. Without the reset, a transient blip early in a
// long-running process's life would permanently inflate its post-blip
// contention-polling latency toward max — this proves failure immediately
// following a hand-off starts from initial again, not from wherever backoff
// had grown to pre-hand-off.
func TestElector_Run_ResetsBackoffToInitial_AfterReacquiringLeadership(t *testing.T) {
	const initial = 5 * time.Millisecond
	const max = 100 * time.Millisecond
	const failsBeforeSuccess = 6 // enough doublings from 5ms to reach the 100ms cap
	strat := &failThenSucceedOnceStrategy{failsBeforeSuccess: failsBeforeSuccess}
	elector := newTestElector(strat, backoffConfig{initial: initial, max: max})

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	err := elector.Run(ctx, "backoff-reset-test", func(context.Context, CycleReporter) {}) // fn returns immediately: release right away
	assert.NoError(t, err)

	times := strat.snapshot()
	require.Greater(t, len(times), failsBeforeSuccess+2, "must have retried past the hand-off at least once")

	successIdx := failsBeforeSuccess // 0-indexed: the (failsBeforeSuccess+1)-th attempt
	preSuccessGap := times[successIdx].Sub(times[successIdx-1])
	postSuccessGap := times[successIdx+2].Sub(times[successIdx+1])

	assert.Greater(t, preSuccessGap, initial*4,
		"sanity check: backoff must actually have grown well past initial before the hand-off")
	assert.InDelta(t, float64(initial), float64(postSuccessGap), float64(15*time.Millisecond),
		"the first retry after losing leadership again must wait ~initial, not continue from the pre-hand-off backoff")
}

// TestJitter_StaysWithinBoundsAndVaries pins jitter as a pure function,
// independent of timing: it must never move a duration by more than ~20%,
// but also must not be a no-op — replicas started by the same rollout would
// otherwise poll Postgres in lockstep.
func TestJitter_StaysWithinBoundsAndVaries(t *testing.T) {
	const d = 100 * time.Millisecond
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		j := jitter(d)
		assert.GreaterOrEqual(t, j, d-d/5, "jitter must not move the duration down by more than ~20%%")
		assert.LessOrEqual(t, j, d+d/5, "jitter must not move the duration up by more than ~20%%")
		seen[j] = true
	}
	assert.Greater(t, len(seen), 1, "jitter must vary across calls, not return a constant offset")
}
