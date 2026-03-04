package inventory

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/common/model"
)

type Syncer struct {
	dbProvider          db.Provider
	promAPI             v1.API
	timeWindow          time.Duration
	interval            time.Duration
	metadataLim         string
	metadataSyncEnabled bool

	runTimeout            time.Duration
	metadataStepTimeout   time.Duration
	summaryStepTimeout    time.Duration
	jobIndexLabelTimeout  time.Duration
	jobIndexPerJobTimeout time.Duration

	jobIndexWorkers int

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
		dbProvider:            dbp,
		promAPI:               v1.NewAPI(client),
		timeWindow:            cfg.Inventory.TimeWindow,
		interval:              cfg.Inventory.SyncInterval,
		metadataLim:           lim,
		metadataSyncEnabled:   cfg.Inventory.MetadataSyncEnabled,
		runTimeout:            cfg.Inventory.RunTimeout,
		metadataStepTimeout:   cfg.Inventory.MetadataStepTimeout,
		summaryStepTimeout:    cfg.Inventory.SummaryStepTimeout,
		jobIndexLabelTimeout:  cfg.Inventory.JobIndexLabelTimeout,
		jobIndexPerJobTimeout: cfg.Inventory.JobIndexPerJobTimeout,
		jobIndexWorkers:       cfg.Inventory.JobIndexWorkers,
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

	return s, nil
}

func (s *Syncer) RunLeaderless(ctx context.Context) {
	s.runLoop(ctx)
}

func (s *Syncer) RunWithLeader(ctx context.Context, isLeader func(context.Context) bool) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if isLeader(ctx) {
			s.runLoop(ctx)
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

func (s *Syncer) runOnce(ctx context.Context) {
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, s.runTimeout)
	defer cancel()

	if err := s.syncCatalogAndSummary(runCtx); err != nil {
		s.syncFailure.Inc()
		s.syncDuration.Observe(time.Since(start).Seconds())
		return
	}

	tr := db.TimeRange{From: time.Now().UTC().Add(-s.timeWindow), To: time.Now().UTC()}
	if err := s.syncJobIndex(runCtx, tr); err != nil {
		slog.Error("inventory: job index", "err", err)
	}

	slog.Info("inventory: sync complete")
	s.syncSuccess.Inc()
	s.syncDuration.Observe(time.Since(start).Seconds())
}

func (s *Syncer) syncCatalogAndSummary(ctx context.Context) error {
	if s.metadataSyncEnabled {
		if err := s.syncMetadataCatalog(ctx); err != nil {
			return err
		}
	} else {
		slog.Info("inventory: metadata sync disabled, skipping catalog population")
	}

	sumCtx, cancelSum := context.WithTimeout(ctx, s.summaryStepTimeout)
	defer cancelSum()
	tr := db.TimeRange{From: time.Now().UTC().Add(-s.timeWindow), To: time.Now().UTC()}
	if err := s.dbProvider.RefreshMetricsUsageSummary(sumCtx, tr); err != nil {
		slog.Error("inventory: refresh summary", "err", err)
		return err
	}
	return nil
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

func (s *Syncer) syncJobIndex(ctx context.Context, tr db.TimeRange) error {
	labelCtx, cancelLabels := context.WithTimeout(ctx, s.jobIndexLabelTimeout)
	defer cancelLabels()
	jobs, _, err := s.promAPI.LabelValues(labelCtx, "job", []string{}, tr.From, tr.To)
	if err != nil {
		// Handle 404 gracefully - it means no series with job label exist or endpoint not supported
		slog.Warn("failed to fetch job label values", "err", err, "msg", "job index will be empty - this is normal if no series have job labels")
		return nil
	}

	if len(jobs) == 0 {
		slog.Debug("no job labels found in time range", "from", tr.From, "to", tr.To)
		return nil
	}

	slog.Info("syncing job index", "jobs_found", len(jobs), "workers", s.jobIndexWorkers, "time_range", tr)

	jobChan := make(chan string, len(jobs))
	errorChan := make(chan error, len(jobs))

	var wg sync.WaitGroup
	for i := 0; i < s.jobIndexWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobChan {
				err := s.processJob(ctx, job, tr, workerID)
				errorChan <- err
			}
		}(i)
	}

	jobsProcessed := 0
	for _, jobLabel := range jobs {
		job := string(jobLabel)
		if job != "" {
			jobChan <- job
			jobsProcessed++
		}
	}
	close(jobChan)

	go func() {
		wg.Wait()
		close(errorChan)
	}()

	var successCount, failureCount int
	for err := range errorChan {
		if err != nil {
			failureCount++
		} else {
			successCount++
		}
	}

	slog.Info("job index sync complete",
		"jobs_processed", jobsProcessed,
		"successful", successCount,
		"failed", failureCount)

	if failureCount > 0 && failureCount > successCount/2 {
		return fmt.Errorf("job index sync failed: %d failures out of %d jobs", failureCount, jobsProcessed)
	}

	return nil
}

func (s *Syncer) processJob(ctx context.Context, job string, tr db.TimeRange, workerID int) error {
	jobCtx, cancelJob := context.WithTimeout(ctx, s.jobIndexPerJobTimeout)
	defer cancelJob()

	query := fmt.Sprintf(`group({job="%s", __name__=~".+"}) by (__name__)`, job)
	result, _, err := s.promAPI.Query(jobCtx, query, tr.To)
	if err != nil {
		slog.Warn("failed to query metrics for job", "job", job, "worker", workerID, "query", query, "err", err)
		return err
	}

	metricNames := make(map[string]struct{})

	// Extract metric names from the query result
	switch v := result.(type) {
	case model.Vector:
		for _, sample := range v {
			if metricName, ok := sample.Metric["__name__"]; ok {
				metricNames[string(metricName)] = struct{}{}
			}
		}
	default:
		slog.Debug("unexpected query result type", "job", job, "worker", workerID, "type", fmt.Sprintf("%T", result))
	}

	var items []db.MetricJobIndexItem
	for name := range metricNames {
		items = append(items, db.MetricJobIndexItem{Name: name, Job: job})
	}

	if len(items) > 0 {
		if err := s.dbProvider.UpsertMetricsJobIndex(ctx, items); err != nil {
			slog.Error("failed to upsert metrics for job", "job", job, "worker", workerID, "metrics", len(items), "err", err)
			return fmt.Errorf("upsert job %s: %w", job, err)
		}
		slog.Debug("processed job", "job", job, "worker", workerID, "metrics", len(items))
	}

	return nil
}
