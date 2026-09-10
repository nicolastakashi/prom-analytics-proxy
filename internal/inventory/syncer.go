package inventory

import (
	"context"
	"fmt"
	"log/slog"
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
	jobIndexFailure        prometheus.Counter
}

// stepOverrunAllowance is what a cycle budget has to carry, per enabled
// step, on top of the step budgets themselves. A step reaching its own
// deadline still has to notice the cancellation and unwind, and cancelling
// an in-flight Prometheus or Postgres round trip costs a round trip of its
// own, so a step's wall-clock duration runs over the budget it was given by
// a little. Cover that here and the overrun is spent out of the cycle's own
// allowance; leave it uncovered and it is spent out of the next step's
// window, which is the truncation independent step budgets exist to prevent.
// An allowance rather than a measurement, and deliberately well above what
// abandoning a round trip should cost: over-provisioning it only raises the
// floor RunTimeout has to clear, where under-provisioning it puts the
// truncation back.
const stepOverrunAllowance = 10 * time.Second

// SettleConfig fixes cfg's inventory timeouts at the values a cycle will
// actually run under, and is what makes run_timeout mean the same thing to
// every reader: startup calls it once, before anything reads the config, so
// whatever declares this job's cycle budget can read the config rather than
// asking separately. Idempotent, which is what lets NewSyncer settle again
// for callers that never went through startup.
func SettleConfig(cfg *config.Config) {
	cfg.Inventory.JobIndexTimeout = settledJobIndexTimeout(cfg.Inventory)
	cfg.Inventory.RunTimeout = settledRunTimeout(cfg.Inventory)
}

// A cycle's enabled steps are independent, non-overlapping budgets, so its
// worst case is all of them in full plus their overrun allowance, and
// RunTimeout has to cover that floor; the same holds one level down, where
// JobIndexTimeout has to cover its own label fetch plus at least one job's
// query. Where a container is too small for what it must hold, the two
// settled* functions below widen it to its floor and warn naming the value
// to fix, rather than refusing to start: the operator asked for those step
// budgets, and running with them is closer to their intent than not running
// at all.
// settledRunTimeout reads JobIndexTimeout as given, so a caller wanting both
// settled has to settle that one first - as SettleConfig does.
func settledRunTimeout(cfg config.InventoryConfig) time.Duration {
	// The summary step is never gated by a flag, so it always counts.
	stepSum, steps := cfg.SummaryStepTimeout, 1
	if cfg.MetadataSyncEnabled {
		stepSum += cfg.MetadataStepTimeout
		steps++
	}
	if cfg.JobSyncEnabled {
		stepSum += cfg.JobIndexTimeout
		steps++
	}
	allowance := time.Duration(steps) * stepOverrunAllowance
	floor := stepSum + allowance
	if floor <= cfg.RunTimeout {
		return cfg.RunTimeout
	}
	slog.Warn("inventory: run_timeout is shorter than the steps one cycle must run plus their overrun allowance; raising it - set it to at least this floor in your config to silence this",
		"configured", cfg.RunTimeout,
		"using", floor,
		"step_sum", stepSum,
		"overrun_allowance", allowance,
		"metadata_step_timeout", cfg.MetadataStepTimeout,
		"metadata_sync_enabled", cfg.MetadataSyncEnabled,
		"summary_step_timeout", cfg.SummaryStepTimeout,
		"job_index_timeout", cfg.JobIndexTimeout,
		"job_sync_enabled", cfg.JobSyncEnabled)
	return floor
}

// settledJobIndexTimeout's widening feeds the step sum above, so it can
// widen the cycle that has to contain it.
func settledJobIndexTimeout(cfg config.InventoryConfig) time.Duration {
	floor := cfg.JobIndexLabelTimeout + cfg.JobIndexPerJobTimeout
	if !cfg.JobSyncEnabled || cfg.JobIndexTimeout >= floor {
		return cfg.JobIndexTimeout
	}
	slog.Warn("inventory: job_index_timeout is too short to fetch job labels and process a single job; raising it - set it to at least this sum in your config to silence this",
		"configured", cfg.JobIndexTimeout,
		"using", floor,
		"job_index_label_timeout", cfg.JobIndexLabelTimeout,
		"job_index_per_job_timeout", cfg.JobIndexPerJobTimeout)
	return floor
}

// NewSyncer builds a Syncer whose cycles run under settled timeouts (see
// above) and rejects the config values nothing could settle - a non-positive
// interval, no workers to fan out to. Settling is what lets RunOnce nest
// every step under one context bounded by RunTimeout while each step still
// gets its own window in full - see docs/jobs.md, and
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/572 for the
// starvation this generalizes away.
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
	// would surprise anyone sharing one. Every field below reads from this
	// copy rather than cfg.Inventory, so a field that gains a settling rule
	// later gets it whether or not whoever adds it notices the two sources.
	inv := cfg.Inventory
	inv.JobIndexTimeout = settledJobIndexTimeout(inv)
	inv.RunTimeout = settledRunTimeout(inv)

	// What cycles actually run on, where the startup config dump prints what
	// was configured - the two differ whenever settling had to widen one.
	// Only the values settling can change, so nothing here reads as a second
	// source for the rest.
	slog.Info("inventory: settled cycle budget",
		"run_timeout", inv.RunTimeout,
		"job_index_timeout", inv.JobIndexTimeout)

	lim := ""
	if cfg.MetadataLimit > 0 {
		lim = strconv.FormatUint(cfg.MetadataLimit, 10)
	}
	s := &Syncer{
		dbProvider:              dbp,
		promAPI:                 v1.NewAPI(client),
		timeWindow:              inv.TimeWindow,
		metadataLim:             lim,
		metadataSyncEnabled:     inv.MetadataSyncEnabled,
		metadataMetricsNameOnly: inv.MetadataMetricsNameOnly,
		runTimeout:              inv.RunTimeout,
		metadataStepTimeout:     inv.MetadataStepTimeout,
		summaryStepTimeout:      inv.SummaryStepTimeout,
		jobSyncEnabled:          inv.JobSyncEnabled,
		jobIndexTimeout:         inv.JobIndexTimeout,
		jobIndexLabelTimeout:    inv.JobIndexLabelTimeout,
		jobIndexPerJobTimeout:   inv.JobIndexPerJobTimeout,
		jobIndexWorkers:         inv.JobIndexWorkers,
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
	s.jobIndexFailure = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "inventory_job_index_failure_total",
		Help: "Total number of runs where the job-index step failed, leaving the index stale for the jobs it could not reach while the run itself still counted as a success",
	})

	return s, nil
}

// RunOnce runs exactly one sync cycle, bounded by whatever ctx it's given -
// scheduling and leadership are entirely the caller's concern; RunOnce
// itself doesn't loop or know who's calling it.
//
// cycleCtx is the single deadline every step below derives from, bounded
// by runTimeout. Each step still nests its own context.WithTimeout inside
// it and gets that window in full; NewSyncer's validation of runTimeout
// against the step sum is what makes that safe.
func (s *Syncer) RunOnce(ctx context.Context) {
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
			// The run still counts as a success: the catalog and the summary
			// both landed, and this step's outcome is separable from theirs
			// (see docs/jobs.md). Counted on its own so an index left stale
			// for most of the jobs is queryable, rather than a log line
			// nothing alerts on.
			s.jobIndexFailure.Inc()
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
