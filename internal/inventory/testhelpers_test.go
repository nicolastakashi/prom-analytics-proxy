package inventory

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
)

// The two collaborators every test in this package drives a Syncer through,
// faked once here rather than per concern: a Syncer's catalog, summary and
// job-index work all go through the same Prometheus API and the same storage
// provider, so a fake per test file would be two things to keep in step
// with db.Provider and v1.API instead of one.

// fakePromAPI is a minimal v1.API stand-in. Embedding the (nil) interface
// means any method a test's code path doesn't use panics if called - which
// is what a test wants, rather than a silent zero value. Every call can be
// made slow (to exercise a step's timeout) or made to fail.
type fakePromAPI struct {
	v1.API

	metadataDelay time.Duration
	metadataErr   error
	meta          map[string][]v1.Metadata

	labelDelay time.Duration
	jobs       []string
	labelErr   error

	queryDelay time.Duration
	// queryErr, if set, is returned instead of a normal result for every
	// job named in queryErrJobs - every other job still succeeds.
	queryErr     error
	queryErrJobs map[string]bool

	mu          sync.Mutex
	queriedJobs []string // jobs Query was actually called for, in call order
}

func (f *fakePromAPI) Metadata(ctx context.Context, _ string, _ string) (map[string][]v1.Metadata, error) {
	if f.metadataErr != nil {
		return nil, f.metadataErr
	}
	select {
	case <-time.After(f.metadataDelay):
		return f.meta, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakePromAPI) LabelValues(ctx context.Context, _ string, _ []string, _, _ time.Time, _ ...v1.Option) (model.LabelValues, v1.Warnings, error) {
	select {
	case <-time.After(f.labelDelay):
		if f.labelErr != nil {
			return nil, nil, f.labelErr
		}
		values := make(model.LabelValues, len(f.jobs))
		for i, j := range f.jobs {
			values[i] = model.LabelValue(j)
		}
		return values, nil, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

// jobFromQueryRe pulls the job name back out of the PromQL processJob
// builds, so this fake can respond per-job without a separate lookup keyed
// some other way.
var jobFromQueryRe = regexp.MustCompile(`job="([^"]*)"`)

func (f *fakePromAPI) Query(ctx context.Context, query string, _ time.Time, _ ...v1.Option) (model.Value, v1.Warnings, error) {
	select {
	case <-time.After(f.queryDelay):
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	job := ""
	if m := jobFromQueryRe.FindStringSubmatch(query); len(m) == 2 {
		job = m[1]
	}
	f.mu.Lock()
	f.queriedJobs = append(f.queriedJobs, job)
	f.mu.Unlock()

	if f.queryErr != nil && f.queryErrJobs[job] {
		return nil, nil, f.queryErr
	}
	return model.Vector{{Metric: model.Metric{"__name__": "up"}}}, nil, nil
}

// fakeProvider records what a cycle actually committed - catalog, summary
// and job index - so a test can tell a step that ran from one that was
// skipped or cut short.
type fakeProvider struct {
	db.Provider

	mu sync.Mutex

	catalogCommits int

	summaryDelay     time.Duration // how long a real refresh would take
	summaryCalls     int
	summarySucceeded bool

	jobIndexItems []db.MetricJobIndexItem
	// upsertErr, if set, is returned instead of succeeding for the one job
	// named upsertErrForJob - every other job's upsert still succeeds.
	upsertErr       error
	upsertErrForJob string
}

func (f *fakeProvider) UpsertMetricsCatalog(_ context.Context, _ []db.MetricCatalogItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogCommits++
	return nil
}

func (f *fakeProvider) RefreshMetricsUsageSummary(ctx context.Context, _ db.TimeRange) error {
	f.mu.Lock()
	f.summaryCalls++
	f.mu.Unlock()

	select {
	case <-time.After(f.summaryDelay):
		f.mu.Lock()
		f.summarySucceeded = true
		f.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeProvider) UpsertMetricsJobIndex(_ context.Context, items []db.MetricJobIndexItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil && len(items) > 0 && items[0].Job == f.upsertErrForJob {
		return f.upsertErr
	}
	f.jobIndexItems = append(f.jobIndexItems, items...)
	return nil
}

// lastHour is the lookback window these tests pass to syncJobIndex; the
// fakes never read it, so any window will do.
func lastHour() db.TimeRange {
	return db.TimeRange{From: time.Now().Add(-time.Hour), To: time.Now()}
}

func newCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Name: "test_counter"})
}

func newHistogram() prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{Name: "test_histogram"})
}
