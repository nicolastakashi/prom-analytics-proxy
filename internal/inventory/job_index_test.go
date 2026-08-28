package inventory

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every step timeout below is a shipped default, and runTimeout is exactly
// their sum - the minimum NewSyncer accepts. The non-starvation tests that
// follow run inside a synctest bubble, so they can drive that real budget
// on a fake clock instead of a millisecond-scale imitation of it.
const (
	defaultMetadataStepTimeout = 30 * time.Second
	defaultSummaryStepTimeout  = 30 * time.Second
	defaultJobIndexTimeout     = 240 * time.Second
	// nearItsCap is long enough to leave a later step nothing at all if the
	// budgets were ever shared rather than independent.
	nearItsCap = 29 * time.Second
)

// defaultBudgetSyncer builds a Syncer on the shipped default budget with
// every step enabled, so a test only has to say which step runs close to
// its own cap.
func defaultBudgetSyncer(provider db.Provider, promAPI v1.API) *Syncer {
	return &Syncer{
		dbProvider:             provider,
		promAPI:                promAPI,
		timeWindow:             time.Hour,
		metadataSyncEnabled:    true,
		runTimeout:             defaultMetadataStepTimeout + defaultSummaryStepTimeout + defaultJobIndexTimeout,
		metadataStepTimeout:    defaultMetadataStepTimeout,
		summaryStepTimeout:     defaultSummaryStepTimeout,
		jobSyncEnabled:         true,
		jobIndexTimeout:        defaultJobIndexTimeout,
		jobIndexLabelTimeout:   30 * time.Second,
		jobIndexPerJobTimeout:  30 * time.Second,
		jobIndexWorkers:        1,
		syncDuration:           newHistogram(),
		syncSuccess:            newCounter(),
		syncFailure:            newCounter(),
		catalogSummaryMismatch: newCounter(),
	}
}

// oneJobAPI answers a single job's queries quickly, so whatever a test does
// to the steps before job index is the only thing that can starve it.
func oneJobAPI() *fakePromAPI {
	return &fakePromAPI{
		meta:       map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}},
		labelDelay: 5 * time.Second,
		jobs:       []string{"job-a"},
		queryDelay: 10 * time.Second, // label+query need 15s, well inside jobIndexTimeout
	}
}

// jobIndexOnlySyncer builds a Syncer for driving syncJobIndex directly:
// every timeout generous enough not to interfere, so a test that cares
// about one of them sets just that one.
func jobIndexOnlySyncer(provider db.Provider, promAPI v1.API) *Syncer {
	return &Syncer{
		dbProvider:            provider,
		promAPI:               promAPI,
		jobIndexTimeout:       time.Hour,
		jobIndexLabelTimeout:  time.Hour,
		jobIndexPerJobTimeout: time.Hour,
		jobIndexWorkers:       1,
	}
}

// TestSyncer_CatalogRunningCloseToItsOwnBudgetDoesNotStarveJobIndex proves
// job index gets its own full jobIndexTimeout window regardless of how much
// of its own metadataStepTimeout the catalog step actually used - the same
// non-starvation guarantee every independently-budgeted step of a cycle gets.
func TestSyncer_CatalogRunningCloseToItsOwnBudgetDoesNotStarveJobIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &fakeProvider{}
		api := oneJobAPI()
		api.metadataDelay = nearItsCap

		s := defaultBudgetSyncer(provider, api)
		s.runOnce(context.Background())

		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Equal(t, 1, provider.catalogCommits, "metadata catalog step should have committed")
		assert.Equal(t, 1, provider.summaryCalls, "summary refresh should have been attempted")
		assert.Len(t, provider.jobIndexItems, 1,
			"job index should get its own full jobIndexTimeout window (%s) even though the metadata "+
				"step already used %s of its own %s budget; otherwise the job index is never populated",
			defaultJobIndexTimeout, nearItsCap, defaultMetadataStepTimeout)
	})
}

// TestSyncer_SlowSummaryRefreshDoesNotStarveJobIndex is the same guarantee
// for the other step job index runs behind: a summary refresh close to its
// own cap must leave job index its full window too, not just a catalog
// step doing so.
func TestSyncer_SlowSummaryRefreshDoesNotStarveJobIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &fakeProvider{summaryDelay: nearItsCap}

		s := defaultBudgetSyncer(provider, oneJobAPI())
		s.runOnce(context.Background())

		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Equal(t, 1, provider.summaryCalls, "summary refresh should have been attempted")
		assert.Len(t, provider.jobIndexItems, 1,
			"job index should get its own full jobIndexTimeout window even though the summary step "+
				"already used most of its own %s budget", defaultSummaryStepTimeout)
	})
}

