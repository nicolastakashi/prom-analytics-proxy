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

// fakeAdvisoryConn is a hand-written fake for advisoryConn, injecting
// deterministic unlock/ping outcomes that a real Postgres backend can't be
// forced into on demand (a statement-timeout-but-session-alive unlock
// failure, or a session that dies mid-lease) — the same rationale
// flakyConnGetter documents for connGetter.
type fakeAdvisoryConn struct {
	mu sync.Mutex

	unlockResult bool
	unlockErr    error
	unlockCtx    context.Context

	pingErrAfter int // ping fails starting from this call number; 0 = never fails
	pingCalls    int

	discarded bool
	closed    bool
}

func (f *fakeAdvisoryConn) unlock(ctx context.Context, _ int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlockCtx = ctx
	return f.unlockResult, f.unlockErr
}

func (f *fakeAdvisoryConn) ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pingCalls++
	if f.pingErrAfter > 0 && f.pingCalls >= f.pingErrAfter {
		return errors.New("simulated ping failure")
	}
	return nil
}

func (f *fakeAdvisoryConn) discard() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = true
}

func (f *fakeAdvisoryConn) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeAdvisoryConn) snapshot() (discarded, closed bool, pingCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discarded, f.closed, f.pingCalls
}

// TestNewHeldLease_DiscardsConnection_WhenUnlockErrors pins the fix for the
// leak this package exists to close: a connection whose unlock call errors
// (e.g. a statement timeout, which does not kill the session) must be
// force-discarded rather than pooled — pooling it would return it to
// ordinary application traffic while it still physically holds the lock,
// leaking the lease forever.
func TestNewHeldLease_DiscardsConnection_WhenUnlockErrors(t *testing.T) {
	ac := &fakeAdvisoryConn{unlockErr: errors.New("simulated statement timeout")}
	_, release := newHeldLease(context.Background(), ac, 1, "fault-test", time.Hour)

	release()

	discarded, closed, _ := ac.snapshot()
	assert.True(t, discarded, "an unlock error must force-discard the connection")
	assert.False(t, closed, "a discarded connection must not also be pooled via close()")
}

// TestNewHeldLease_DiscardsConnection_WhenUnlockReturnsFalse covers the
// other half of "checking both return values": pg_advisory_unlock can
// return false with no error at all (the session doesn't hold the lock),
// which is just as much a failure to actually release as a query error.
func TestNewHeldLease_DiscardsConnection_WhenUnlockReturnsFalse(t *testing.T) {
	ac := &fakeAdvisoryConn{unlockResult: false, unlockErr: nil}
	_, release := newHeldLease(context.Background(), ac, 1, "fault-test", time.Hour)

	release()

	discarded, closed, _ := ac.snapshot()
	assert.True(t, discarded, "unlocked=false must force-discard the connection even with err == nil")
	assert.False(t, closed, "a discarded connection must not also be pooled via close()")
}

// TestNewHeldLease_ClosesConnectionNormally_WhenUnlockSucceeds is the
// counterpart to the two tests above: the common case (unlock actually
// releases the lock) must return the connection to the pool for reuse, not
// discard a perfectly good connection.
func TestNewHeldLease_ClosesConnectionNormally_WhenUnlockSucceeds(t *testing.T) {
	ac := &fakeAdvisoryConn{unlockResult: true, unlockErr: nil}
	_, release := newHeldLease(context.Background(), ac, 1, "fault-test", time.Hour)

	release()

	discarded, closed, _ := ac.snapshot()
	assert.True(t, closed, "a successful unlock must return the connection to the pool")
	assert.False(t, discarded, "a successful unlock must not discard a healthy connection")
}

// TestNewHeldLease_ReleaseUsesABoundedDetachedContext pins both halves of
// the fix for the shutdown-hang regression: release must run detached from
// the caller's ctx (so cleanup still runs after ctx is already canceled),
// but that detached context must carry a deadline — not
// context.Background()'s none-at-all — or a stuck query blocks process
// shutdown on the OS TCP timeout instead of failing over to the discard
// path.
func TestNewHeldLease_ReleaseUsesABoundedDetachedContext(t *testing.T) {
	ac := &fakeAdvisoryConn{unlockResult: true}

	callerCtx, cancel := context.WithCancel(context.Background())
	cancel() // simulate release() running after the caller's ctx is already gone

	_, release := newHeldLease(callerCtx, ac, 1, "fault-test", time.Hour)
	release()

	deadline, ok := ac.unlockCtx.Deadline()
	require.True(t, ok, "the context passed to unlock must carry a deadline, not context.Background()")
	assert.WithinDuration(t, time.Now().Add(releaseUnlockTimeout), deadline, 2*time.Second,
		"the unlock deadline must be bounded around releaseUnlockTimeout")

	_, closed, _ := ac.snapshot()
	assert.True(t, closed, "unlock must still run to completion (and succeed) even though callerCtx was already canceled")
}

// TestNewHeldLease_CancelsLeaderCtx_WhenLivenessPingFails pins the fix for
// the split-brain gap: if the physical backend session dies without the
// client noticing (server restart, pg_terminate_backend, a half-open
// partition), Postgres frees the advisory lock immediately regardless of
// whether this process's leaderCtx is still live. leaderCtx must be
// canceled the moment a liveness ping detects that, not only when release()
// is explicitly called.
func TestNewHeldLease_CancelsLeaderCtx_WhenLivenessPingFails(t *testing.T) {
	ac := &fakeAdvisoryConn{pingErrAfter: 1}
	leaderCtx, release := newHeldLease(context.Background(), ac, 1, "liveness-test", 5*time.Millisecond)
	defer release()

	select {
	case <-leaderCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("leaderCtx must be canceled once a liveness ping fails, without release() ever being called")
	}
}

// TestNewHeldLease_LeaderCtxStaysLive_WhileLivenessPingsSucceed is the
// liveness check's counterpart safety test: it must not cancel leaderCtx on
// its own as long as the connection stays healthy, or every lease would be
// spuriously torn down.
func TestNewHeldLease_LeaderCtxStaysLive_WhileLivenessPingsSucceed(t *testing.T) {
	ac := &fakeAdvisoryConn{}
	leaderCtx, release := newHeldLease(context.Background(), ac, 1, "liveness-healthy-test", 5*time.Millisecond)
	defer release()

	select {
	case <-leaderCtx.Done():
		t.Fatal("leaderCtx must stay live while liveness pings keep succeeding")
	case <-time.After(50 * time.Millisecond):
	}

	_, _, pingCalls := ac.snapshot()
	assert.Greater(t, pingCalls, 0, "the liveness goroutine must actually be pinging on its interval")
}

// TestNewHeldLease_StopsLivenessGoroutine_AfterRelease proves release()
// doesn't leak the liveness goroutine: once released, pings must stop
// happening, rather than continuing forever against a connection that's
// already been closed or discarded.
func TestNewHeldLease_StopsLivenessGoroutine_AfterRelease(t *testing.T) {
	ac := &fakeAdvisoryConn{unlockResult: true}
	_, release := newHeldLease(context.Background(), ac, 1, "liveness-stop-test", 5*time.Millisecond)

	release()
	_, _, callsAtRelease := ac.snapshot()

	time.Sleep(50 * time.Millisecond)
	_, _, callsAfterWait := ac.snapshot()

	assert.Equal(t, callsAtRelease, callsAfterWait, "no further liveness pings may occur after release()")
}
