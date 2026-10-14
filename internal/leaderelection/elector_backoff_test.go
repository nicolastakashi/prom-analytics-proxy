package leaderelection

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestElector_Run_BacksOffThenCapsRetryRate_OnErrors pins the observable
// retry-pacing contract for the error path: retrying against a strategy that
// always errors waits the initial backoff, then twice the previous wait, up to
// the cap, each wait jittered. Breaking it means a retry storm against the
// database, a recovery that slows without bound, or replicas retrying in
// lockstep.
func TestElector_Run_BacksOffThenCapsRetryRate_OnErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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

		jittered := false
		for i, gap := range gaps {
			want := min(initial<<i, max)
			assertJittered(t, want, gap, "retry %d must wait %v (doubling from the initial backoff, capped), jittered", i+1, want)
			jittered = jittered || gap != want
		}
		assert.Equal(t, max, min(initial<<(len(gaps)-1), max), "sanity check: the window must reach the cap")
		assert.True(t, jittered, "retry waits must be jittered, or replicas retry in lockstep")
	})
}

// TestElector_Run_ContentionPollsAtFixedIntervalWithoutBackoff pins the
// failover-latency regression this fixes: ok=false, err=nil ("someone else
// holds it") is the steady state for every follower, so growing backoff on
// plain contention means every standby walks its wait up toward the cap
// over time — and when the leader dies, takeover then takes as long as
// whatever the wait happened to grow to, not the fixed poll interval the
// system is documented to have. Errors before the contention grow the
// backoff, which must not carry over. Contention must keep polling at
// backoff.initial for as long as it persists, never growing toward max.
func TestElector_Run_ContentionPollsAtFixedIntervalWithoutBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// Errors first grow the backoff to 4x initial; contention follows.
		const errorsFirst = 3
		strat := &recordingStrategy{errorsFirst: errorsFirst}
		const initial = 20 * time.Millisecond
		const max = 100 * time.Millisecond
		elector := newTestElector(strat, backoffConfig{initial: initial, max: max})

		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		err := elector.Run(ctx, "contention-fixed-interval-test", func(context.Context, CycleReporter) {
			t.Fatal("fn must never run: this strategy never grants leadership")
		})
		assert.NoError(t, err)

		times := strat.snapshot()
		require.GreaterOrEqual(t, len(times), errorsFirst+6, "must poll repeatedly after the errors") // loop below needs at least this many

		for i := errorsFirst + 1; i < len(times); i++ {
			gap := times[i].Sub(times[i-1])
			assert.Equal(t, initial, gap, "gap %d: plain contention must poll at the fixed initial interval, never growing toward max", i)
		}
	})
}

// failThenSucceedOnceStrategy fails failsBeforeSuccess attempts, grants
// leadership exactly once on the next, then fails every attempt after —
// letting a test observe backoff's state at the moment leadership is
// (re)acquired and immediately lost again. Attempt times are logged.
type failThenSucceedOnceStrategy struct {
	attemptLog
	failsBeforeSuccess int
}

func (s *failThenSucceedOnceStrategy) acquireOrHold(ctx context.Context, _ string) (acquisition, bool, error) {
	if s.record() == s.failsBeforeSuccess+1 {
		leaderCtx, cancel := context.WithCancel(ctx)
		return acquisition{leaderCtx: leaderCtx, release: cancel}, true, nil
	}
	return acquisition{}, false, errors.New("simulated error")
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		assertJittered(t, initial, postSuccessGap,
			"the first retry after losing leadership again must wait the initial backoff, not continue from the pre-hand-off one")
	})
}

// TestJitter_StaysWithinBoundsAndVaries pins jitter as a pure function,
// independent of timing: it must never move a duration by more than ~20%,
// but also must not be a no-op — replicas started by the same rollout would
// otherwise poll Postgres in lockstep.
func TestJitter_StaysWithinBoundsAndVaries(t *testing.T) {
	t.Parallel()
	const d = 100 * time.Millisecond
	seen := map[time.Duration]bool{}
	var shorter, longer bool
	for i := 0; i < 50; i++ {
		j := jitter(d)
		assert.GreaterOrEqual(t, j, d-d/5, "jitter must not move the duration down by more than ~20%%")
		assert.LessOrEqual(t, j, d+d/5, "jitter must not move the duration up by more than ~20%%")
		seen[j] = true
		shorter = shorter || j < d
		longer = longer || j > d
	}
	assert.Greater(t, len(seen), 1, "jitter must vary across calls, not return a constant offset")
	assert.True(t, shorter && longer, "jitter must move the duration both ways, not only one")
}

// assertJittered asserts got is want after jitter: within 20% of it either
// way.
func assertJittered(t *testing.T, want, got time.Duration, msgAndArgs ...any) {
	t.Helper()
	assert.GreaterOrEqual(t, got, want-want/5, msgAndArgs...)
	assert.LessOrEqual(t, got, want+want/5, msgAndArgs...)
}
