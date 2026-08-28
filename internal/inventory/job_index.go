package inventory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/common/model"
)

// syncJobIndex populates job_index: which metrics each Prometheus job
// produces, used to answer job-scoped "which of this job's metrics are
// unused" queries. Bounded as a whole by jobIndexTimeout - independent of
// every other RunOnce step (see docs/jobs.md) - since its own duration
// otherwise scales with however many jobs Prometheus reports at run time,
// something no per-call timeout alone can bound.
func (s *Syncer) syncJobIndex(ctx context.Context, tr db.TimeRange) error {
	jobIndexCtx, cancel := context.WithTimeout(ctx, s.jobIndexTimeout)
	defer cancel()

	labelCtx, cancelLabels := context.WithTimeout(jobIndexCtx, s.jobIndexLabelTimeout)
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
				err := s.processJob(jobIndexCtx, job, tr, workerID)
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
