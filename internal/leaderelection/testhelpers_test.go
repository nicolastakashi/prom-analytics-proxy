package leaderelection

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// attemptLog records when each acquire attempt happened.
type attemptLog struct {
	mu    sync.Mutex
	times []time.Time
}

// record logs an attempt now and returns its number, counting from 1.
func (l *attemptLog) record() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.times = append(l.times, time.Now())
	return len(l.times)
}

func (l *attemptLog) snapshot() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]time.Time(nil), l.times...)
}

// recordingStrategy never grants leadership: its first errorsFirst attempts
// fail with a transient error, every later one with returnErr (contention
// when nil), and it logs when each one happened.
type recordingStrategy struct {
	attemptLog
	errorsFirst int
	returnErr   error
}

func (s *recordingStrategy) acquireOrHold(context.Context, string) (acquisition, bool, error) {
	if s.record() <= s.errorsFirst {
		return acquisition{}, false, errors.New("simulated transient error")
	}
	return acquisition{}, false, s.returnErr
}

// mustReceive returns the value ch holds once every goroutine in the
// synctest bubble has settled, failing t if it holds none.
func mustReceive[T any](t *testing.T, ch <-chan T, msgAndArgs ...any) T {
	t.Helper()
	synctest.Wait()
	select {
	case v := <-ch:
		return v
	default:
		require.FailNow(t, "nothing received", msgAndArgs...)
		var zero T
		return zero
	}
}

// noRenewal is used as a leaseStrategy's renewInterval in tests that want
// the background renewal watchdog to never fire during the test's short
// lifetime — either because the test is exercising the raw acquire/renew
// SQL directly and doesn't want a concurrent watchdog interfering, or
// because the test is deliberately simulating a holder that stops renewing
// (a live watchdog would keep renewing regardless of whether the test
// calls acquireOrHold again, defeating the simulation).
const noRenewal = time.Hour

// newTestElector builds an elector against a fresh, throwaway registry —
// every test needs its own registry (the same metric names registered
// twice against one shared registry panics), so this is the one place
// that decision lives rather than being repeated at every call site.
func newTestElector(strat strategy, backoff backoffConfig) *elector {
	e, _ := newTestElectorWithRegistry(strat, backoff)
	return e
}

// newTestElectorWithRegistry is newTestElector's variant for tests that
// need to Gather() the registry directly — e.g. to prove a gauge's series
// exists without the test itself creating it via WithLabelValues, which
// would happen if it read the gauge instead.
func newTestElectorWithRegistry(strat strategy, backoff backoffConfig) (*elector, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return newElector(strat, backoff, reg), reg
}

// releaseTrackingStrategy always grants leadership immediately and records
// whether release() has run — enough seam for any test that needs a
// trivial, always-succeeding strategy without a real database, whether or
// not it cares about the release tracking itself.
type releaseTrackingStrategy struct {
	released atomic.Bool
}

func (s *releaseTrackingStrategy) acquireOrHold(ctx context.Context, _ string) (acquisition, bool, error) {
	leaderCtx, cancel := context.WithCancel(ctx)
	release := func() {
		cancel()
		s.released.Store(true)
	}
	return acquisition{leaderCtx: leaderCtx, release: release}, true, nil
}

// captureDefaultLogs routes the process-wide default logger to a recorder
// until t ends. Callers must not run in parallel.
func captureDefaultLogs(t testing.TB) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// recordingHandler is a hand-written slog.Handler fake that records every
// record handled, so a test can assert on log output without parsing text
// from an io.Writer.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}
