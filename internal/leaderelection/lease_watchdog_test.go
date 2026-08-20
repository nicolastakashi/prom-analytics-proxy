package leaderelection

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeLeaseRenewer is a hand-written fake for leaseRenewer, injecting a
// scripted sequence of renewal outcomes — deterministic transient-then-
// recovered failures a real Postgres connection can't be forced into on
// demand, the same rationale fakeAdvisoryConn documents for advisoryConn.
// Calls past the end of the script repeat its last entry.
type fakeLeaseRenewer struct {
	mu     sync.Mutex
	script []renewOutcome
	calls  int
}

type renewOutcome struct {
	ok  bool
	err error
}

func (f *fakeLeaseRenewer) tryAcquireOrRenew(context.Context, string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	if idx >= len(f.script) {
		idx = len(f.script) - 1
	}
	f.calls++
	o := f.script[idx]
	return 0, o.ok, o.err
}

func (f *fakeLeaseRenewer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var errSimulatedTransient = errors.New("simulated transient renewal failure")

// TestWatchdog_TransientRenewalError_DoesNotCancelLeaderCtx proves a single
// failed renewal (a dropped connection, a query timeout) does not cancel
// leaderCtx on the spot — a renew deadline exists precisely to absorb this
// without throwing away the TTL margin the renew interval provides.
func TestWatchdog_TransientRenewalError_DoesNotCancelLeaderCtx(t *testing.T) {
	const renewInterval = 10 * time.Millisecond
	const ttl = time.Hour // never approached: isolates the single-blip behavior

	renewer := &fakeLeaseRenewer{script: []renewOutcome{
		{err: errSimulatedTransient},
		{ok: true},
		{ok: true},
		{ok: true},
	}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := watchdog(leaderCtx, cancel, renewer, "transient-test", renewInterval, ttl)

	time.Sleep(renewInterval * 8)

	assert.Nil(t, leaderCtx.Err(), "a single transient renewal error must not cancel leaderCtx")
	assert.GreaterOrEqual(t, renewer.callCount(), len(renewer.script),
		"the watchdog must keep retrying past the failed tick, not stall")

	cancel()
	<-done
}

// TestWatchdog_FlappingErrors_NeverCancelWhenEachRecoveryResetsWithinTTL
// proves the deadline is measured from the last *successful* renewal, not
// wall-clock time since the watchdog started: every other tick failing,
// indefinitely, must never cancel as long as each recovery keeps the gap
// since the last success comfortably under ttl — even once total elapsed
// time exceeds ttl many times over.
func TestWatchdog_FlappingErrors_NeverCancelWhenEachRecoveryResetsWithinTTL(t *testing.T) {
	const renewInterval = 10 * time.Millisecond
	const ttl = 50 * time.Millisecond // 5x renewInterval: comfortable margin between recoveries

	script := make([]renewOutcome, 0, 40)
	for i := 0; i < 40; i++ {
		if i%2 == 0 {
			script = append(script, renewOutcome{err: errSimulatedTransient})
		} else {
			script = append(script, renewOutcome{ok: true})
		}
	}
	renewer := &fakeLeaseRenewer{script: script}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := watchdog(leaderCtx, cancel, renewer, "flapping-test", renewInterval, ttl)

	deadline := time.Now().Add(renewInterval * time.Duration(len(script)))
	for time.Now().Before(deadline) {
		if leaderCtx.Err() != nil {
			t.Fatal("leaderCtx was canceled despite every failure being followed by a recovery within the TTL margin")
		}
		time.Sleep(renewInterval)
	}

	cancel()
	<-done
}

// TestWatchdog_SustainedRenewalErrors_CancelsOncePastTTLMargin is the
// boundary this design exists to enforce: renewal failing on every tick,
// with no recovery, must eventually cancel — once the time since the last
// success reaches ttl, the lease may genuinely have expired server-side,
// and continuing to run as leader can no longer be trusted.
func TestWatchdog_SustainedRenewalErrors_CancelsOncePastTTLMargin(t *testing.T) {
	const renewInterval = 10 * time.Millisecond
	const ttl = 50 * time.Millisecond

	renewer := &fakeLeaseRenewer{script: []renewOutcome{{err: errSimulatedTransient}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	done := watchdog(leaderCtx, cancel, renewer, "sustained-failure-test", renewInterval, ttl)

	select {
	case <-leaderCtx.Done():
		assert.GreaterOrEqual(t, time.Since(start), ttl-renewInterval,
			"must not cancel meaningfully earlier than the TTL margin allows")
	case <-time.After(ttl * 4):
		t.Fatal("leaderCtx was never canceled despite renewal failing continuously past the TTL margin")
	}

	<-done
}

// TestWatchdog_LostToAnotherHolder_CancelsImmediatelyEvenWithMarginRemaining
// proves !stillOK stays authoritative regardless of TTL margin: losing the
// lease to another holder is confirmed by the database itself (the WHERE
// clause matched no row), so it cancels on the very first tick even with
// plenty of margin left.
func TestWatchdog_LostToAnotherHolder_CancelsImmediatelyEvenWithMarginRemaining(t *testing.T) {
	const renewInterval = 10 * time.Millisecond
	const ttl = time.Hour // plenty of margin left, if margin applied here

	renewer := &fakeLeaseRenewer{script: []renewOutcome{{ok: false, err: nil}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := watchdog(leaderCtx, cancel, renewer, "lost-lease-test", renewInterval, ttl)

	select {
	case <-leaderCtx.Done():
	case <-time.After(renewInterval * 20):
		t.Fatal("leaderCtx must be canceled immediately once the lease is confirmed lost to another holder")
	}

	<-done
}

// TestWatchdog_StopsCleanly_WhenLeaderCtxCanceledExternally proves the
// watchdog goroutine exits (closing done) as soon as leaderCtx is canceled
// from outside — the release() path — without needing a renewal failure of
// its own, and performs no further renewal attempts afterward.
func TestWatchdog_StopsCleanly_WhenLeaderCtxCanceledExternally(t *testing.T) {
	const renewInterval = 5 * time.Millisecond
	renewer := &fakeLeaseRenewer{script: []renewOutcome{{ok: true}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	done := watchdog(leaderCtx, cancel, renewer, "external-cancel-test", renewInterval, time.Hour)

	time.Sleep(renewInterval * 4)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchdog goroutine did not exit after leaderCtx was canceled externally")
	}

	callsAtStop := renewer.callCount()
	time.Sleep(renewInterval * 10)
	assert.Equal(t, callsAtStop, renewer.callCount(), "no renewal attempts may occur after the watchdog has stopped")
}

// TestWatchdog_LogsDistinguishTransientRetryFromTTLStepDown proves the two
// failure shapes log distinguishable messages — an on-call reader must be
// able to tell "still retrying, within margin" apart from "gave up,
// stepped down" without reading source.
func TestWatchdog_LogsDistinguishTransientRetryFromTTLStepDown(t *testing.T) {
	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const renewInterval = 10 * time.Millisecond
	const ttl = 30 * time.Millisecond

	renewer := &fakeLeaseRenewer{script: []renewOutcome{{err: errSimulatedTransient}}}
	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := watchdog(leaderCtx, cancel, renewer, "log-test", renewInterval, ttl)
	<-leaderCtx.Done()
	<-done

	var sawRetry, sawStepDown bool
	for _, r := range handler.snapshot() {
		switch {
		case strings.Contains(r.Message, "retrying within TTL margin"):
			sawRetry = true
		case strings.Contains(r.Message, "failing past TTL margin"):
			sawStepDown = true
		}
	}
	assert.True(t, sawRetry, "an in-margin retry must be logged distinctly from the final step-down")
	assert.True(t, sawStepDown, "giving up past the TTL margin must be logged distinctly from an in-margin retry")
}
