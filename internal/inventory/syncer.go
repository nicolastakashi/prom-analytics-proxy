package inventory

import (
	"context"
	"fmt"
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
	dbProvider              db.Provider
	promAPI                 v1.API
	timeWindow              time.Duration
	interval                time.Duration
	metadataLim             string
	metadataSyncEnabled     bool
	metadataMetricsNameOnly bool

	runTimeout            time.Duration
	metadataStepTimeout   time.Duration
	summaryStepTimeout    time.Duration
	jobSyncEnabled        bool
	jobIndexTimeout       time.Duration
	jobIndexLabelTimeout  time.Duration
	jobIndexPerJobTimeout time.Duration

	jobIndexWorkers int

	syncDuration           prometheus.Histogram
	syncSuccess            prometheus.Counter
	syncFailure            prometheus.Counter
	catalogSummaryMismatch prometheus.Counter
}

// A cycle's enabled steps are independent, non-overlapping budgets, so its
// worst case is all of them in full and RunTimeout has to cover that sum;
// the same holds one level down, where JobIndexTimeout has to cover its own
// label fetch plus at least one job's query. Where a container is too small
// for what it must hold, the two settled* functions below widen it and warn
// naming the value to fix, rather than refusing to start: the operator asked
// for those step budgets, and running with them is closer to their intent
// than not running at all.
// settledRunTimeout reads JobIndexTimeout as given, so a caller wanting both
// settled has to settle that one first - as NewSyncer does.
func settledRunTimeout(cfg config.InventoryConfig) time.Duration {
	stepSum := cfg.SummaryStepTimeout
	if cfg.MetadataSyncEnabled {
		stepSum += cfg.MetadataStepTimeout
	}
	if cfg.JobSyncEnabled {
		stepSum += cfg.JobIndexTimeout
	}
	if stepSum <= cfg.RunTimeout {
		return cfg.RunTimeout
	}
	slog.Warn("inventory: run_timeout is shorter than the steps one cycle must run; raising it - set it to at least this sum in your config to silence this",
		"configured", cfg.RunTimeout,
		"using", stepSum,
		"metadata_step_timeout", cfg.MetadataStepTimeout,
		"metadata_sync_enabled", cfg.MetadataSyncEnabled,
		"summary_step_timeout", cfg.SummaryStepTimeout,
		"job_index_timeout", cfg.JobIndexTimeout,
		"job_sync_enabled", cfg.JobSyncEnabled)
	return stepSum
}

// settledJobIndexTimeout's widening feeds the step sum above, so it can
// widen the cycle that has to contain it.
func settledJobIndexTimeout(cfg config.InventoryConfig) time.Duration {
	min := cfg.JobIndexLabelTimeout + cfg.JobIndexPerJobTimeout
	if !cfg.JobSyncEnabled || cfg.JobIndexTimeout >= min {
		return cfg.JobIndexTimeout
	}
	slog.Warn("inventory: job_index_timeout is too short to fetch job labels and process a single job; raising it - set it to at least this sum in your config to silence this",
		"configured", cfg.JobIndexTimeout,
		"using", min,
		"job_index_label_timeout", cfg.JobIndexLabelTimeout,
		"job_index_per_job_timeout", cfg.JobIndexPerJobTimeout)
	return min
}

