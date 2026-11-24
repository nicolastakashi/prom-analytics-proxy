package otlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

const (
	databaseRetryInitial = time.Second
	databaseRetryMax     = 15 * time.Second

	// circuitFailureThreshold is the number of consecutive failed calls that
	// opens the circuit.
	circuitFailureThreshold = 3
)

// errDatabaseUnavailable is returned by a DatabaseGate's store calls until it
// has connected, and while its circuit is open. No database call is made.
var errDatabaseUnavailable = errors.New("database unavailable")

// DatabaseGate is a Store that connects to the database in the background.
// Until connected, its calls fail at once with errDatabaseUnavailable; after,
// they go through a circuit breaker.
//
// The circuit opens after circuitFailureThreshold consecutive calls fail with
// the database unreachable or a timeout; the call that opens it returns its
// own error. A call the database answered resets the count; one whose caller
// canceled it leaves the count as it was. While open, calls fail at once with
// errDatabaseUnavailable, until a delay drawn from the connection backoff
// elapses: then a single trial call reaches the database. A trial the
// database answered closes the circuit; a failed one reopens it with the
// backoff doubled; a canceled one leaves the next call as the trial.
type DatabaseGate struct {
	connect    func(context.Context) (db.Provider, error)
	provider   atomic.Pointer[db.Provider] // nil until connected
	randInt64N func(n int64) int64         // jitter source: [0, n)

	mu       sync.Mutex
	failures int           // consecutive failed calls while closed; reset on closing
	open     bool          // circuit open
	trial    bool          // open, and a trial call is in progress
	retryAt  time.Time     // open: when the next call becomes the trial
	backoff  time.Duration // open: backoff retryAt was drawn from
}

// NewDatabaseGate returns a gate that connects to the database with connect
// once Run starts.
func NewDatabaseGate(connect func(context.Context) (db.Provider, error)) *DatabaseGate {
	databaseUp.Set(0)
	return &DatabaseGate{connect: connect, randInt64N: rand.Int64N}
}

// Run connects to the database, retrying connection failures
// (db.ErrDatabaseConnection) with capped exponential backoff, then waits
// until ctx is done. It returns any other error from connecting, such as a
// failed migration, and ctx.Err() once ctx is done.
func (g *DatabaseGate) Run(ctx context.Context) error {
	backoff := databaseRetryInitial
	for {
		p, err := g.connect(ctx)
		if err == nil {
			g.provider.Store(&p)
			databaseUp.Set(1)
			databaseConnectAttemptsTotal.WithLabelValues(resultSuccess).Inc()
			slog.InfoContext(ctx, "ingester.database.connected")
			<-ctx.Done()
			return ctx.Err()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		databaseConnectAttemptsTotal.WithLabelValues(resultFailure).Inc()
		if !errors.Is(err, db.ErrDatabaseConnection) {
			return fmt.Errorf("connect to database: %w", err)
		}

		delay := g.retryDelay(backoff)
		slog.WarnContext(ctx, "ingester.database.connect_failed", "err", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = nextBackoff(backoff)
	}
}

// retryDelay returns a delay with equal jitter over backoff: between half the
// backoff and all of it.
func (g *DatabaseGate) retryDelay(backoff time.Duration) time.Duration {
	return backoff/2 + time.Duration(g.randInt64N(int64(backoff/2)+1))
}

// nextBackoff returns the backoff after backoff: doubled, up to
// databaseRetryMax.
func nextBackoff(backoff time.Duration) time.Duration {
	return min(2*backoff, databaseRetryMax)
}

// Close closes the database, if connected.
func (g *DatabaseGate) Close() error {
	if p := g.provider.Load(); p != nil {
		return (*p).Close()
	}
	return nil
}

func (g *DatabaseGate) GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error) {
	var metas []models.MetricMetadata
	err := g.call(ctx, func(p db.Provider) error {
		var err error
		metas, err = p.GetSeriesMetadataByNames(ctx, names, job)
		return err
	})
	return metas, err
}

func (g *DatabaseGate) UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error {
	return g.call(ctx, func(p db.Provider) error { return p.UpsertMetricsCatalog(ctx, items) })
}

// call runs fn on the database if connected and the circuit admits it, and
// records its outcome.
func (g *DatabaseGate) call(ctx context.Context, fn func(db.Provider) error) error {
	p := g.provider.Load()
	if p == nil {
		return errDatabaseUnavailable
	}
	trial, admitted := g.admit()
	if !admitted {
		return errDatabaseUnavailable
	}
	err := fn(*p)
	g.record(ctx, trial, err)
	return err
}

// admit reports whether the circuit lets a call through, and whether that
// call is the trial of an open circuit.
func (g *DatabaseGate) admit() (trial, admitted bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.open {
		return false, true
	}
	if g.trial || time.Now().Before(g.retryAt) {
		return false, false
	}
	g.trial = true
	return true, true
}

// callOutcome is what a finished call says about the database.
type callOutcome int

const (
	outcomeAnswered callOutcome = iota // the database answered
	outcomeFailed                      // unreachable or timed out
	outcomeCanceled                    // the caller gave up: says nothing
)

// classifyCall returns what a call that returned err under ctx says about
// the database.
func classifyCall(ctx context.Context, err error) callOutcome {
	switch {
	case err == nil:
		return outcomeAnswered
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		return outcomeCanceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || db.IsUnreachable(err):
		return outcomeFailed
	default:
		return outcomeAnswered
	}
}

// record updates the circuit with the outcome of a call that returned err
// under ctx. The outcome of a call admitted while closed is ignored once
// another call has opened the circuit.
func (g *DatabaseGate) record(ctx context.Context, trial bool, err error) {
	outcome := classifyCall(ctx, err)
	g.mu.Lock()
	var opened, closed bool
	var retryIn time.Duration
	switch {
	case trial && outcome == outcomeAnswered:
		g.open, g.trial, g.failures = false, false, 0
		databaseUp.Set(1)
		closed = true
	case trial && outcome == outcomeFailed:
		g.trial = false
		g.backoff = nextBackoff(g.backoff)
		retryIn, opened = g.openLocked(), true
	case trial:
		g.trial = false
	case g.open:
	case outcome == outcomeAnswered:
		g.failures = 0
	case outcome == outcomeFailed:
		g.failures++
		if g.failures >= circuitFailureThreshold {
			g.open = true
			g.backoff = databaseRetryInitial
			databaseUp.Set(0)
			retryIn, opened = g.openLocked(), true
		}
	}
	g.mu.Unlock()

	switch {
	case opened:
		slog.WarnContext(ctx, "ingester.database.circuit_opened", "err", err, "retry_in", retryIn)
	case closed:
		slog.InfoContext(ctx, "ingester.database.circuit_closed")
	}
}

// openLocked sets when the open circuit admits its next trial, drawn from
// its backoff, and returns the delay until then. g.mu must be held.
func (g *DatabaseGate) openLocked() time.Duration {
	delay := g.retryDelay(g.backoff)
	g.retryAt = time.Now().Add(delay)
	return delay
}