// TestSyncer_AllPrecedingStepsNearTheirOwnCapsStillLeaveJobIndexItsFullWindow
// drives the actual worst case NewSyncer's validation protects against:
// every enabled step before job index running close to its own cap at
// once, with runTimeout sized to exactly their validated sum (no slack).
func TestSyncer_AllPrecedingStepsNearTheirOwnCapsStillLeaveJobIndexItsFullWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &fakeProvider{summaryDelay: nearItsCap}
		api := oneJobAPI()
		api.metadataDelay = nearItsCap

		s := defaultBudgetSyncer(provider, api)
		s.runOnce(context.Background())

		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Equal(t, 1, provider.catalogCommits)
		assert.Equal(t, 1, provider.summaryCalls)
		assert.Len(t, provider.jobIndexItems, 1,
			"job index should still get its full jobIndexTimeout window even when every preceding "+
				"step ran close to its own cap, with runTimeout sized to exactly their validated sum")
	})
}

// TestSyncJobIndex_JobIndexTimeoutBoundsTheStepOnItsOwn proves
// jobIndexTimeout itself - not whatever's left of some caller's own
// context - is what bounds the job-index step: called directly with an
// unbounded context, jobIndexTimeout alone must still cut processing off
// partway through. Under a fake clock the cut-off point is exact: two
// 100s jobs fit inside the 240s cap, the third does not.
func TestSyncJobIndex_JobIndexTimeoutBoundsTheStepOnItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &fakeProvider{}
		api := &fakePromAPI{
			jobs:       []string{"job-1", "job-2", "job-3", "job-4", "job-5"},
			queryDelay: 100 * time.Second,
		}

		s := jobIndexOnlySyncer(provider, api)
		s.jobIndexTimeout = defaultJobIndexTimeout // the only limiter this test leaves in play

		_ = s.syncJobIndex(context.Background(), lastHour())

		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Len(t, provider.jobIndexItems, 2,
			"jobIndexTimeout (%s) should cut the step off partway through 5 jobs at %s each, even "+
				"with no outer context bounding it at all", s.jobIndexTimeout, api.queryDelay)
	})
}

// TestSyncJobIndex_NoJobLabelsFound_CompletesWithoutUpserts proves an
// empty job-label result is a normal, non-error outcome (e.g. no series
// have a job label yet), not a failure.
func TestSyncJobIndex_NoJobLabelsFound_CompletesWithoutUpserts(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{jobs: nil}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	require.NoError(t, err)
	assert.Empty(t, provider.jobIndexItems)
}

// TestSyncJobIndex_LabelValuesError_TreatedAsEmptyIndexNotFailure proves a
// job-label-fetch error (e.g. a 404 on a Prometheus without job labels) is
// logged and treated as an empty index, not returned as this cycle's
// failure.
func TestSyncJobIndex_LabelValuesError_TreatedAsEmptyIndexNotFailure(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{labelErr: errors.New("upstream unavailable")}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	require.NoError(t, err)
	assert.Empty(t, provider.jobIndexItems)
}

// TestSyncJobIndex_MinorityOfJobFailures_StillReturnsNilAndCommitsTheRest
// proves the step tolerates a minority of per-job failures - each job is
// independent, so one job's upstream error shouldn't cost the others their
// own results.
func TestSyncJobIndex_MinorityOfJobFailures_StillReturnsNilAndCommitsTheRest(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{
		jobs:         []string{"job-a", "job-b", "job-c", "job-d"},
		queryErr:     errors.New("upstream unavailable"),
		queryErrJobs: map[string]bool{"job-b": true},
	}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	require.NoError(t, err, "1 failure out of 4 jobs is a minority the step should tolerate")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Len(t, provider.jobIndexItems, 3, "the 3 succeeding jobs should still have been committed")
}