// NewSyncer builds a Syncer whose cycles run under settled timeouts (see
// above) and rejects the config values nothing could settle - a non-positive
// interval, no workers to fan out to.
//
// One caveat that outlives construction: the settled sum bounds the
// configured worst case, not the wall clock. A step whose work overruns its
// own context spends budget a later step was counting on, and a RunTimeout
// equal to the sum has no slack to absorb that. It is still enough that
// RunOnce can nest every step under one shared context - see docs/jobs.md,
// and https://github.com/nicolastakashi/prom-analytics-proxy/issues/572 for
// the starvation this generalizes away.
func NewSyncer(dbp db.Provider, upstream string, cfg *config.Config, reg prometheus.Registerer) (*Syncer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	client, err := api.NewClient(api.Config{Address: upstream})
	if err != nil {
		return nil, err
	}

	// Range checks live with the schema (see config.InventoryConfig.Validate);
	// startup runs them before anything acts on the config, and calling them
	// again here is what holds for a caller that never went through startup.
	if err := cfg.Inventory.Validate(); err != nil {
		return nil, err
	}

	// A copy, not cfg itself: a constructor that rewrote its caller's config
	// would surprise anyone sharing one.
	inv := cfg.Inventory
	inv.JobIndexTimeout = settledJobIndexTimeout(inv)
	inv.RunTimeout = settledRunTimeout(inv)

	lim := ""
	if cfg.MetadataLimit > 0 {
		lim = strconv.FormatUint(cfg.MetadataLimit, 10)
	}
	s := &Syncer{
		dbProvider:              dbp,
		promAPI:                 v1.NewAPI(client),
		timeWindow:              cfg.Inventory.TimeWindow,
		interval:                cfg.Inventory.SyncInterval,
		metadataLim:             lim,
		metadataSyncEnabled:     cfg.Inventory.MetadataSyncEnabled,
		metadataMetricsNameOnly: cfg.Inventory.MetadataMetricsNameOnly,
		runTimeout:              inv.RunTimeout,
		metadataStepTimeout:     cfg.Inventory.MetadataStepTimeout,
		summaryStepTimeout:      cfg.Inventory.SummaryStepTimeout,
		jobSyncEnabled:          cfg.Inventory.JobSyncEnabled,
		jobIndexTimeout:         inv.JobIndexTimeout,
		jobIndexLabelTimeout:    cfg.Inventory.JobIndexLabelTimeout,
		jobIndexPerJobTimeout:   cfg.Inventory.JobIndexPerJobTimeout,
		jobIndexWorkers:         cfg.Inventory.JobIndexWorkers,
	}

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
	s.catalogSummaryMismatch = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "inventory_catalog_summary_mismatch_total",
		Help: "Total number of runs where the metadata catalog was committed but the usage-summary refresh failed, stranding the affected metrics at placeholder values",
	})

	return s, nil
}

func (s *Syncer) RunLeaderless(ctx context.Context) {
	s.runLoop(ctx)
}

