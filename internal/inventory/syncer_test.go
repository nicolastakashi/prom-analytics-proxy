package inventory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// fakeMetadataAPI is a minimal v1.API stand-in. Embedding the (nil) interface
// means any method we don't override panics if called - fine, since these
// tests only ever exercise Metadata.
type fakeMetadataAPI struct {
	v1.API
	delay time.Duration
	meta  map[string][]v1.Metadata
	err   error
}

func (f *fakeMetadataAPI) Metadata(ctx context.Context, _ string, _ string) (map[string][]v1.Metadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	select {
	case <-time.After(f.delay):
		return f.meta, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fakeInventoryProvider is a minimal db.Provider stand-in that only tracks
// the two calls the syncer's catalog+summary path makes.
type fakeInventoryProvider struct {
	db.Provider

	mu sync.Mutex

	catalogCommits int

	summaryCalls     int
	summaryWork      time.Duration // how long a real refresh would take
	summarySucceeded bool
}

func (f *fakeInventoryProvider) UpsertMetricsCatalog(_ context.Context, _ []db.MetricCatalogItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogCommits++
	return nil
}

func (f *fakeInventoryProvider) RefreshMetricsUsageSummary(ctx context.Context, _ db.TimeRange) error {
	f.mu.Lock()
	f.summaryCalls++
	f.mu.Unlock()

	select {
	case <-time.After(f.summaryWork):
		f.mu.Lock()
		f.summarySucceeded = true
		f.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Name: "test_counter"})
}

func newHistogram() prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{Name: "test_histogram"})
}

// TestSyncer_SlowMetadataStepDoesNotPermanentlyStarveSummaryRefresh reproduces
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/572: the
// metadata-catalog step and the usage-summary refresh share a single
// run-timeout budget. A metadata step that (legitimately, within its own
// per-step timeout) takes long enough leaves the summary step less time than
// its own configured summaryStepTimeout - or none at all - every single run,
// so the newly-catalogued metrics never get their placeholder summary rows
// refreshed with real usage data. This is a deterministic race, not a flaky
// one: the same shortfall recurs on every run, so nothing about "just retry
// on the next scheduled sync" fixes it.
func TestSyncer_SlowMetadataStepDoesNotPermanentlyStarveSummaryRefresh(t *testing.T) {
	provider := &fakeInventoryProvider{
		summaryWork: 40 * time.Millisecond,
	}
	api := &fakeMetadataAPI{
		delay: 60 * time.Millisecond,
		meta:  map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}},
	}

	s := &Syncer{
		dbProvider:              provider,
		promAPI:                 api,
		timeWindow:              time.Hour,
		metadataSyncEnabled:     true,
		metadataMetricsNameOnly: false,
		runTimeout:              80 * time.Millisecond, // shared budget: barely more than the metadata step alone needs
		metadataStepTimeout:     time.Second,           // generous on its own; runTimeout is the real limiter
		summaryStepTimeout:      50 * time.Millisecond, // > summaryWork, so summary should always have enough time of its own
		syncDuration:            newHistogram(),
		syncSuccess:             newCounter(),
		syncFailure:             newCounter(),
		catalogSummaryMismatch:  newCounter(),
	}

	// Run twice: a fix must not merely get lucky once, it must hold up on
	// every run since the metadata step's duration here is fixed and always
	// eats the same share of any shared budget.
	for i := 0; i < 2; i++ {
		provider.mu.Lock()
		provider.summarySucceeded = false
		provider.mu.Unlock()

		s.runOnce(context.Background())

		provider.mu.Lock()
		commits, calls, succeeded := provider.catalogCommits, provider.summaryCalls, provider.summarySucceeded
		provider.mu.Unlock()

		assert.Equalf(t, i+1, commits, "run %d: metadata catalog step should have committed", i+1)
		assert.Equalf(t, i+1, calls, "run %d: summary refresh should have been attempted", i+1)
		assert.Truef(t, succeeded,
			"run %d: usage-summary refresh should complete within its own summaryStepTimeout "+
				"(%s) even though the metadata step already used most of the shared run timeout "+
				"(%s); otherwise newly-catalogued metrics are permanently stranded at placeholder "+
				"values", i+1, s.summaryStepTimeout, s.runTimeout)
	}
}

// TestSyncer_SummaryFailureAfterCatalogCommitEmitsMismatchMetric asserts the
// partial-failure case from the acceptance criteria is distinguishable: when
// the catalog step commits but the summary refresh still fails (e.g. the DB
// itself is unavailable, independent of any timeout budget), that's reported
// via the dedicated catalogSummaryMismatch counter and a distinct log line,
// not folded into the generic failure counter alone.
func TestSyncer_SummaryFailureAfterCatalogCommitEmitsMismatchMetric(t *testing.T) {
	provider := &fakeInventoryProvider{
		summaryWork: time.Hour, // never completes within the step timeout
	}
	api := &fakeMetadataAPI{meta: map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}}}

	s := &Syncer{
		dbProvider:             provider,
		promAPI:                api,
		timeWindow:             time.Hour,
		metadataSyncEnabled:    true,
		runTimeout:             time.Second,
		metadataStepTimeout:    time.Second,
		summaryStepTimeout:     10 * time.Millisecond,
		syncDuration:           newHistogram(),
		syncSuccess:            newCounter(),
		syncFailure:            newCounter(),
		catalogSummaryMismatch: newCounter(),
	}

	s.runOnce(context.Background())

	assert.Equal(t, 1, provider.catalogCommits, "catalog step should have committed")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncFailure), "generic failure counter should still increment")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.catalogSummaryMismatch),
		"catalog-committed-but-summary-failed should be counted separately from the generic failure")
	assert.Equal(t, float64(0), testutil.ToFloat64(s.syncSuccess))
}

// TestSyncer_MetadataFailureDoesNotEmitMismatchMetric asserts an ordinary
// metadata-step failure (nothing committed) is NOT misreported as the
// catalog/summary partial-failure case.
func TestSyncer_MetadataFailureDoesNotEmitMismatchMetric(t *testing.T) {
	provider := &fakeInventoryProvider{}
	api := &fakeMetadataAPI{err: errors.New("upstream unavailable")}

	s := &Syncer{
		dbProvider:             provider,
		promAPI:                api,
		timeWindow:             time.Hour,
		metadataSyncEnabled:    true,
		runTimeout:             time.Second,
		metadataStepTimeout:    time.Second,
		summaryStepTimeout:     time.Second,
		syncDuration:           newHistogram(),
		syncSuccess:            newCounter(),
		syncFailure:            newCounter(),
		catalogSummaryMismatch: newCounter(),
	}

	s.runOnce(context.Background())

	assert.Equal(t, 0, provider.catalogCommits, "catalog step should not have committed")
	assert.Equal(t, 0, provider.summaryCalls, "summary refresh should not even be attempted")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncFailure))
	assert.Equal(t, float64(0), testutil.ToFloat64(s.catalogSummaryMismatch),
		"a plain metadata-step failure is not the catalog/summary partial-failure case")
}
