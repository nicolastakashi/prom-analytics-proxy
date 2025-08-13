package inventory

import (
	"context"
	"log/slog"
	"math/rand"
	"strconv"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Syncer struct {
	dbProvider  db.Provider
	promAPI     v1.API
	timeWindow  time.Duration
	interval    time.Duration
	metadataLim string

	syncDuration prometheus.Histogram
	syncSuccess  prometheus.Counter
	syncFailure  prometheus.Counter
}

func NewSyncer(dbp db.Provider, upstream string, cfg *config.Config, reg prometheus.Registerer) (*Syncer, error) {
	client, err := api.NewClient(api.Config{Address: upstream})
	if err != nil {
		return nil, err
	}
	lim := ""
	if cfg != nil && cfg.MetadataLimit > 0 {
		lim = strconv.FormatUint(cfg.MetadataLimit, 10)
	}
	s := &Syncer{
		dbProvider:  dbp,
		promAPI:     v1.NewAPI(client),
		timeWindow:  cfg.Inventory.TimeWindow,
		interval:    cfg.Inventory.SyncInterval,
		metadataLim: lim,
	}

	// Metrics
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	s.syncDuration = promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
		Name:    "inventory_sync_duration_seconds",
		Help:    "Duration of inventory sync runs in seconds",
		Buckets: prometheus.DefBuckets,
	})
	s.syncSuccess = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "inventory_sync_success_total",
		Help: "Total number of successful inventory sync runs",
	})
	s.syncFailure = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "inventory_sync_failure_total",
		Help: "Total number of failed inventory sync runs",
	})

	return s, nil
}

func (s *Syncer) RunLeaderless(ctx context.Context) {
	// For SQLite or when leader election is not required
	s.runLoop(ctx)
}

func (s *Syncer) RunWithLeader(ctx context.Context, isLeader func(context.Context) bool) {
	// Poll for leadership; when leader, run loop until context done or leadership lost
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if isLeader(ctx) {
			s.runLoop(ctx)
		} else {
			// sleep with jitter before retrying leadership
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

func (s *Syncer) runLoop(ctx context.Context) {
	ticker := time.NewTicker(s.interval + time.Duration(rand.Int63n(int64(s.interval/5))))
	defer ticker.Stop()

	// Run immediately on start
	s.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Syncer) runOnce(ctx context.Context) {
	start := time.Now()
	failed := false
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// 1) Fetch metadata
	meta, err := s.promAPI.Metadata(deadline, "", s.metadataLim)
	if err != nil {
		slog.Error("inventory: fetch metadata", "err", err)
		failed = true
		s.syncFailure.Inc()
		s.syncDuration.Observe(time.Since(start).Seconds())
		return
	}

	// 2) Upsert catalog
	items := make([]db.MetricCatalogItem, 0, len(meta))
	for name, infos := range meta {
		if len(infos) == 0 {
			continue
		}
		// Prom returns slice; use the first entry's type/help/unit
		info := infos[0]
		items = append(items, db.MetricCatalogItem{
			Name: name,
			Type: string(info.Type),
			Help: info.Help,
			Unit: info.Unit,
		})
	}
	if err := s.dbProvider.UpsertMetricsCatalog(deadline, items); err != nil {
		slog.Error("inventory: upsert catalog", "err", err)
		failed = true
		s.syncFailure.Inc()
		s.syncDuration.Observe(time.Since(start).Seconds())
		return
	}

	// 3) Refresh usage summary for configured window
	tr := db.TimeRange{From: time.Now().UTC().Add(-s.timeWindow), To: time.Now().UTC()}
	if err := s.dbProvider.RefreshMetricsUsageSummary(deadline, tr); err != nil {
		slog.Error("inventory: refresh summary", "err", err)
		failed = true
		s.syncFailure.Inc()
		s.syncDuration.Observe(time.Since(start).Seconds())
		return
	}

	slog.Info("inventory: sync complete")
	if !failed {
		s.syncSuccess.Inc()
		s.syncDuration.Observe(time.Since(start).Seconds())
	}
}