func (s *Syncer) runLoop(ctx context.Context) {
	ticker := time.NewTicker(s.interval + time.Duration(rand.Int63n(int64(s.interval/5))))
	defer ticker.Stop()

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

// runOnce runs exactly one sync cycle. cycleCtx is the single deadline every
// step below derives from, bounded by runTimeout. Each step still nests its
// own context.WithTimeout inside it and gets that window in full; the
// settled sum is what makes that safe.
func (s *Syncer) runOnce(ctx context.Context) {
	start := time.Now()
	cycleCtx, cancel := context.WithTimeout(ctx, s.runTimeout)
	defer cancel()

	catalogCommitted, err := s.syncCatalogAndSummary(cycleCtx)
	if err != nil {
		s.syncFailure.Inc()
		if catalogCommitted {
			// The metadata catalog step already committed (or was skipped)
			// this run, so any newly-catalogued metrics now sit at their
			// default placeholder summary values (is_unused=true, zero
			// counts) until a future run successfully refreshes them.
			// Surface this as its own signal so it isn't buried in the
			// generic failure counter.
			s.catalogSummaryMismatch.Inc()
			slog.Warn("inventory: catalog committed but usage-summary refresh failed this run; affected metrics remain at placeholder values until the next scheduled sync",
				"err", err)
		}
		s.syncDuration.Observe(time.Since(start).Seconds())
		return
	}

	if s.jobSyncEnabled {
		tr := db.TimeRange{From: time.Now().UTC().Add(-s.timeWindow), To: time.Now().UTC()}
		if err := s.syncJobIndex(cycleCtx, tr); err != nil {
			slog.Error("inventory: job index", "err", err)
		}
	}

	slog.Info("inventory: sync complete")
	s.syncSuccess.Inc()
	s.syncDuration.Observe(time.Since(start).Seconds())
}

// syncCatalogAndSummary runs the metadata-catalog sync followed by the
// usage-summary refresh. It reports whether the catalog step committed (or
// was intentionally skipped) so the caller can tell an ordinary failure
// apart from the partial-failure case where the catalog moved forward but
// the summary refresh didn't.
func (s *Syncer) syncCatalogAndSummary(cycleCtx context.Context) (catalogCommitted bool, err error) {
	if s.metadataSyncEnabled {
		if s.metadataMetricsNameOnly {
			if err := s.syncMetadataCatalogFromMetricNames(cycleCtx); err != nil {
				return false, err
			}
		} else {
			if err := s.syncMetadataCatalog(cycleCtx); err != nil {
				return false, err
			}
		}
	} else {
		slog.Info("inventory: metadata sync disabled, skipping catalog population")
	}

	sumCtx, cancelSum := context.WithTimeout(cycleCtx, s.summaryStepTimeout)
	defer cancelSum()
	tr := db.TimeRange{From: time.Now().UTC().Add(-s.timeWindow), To: time.Now().UTC()}
	if err := s.dbProvider.RefreshMetricsUsageSummary(sumCtx, tr); err != nil {
		slog.Error("inventory: refresh summary", "err", err)
		return true, err
	}
	return true, nil
}

func (s *Syncer) syncMetadataCatalog(ctx context.Context) error {
	metaCtx, cancelMeta := context.WithTimeout(ctx, s.metadataStepTimeout)
	defer cancelMeta()
	meta, err := s.promAPI.Metadata(metaCtx, "", s.metadataLim)
	if err != nil {
		slog.Error("inventory: fetch metadata", "err", err)
		return err
	}

	items := make([]db.MetricCatalogItem, 0, len(meta)*2)
	for name, infos := range meta {
		if len(infos) == 0 {
			continue
		}
		info := infos[0]
		metricType := string(info.Type)
		switch metricType {
		case "histogram":
			items = append(items,
				db.MetricCatalogItem{Name: name + "_bucket", Type: "histogram_bucket", Help: info.Help + " (histogram buckets)", Unit: info.Unit},
				db.MetricCatalogItem{Name: name + "_count", Type: "histogram_count", Help: info.Help + " (histogram count)", Unit: ""},
				db.MetricCatalogItem{Name: name + "_sum", Type: "histogram_sum", Help: info.Help + " (histogram sum)", Unit: info.Unit},
			)
		case "summary":
			items = append(items,
				db.MetricCatalogItem{Name: name, Type: metricType, Help: info.Help, Unit: info.Unit},
				db.MetricCatalogItem{Name: name + "_count", Type: "summary_count", Help: info.Help + " (summary count)", Unit: ""},
				db.MetricCatalogItem{Name: name + "_sum", Type: "summary_sum", Help: info.Help + " (summary sum)", Unit: info.Unit},
			)
		default:
			items = append(items, db.MetricCatalogItem{Name: name, Type: metricType, Help: info.Help, Unit: info.Unit})
		}
	}
	if err := s.dbProvider.UpsertMetricsCatalog(metaCtx, items); err != nil {
		slog.Error("inventory: upsert catalog", "err", err)
		return err
	}
	return nil
}

func (s *Syncer) syncMetadataCatalogFromMetricNames(ctx context.Context) error {
	metaCtx, cancelMeta := context.WithTimeout(ctx, s.metadataStepTimeout)
	defer cancelMeta()
	now := time.Now()
	labelValues, _, err := s.promAPI.LabelValues(metaCtx, "__name__", nil, now.Add(-s.timeWindow), now)

	if err != nil {
		slog.Error("inventory: fetch label values", "err", err)
		return err
	}

	items := make([]db.MetricCatalogItem, 0, len(labelValues))
	for _, labelValue := range labelValues {
		if !labelValue.IsValid() {
			continue
		}

		items = append(items, db.MetricCatalogItem{Name: string(labelValue)})
	}
	if err := s.dbProvider.UpsertMetricsCatalog(metaCtx, items); err != nil {
		slog.Error("inventory: upsert catalog", "err", err)
		return err
	}
	return nil
}
