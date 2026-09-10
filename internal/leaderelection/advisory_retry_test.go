package leaderelection

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// flakyConnGetter fails the first failsLeft calls to Conn, then delegates to
// the real connGetter — a hand-written fake introduced purely as a test
// seam, since forcing a real Postgres connection to fail exactly once via
// testcontainers stop/start is slow and imprecise.
type flakyConnGetter struct {
	real      connGetter
	failsLeft int32
	attempts  int32
}

func (f *flakyConnGetter) Conn(ctx context.Context) (*sql.Conn, error) {
	atomic.AddInt32(&f.attempts, 1)
	if atomic.AddInt32(&f.failsLeft, -1) >= 0 {
		return nil, errors.New("simulated transient connection error")
	}
	// failsLeft went negative: put it back at -1 forever so we don't wrap around.
	atomic.StoreInt32(&f.failsLeft, -1)
	return f.real.Conn(ctx)
}

// TestAdvisoryElector_RetriesAfterTransientConnError_DoesNotReturnSilently
// pins the invariant that a transient db.Conn error must never cause a
// silent, permanent return: Elector.Run must retry past it instead, and
// must never return before ctx is canceled.
func TestAdvisoryElector_RetriesAfterTransientConnError_DoesNotReturnSilently(t *testing.T) {
	db := newTestPostgresDB(t)
	flaky := &flakyConnGetter{real: db, failsLeft: 1}
	elector := newTestElector(newAdvisoryLockStrategy(flaky), backoffConfig{initial: 10 * time.Millisecond, max: 20 * time.Millisecond})

	var fnCalled atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var runErr error
	go func() {
		defer wg.Done()
		runErr = elector.Run(ctx, "retry-test", func(fnCtx context.Context, _ CycleReporter) {
			fnCalled.Store(true)
			<-fnCtx.Done()
		})
	}()

	// fn must eventually run despite the first Conn() call failing.
	assert.Eventually(t, fnCalled.Load, time.Second, 10*time.Millisecond,
		"fn should eventually run despite the injected transient error")

	cancel()
	wg.Wait()
	assert.NoError(t, runErr, "Run must return nil only via ctx cancellation")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&flaky.attempts), int32(2), "must have retried Conn() at least once after the injected failure")
}