// TestSyncJobIndex_HalfOfTheJobsFailing_ReturnsError pins where tolerance
// ends: half the jobs failing is already too many, so the threshold sits
// below half rather than at "most of them".
func TestSyncJobIndex_HalfOfTheJobsFailing_ReturnsError(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{
		jobs:         []string{"job-a", "job-b", "job-c", "job-d"},
		queryErr:     errors.New("upstream unavailable"),
		queryErrJobs: map[string]bool{"job-a": true, "job-b": true},
	}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	assert.Error(t, err, "2 failures out of 4 jobs is already too many to report as success")
}

// TestSyncJobIndex_FansOutAcrossEveryWorker proves the concurrent path
// itself: with more jobs than workers, every job is still queried exactly
// once and every result committed, no matter which worker picked it up.
func TestSyncJobIndex_FansOutAcrossEveryWorker(t *testing.T) {
	jobs := []string{"job-a", "job-b", "job-c", "job-d", "job-e", "job-f", "job-g", "job-h"}
	provider := &fakeProvider{}
	api := &fakePromAPI{jobs: jobs}
	s := jobIndexOnlySyncer(provider, api)
	s.jobIndexWorkers = 4 // fewer workers than jobs, so the channel is genuinely contended

	err := s.syncJobIndex(context.Background(), lastHour())

	require.NoError(t, err)
	api.mu.Lock()
	defer api.mu.Unlock()
	assert.ElementsMatch(t, jobs, api.queriedJobs, "every job must be picked up exactly once across the workers")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Len(t, provider.jobIndexItems, len(jobs))
}

// TestSyncer_JobIndexFailureStillCountsTheCycleAsSuccessful proves the job
// index step's outcome is isolated from the cycle's: it runs last and its
// failure is reported on its own, so it can't turn a committed catalog and
// a refreshed summary into a failed run.
func TestSyncer_JobIndexFailureStillCountsTheCycleAsSuccessful(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{
		meta:         map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}},
		jobs:         []string{"job-a", "job-b", "job-c"},
		queryErr:     errors.New("upstream unavailable"),
		queryErrJobs: map[string]bool{"job-a": true, "job-b": true, "job-c": true},
	}

	s := defaultBudgetSyncer(provider, api)

	s.runOnce(context.Background())

	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Equal(t, 1, provider.catalogCommits)
	assert.Equal(t, 1, provider.summaryCalls)
	assert.Empty(t, provider.jobIndexItems, "every job's query failed, so nothing was indexed")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncSuccess),
		"a failed job index step must not cost the cycle its success")
	assert.Equal(t, float64(0), testutil.ToFloat64(s.syncFailure))
	assert.Equal(t, float64(0), testutil.ToFloat64(s.catalogSummaryMismatch))
}

// TestSyncJobIndex_MajorityOfJobFailures_ReturnsError proves the step
// reports failure once too many jobs failed for the result to be worth
// trusting silently.
func TestSyncJobIndex_MajorityOfJobFailures_ReturnsError(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{
		jobs:         []string{"job-a", "job-b", "job-c"},
		queryErr:     errors.New("upstream unavailable"),
		queryErrJobs: map[string]bool{"job-a": true, "job-b": true},
	}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	assert.Error(t, err, "2 failures out of 3 jobs should be reported, not silently tolerated")
}

// TestSyncJobIndex_SkipsEmptyStringJobLabel proves an empty-string job
// label - a real value Prometheus's job-label endpoint can return - is
// filtered out before ever being queried, rather than producing a
// nonsensical `job=""` lookup.
func TestSyncJobIndex_SkipsEmptyStringJobLabel(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{jobs: []string{"", "job-a"}}
	s := jobIndexOnlySyncer(provider, api)

	err := s.syncJobIndex(context.Background(), lastHour())

	require.NoError(t, err)
	api.mu.Lock()
	defer api.mu.Unlock()
	assert.Equal(t, []string{"job-a"}, api.queriedJobs, "the empty-string label must never reach Query")
}

// TestProcessJob_UpsertFailure_WrapsAndReturnsTheError proves a storage
// failure while committing one job's results is reported back to the
// caller, wrapped with which job it was for - not swallowed.
func TestProcessJob_UpsertFailure_WrapsAndReturnsTheError(t *testing.T) {
	provider := &fakeProvider{
		upsertErr:       errors.New("connection reset"),
		upsertErrForJob: "job-a",
	}
	api := &fakePromAPI{}
	s := jobIndexOnlySyncer(provider, api)

	err := s.processJob(context.Background(), "job-a", lastHour(), 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert job job-a")
	assert.ErrorContains(t, err, "connection reset")
}
