package retention

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Worker struct {
	dbProvider    db.Provider
	interval      time.Duration
	runTimeout    time.Duration
	queriesMaxAge time.Duration

	runDuration *prometheus.HistogramVec
}

func NewWorker(store db.Provider, cfg *config.Config, reg prometheus.Registerer) (*Worker, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	if cfg.Retention.Interval <= 0 {
		return nil, fmt.Errorf("retention.interval must be positive (got: %v)", cfg.Retention.Interval)
	}

	if cfg.Retention.RunTimeout <= 0 {
		return nil, fmt.Errorf("retention.run_timeout must be positive (got: %v)", cfg.Retention.RunTimeout)
	}

	if cfg.Retention.QueriesMaxAge <= 0 {
		return nil, fmt.Errorf("retention.queries_max_age must be positive (got: %v)", cfg.Retention.QueriesMaxAge)
	}

	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	w := &Worker{
		dbProvider:    store,
		interval:      cfg.Retention.Interval,
		runTimeout:    cfg.Retention.RunTimeout,
		queriesMaxAge: cfg.Retention.QueriesMaxAge,
	}

	w.runDuration = promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
		Name:    "retention_run_duration_seconds",
		Help:    "Duration of retention runs in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"status"})

	return w, nil
}

func (w *Worker) RunLeaderless(ctx context.Context) {
	w.runLoop(ctx)
}

func (w *Worker) RunWithLeader(ctx context.Context, isLeader func(context.Context) bool) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if isLeader(ctx) {
			w.runLoop(ctx)
		} else {
			j := time.Duration(rand.Int63n(int64(backoff)))
			select {
			case <-time.After(backoff + j):
			case <-ctx.Done():
				return
			}
			if backoff < 10*time.Second {
				backoff *= 2
			}
		}
	}
}

func (w *Worker) runLoop(ctx context.Context) {
	// Calculate jitter as 20% of interval, with a minimum of 1 nanosecond to avoid panic
	jitterBase := w.interval / 5
	if jitterBase == 0 {
		jitterBase = 1
	}
	jitter := time.Duration(rand.Int63n(int64(jitterBase)))
	ticker := time.NewTicker(w.interval + jitter)
	defer ticker.Stop()

	w.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, w.runTimeout)
	defer cancel()

	// Skip deletion if queriesMaxAge is zero or negative (defensive check)
	if w.queriesMaxAge <= 0 {
		return
	}

	cutoff := time.Now().UTC().Add(-w.queriesMaxAge)
	deleted, err := w.dbProvider.DeleteQueriesBefore(runCtx, cutoff)
	if err != nil {
		slog.Error("retention: failed to delete old queries", "err", err, "cutoff", cutoff)
		w.runDuration.WithLabelValues("failure").Observe(time.Since(start).Seconds())
		return
	}

	slog.Info("retention: cleanup complete", "deleted", deleted, "cutoff", cutoff)
	w.runDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
}
