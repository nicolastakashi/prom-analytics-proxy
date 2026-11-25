package otlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

const (
	databaseRetryInitial = time.Second
	databaseRetryMax     = 15 * time.Second
)

// errDatabaseUnavailable is returned by a DatabaseGate's store calls until it
// has connected. No database call is made.
var errDatabaseUnavailable = errors.New("database unavailable")

// DatabaseGate is a Store that connects to the database in the background.
// Until connected, its calls fail at once with errDatabaseUnavailable; after,
// they reach the database.
type DatabaseGate struct {
	connect    func(context.Context) (db.Provider, error)
	provider   atomic.Pointer[db.Provider] // nil until connected
	randInt64N func(n int64) int64         // jitter source: [0, n)
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

		// Equal jitter: wait between half the backoff and all of it.
		delay := backoff/2 + time.Duration(g.randInt64N(int64(backoff/2)+1))
		slog.WarnContext(ctx, "ingester.database.connect_failed", "err", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(2*backoff, databaseRetryMax)
	}
}

// Close closes the database, if connected.
func (g *DatabaseGate) Close() error {
	if p := g.provider.Load(); p != nil {
		return (*p).Close()
	}
	return nil
}

func (g *DatabaseGate) GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error) {
	p := g.provider.Load()
	if p == nil {
		return nil, errDatabaseUnavailable
	}
	return (*p).GetSeriesMetadataByNames(ctx, names, job)
}

func (g *DatabaseGate) UpsertMetricsCatalog(ctx context.Context, items []db.MetricCatalogItem) error {
	p := g.provider.Load()
	if p == nil {
		return errDatabaseUnavailable
	}
	return (*p).UpsertMetricsCatalog(ctx, items)
}
