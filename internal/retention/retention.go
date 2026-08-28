package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Worker struct {
	dbProvider    db.Provider
	runTimeout    time.Duration
	queriesMaxAge time.Duration

	runDuration *prometheus.HistogramVec
}

func NewWorker(store db.Provider, cfg *config.Config, reg prometheus.Registerer) (*Worker, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	// Range checks live with the schema (see config.RetentionConfig.Validate);
	// startup runs them before anything acts on the config, and calling them
	// again here is what holds for a caller that never went through startup.
	if err := cfg.Retention.Validate(); err != nil {
		return nil, err
	}

	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	w := &Worker{
		dbProvider:    store,
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

// RunOnce runs exactly one cleanup cycle, bounded by whatever ctx it's
// given - scheduling and leadership are entirely the caller's concern;
// RunOnce itself doesn't loop or know who's calling it.
func (w *Worker) RunOnce(ctx context.Context) {
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
