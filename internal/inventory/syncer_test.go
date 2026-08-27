package inventory

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncer_SlowMetadataStepDoesNotPermanentlyStarveSummaryRefresh reproduces
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/572: a
// metadata step that (legitimately, within its own per-step timeout) runs
// close to that timeout must still leave the summary step its own full,
// independent window - not whatever happens to be left of a shared budget -
// or newly-catalogued metrics never get their placeholder summary rows
// refreshed with real usage data. The timeouts are the shipped defaults and
// runTimeout is exactly their sum, so this pins the validated sum as
// actually sufficient at the budget operators really run - the synctest
// bubble's clock is what makes those durations free to use.
func TestSyncer_SlowMetadataStepDoesNotPermanentlyStarveSummaryRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			metadataStepTimeout = 30 * time.Second
			summaryStepTimeout  = 30 * time.Second
		)
		provider := &fakeProvider{
			summaryDelay: 25 * time.Second,
		}
		api := &fakePromAPI{
			metadataDelay: 29 * time.Second, // close to metadataStepTimeout's own cap, but still within it
			meta:          map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}},
		}

		s := &Syncer{
			dbProvider:              provider,
			promAPI:                 api,
			timeWindow:              time.Hour,
			metadataSyncEnabled:     true,
			metadataMetricsNameOnly: false,
			runTimeout:              metadataStepTimeout + summaryStepTimeout, // exactly the validated minimum, no slack
			metadataStepTimeout:     metadataStepTimeout,
			summaryStepTimeout:      summaryStepTimeout, // > summaryDelay, so summary should always have enough time of its own
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

			s.RunOnce(context.Background())

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
	})
}

// TestSyncer_SummaryFailureAfterCatalogCommitEmitsMismatchMetric asserts the
// partial-failure case from the acceptance criteria is distinguishable: when
// the catalog step commits but the summary refresh still fails (e.g. the DB
// itself is unavailable, independent of any timeout budget), that's reported
// via the dedicated catalogSummaryMismatch counter and a distinct log line,
// not folded into the generic failure counter alone.
func TestSyncer_SummaryFailureAfterCatalogCommitEmitsMismatchMetric(t *testing.T) {
	provider := &fakeProvider{
		summaryDelay: time.Hour, // never completes within the step timeout
	}
	api := &fakePromAPI{meta: map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}}}

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

	s.RunOnce(context.Background())

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
	provider := &fakeProvider{}
	api := &fakePromAPI{metadataErr: errors.New("upstream unavailable")}

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

	s.RunOnce(context.Background())

	assert.Equal(t, 0, provider.catalogCommits, "catalog step should not have committed")
	assert.Equal(t, 0, provider.summaryCalls, "summary refresh should not even be attempted")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncFailure))
	assert.Equal(t, float64(0), testutil.ToFloat64(s.catalogSummaryMismatch),
		"a plain metadata-step failure is not the catalog/summary partial-failure case")
}

// TestSyncer_DisabledMetadataSyncStillRefreshesTheSummary asserts the
// catalog step being switched off skips it rather than failing the cycle:
// the summary refresh still runs, and the cycle still counts as a success.
func TestSyncer_DisabledMetadataSyncStillRefreshesTheSummary(t *testing.T) {
	provider := &fakeProvider{}
	api := &fakePromAPI{metadataErr: errors.New("must never be called")}

	s := &Syncer{
		dbProvider:             provider,
		promAPI:                api,
		timeWindow:             time.Hour,
		metadataSyncEnabled:    false,
		runTimeout:             time.Second,
		metadataStepTimeout:    time.Second,
		summaryStepTimeout:     time.Second,
		syncDuration:           newHistogram(),
		syncSuccess:            newCounter(),
		syncFailure:            newCounter(),
		catalogSummaryMismatch: newCounter(),
	}

	s.RunOnce(context.Background())

	assert.Equal(t, 0, provider.catalogCommits, "the catalog step is skipped entirely in this mode")
	assert.Equal(t, 1, provider.summaryCalls, "the summary refresh is never gated by metadata_sync_enabled")
	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncSuccess))
	assert.Equal(t, float64(0), testutil.ToFloat64(s.syncFailure))
}

// TestSyncer_SummaryFailureWithMetadataSyncDisabledStillEmitsMismatchMetric
// asserts a skipped catalog step counts as committed for mismatch
// reporting: in this mode the OTLP ingester populates the catalog instead,
// so a failed summary refresh strands its newly-ingested metrics at
// placeholder values exactly as it would after this job's own catalog
// write.
func TestSyncer_SummaryFailureWithMetadataSyncDisabledStillEmitsMismatchMetric(t *testing.T) {
	provider := &fakeProvider{
		summaryDelay: time.Hour, // never completes within the step timeout
	}
	api := &fakePromAPI{metadataErr: errors.New("must never be called")}

	s := &Syncer{
		dbProvider:             provider,
		promAPI:                api,
		timeWindow:             time.Hour,
		metadataSyncEnabled:    false,
		runTimeout:             time.Second,
		metadataStepTimeout:    time.Second,
		summaryStepTimeout:     10 * time.Millisecond,
		syncDuration:           newHistogram(),
		syncSuccess:            newCounter(),
		syncFailure:            newCounter(),
		catalogSummaryMismatch: newCounter(),
	}

	s.RunOnce(context.Background())

	assert.Equal(t, float64(1), testutil.ToFloat64(s.syncFailure))
	assert.Equal(t, float64(1), testutil.ToFloat64(s.catalogSummaryMismatch))
}

// baseInventoryConfig has every step enabled, each step timeout set to a
// distinct value so a test can tell at a glance which one a given
// assertion is about, and RunTimeout sized exactly to their sum - the
// minimum NewSyncer's validation accepts.
func baseInventoryConfig() *config.Config {
	return &config.Config{
		Inventory: config.InventoryConfig{
			Enabled:               true,
			MetadataSyncEnabled:   true,
			JobSyncEnabled:        true,
			SyncInterval:          10 * time.Minute,
			TimeWindow:            time.Hour,
			RunTimeout:            200 * time.Millisecond, // = 100 + 50 + 50
			MetadataStepTimeout:   100 * time.Millisecond,
			SummaryStepTimeout:    50 * time.Millisecond,
			JobIndexTimeout:       50 * time.Millisecond,
			JobIndexLabelTimeout:  10 * time.Millisecond,
			JobIndexPerJobTimeout: 10 * time.Millisecond,
			JobIndexWorkers:       1,
		},
	}
}

// TestNewSyncer_ConfigValidation covers what NewSyncer's construction gate
// does with a config, as opposed to what each value has to be on its own -
// those range rules live with the schema, and config's own tests cover them
// field by field. What matters here is that NewSyncer surfaces them at all,
// and that the rules it owns itself hold: which steps count toward the
// cycle, and which values a disabled step gets to ignore.
func TestNewSyncer_ConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tweak   func(*config.InventoryConfig)
		wantErr string // empty means the config must be accepted
	}{
		{
			name:  "run_timeout exactly the step sum",
			tweak: func(*config.InventoryConfig) {},
		},
		{
			name:    "sync_interval zero",
			tweak:   func(c *config.InventoryConfig) { c.SyncInterval = 0 },
			wantErr: "sync_interval must be positive",
		},
		{
			name:    "job_index_workers zero",
			tweak:   func(c *config.InventoryConfig) { c.JobIndexWorkers = 0 },
			wantErr: "job_index_workers must be positive",
		},
		{
			// A disabled step never runs, so none of its own values have to
			// be usable: this one value is at once too small for the step's
			// own sub-budgets and, if it still counted toward the sum, too
			// large for run_timeout - so every check that would reject it
			// has to be skipped for this config to construct.
			name: "job sync disabled, its every value unusable",
			tweak: func(c *config.InventoryConfig) {
				c.JobSyncEnabled = false
				c.JobIndexTimeout = time.Millisecond
				c.JobIndexWorkers = 0
				c.RunTimeout = c.MetadataStepTimeout + c.SummaryStepTimeout
			},
		},
		{
			name: "metadata sync disabled, its step timeout unusable",
			tweak: func(c *config.InventoryConfig) {
				c.MetadataSyncEnabled = false
				c.MetadataStepTimeout = time.Hour // would blow the budget if it still counted
				c.RunTimeout = c.SummaryStepTimeout + c.JobIndexTimeout
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseInventoryConfig()
			tc.tweak(&cfg.Inventory)

			s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

			if tc.wantErr == "" {
				assert.NoError(t, err)
				assert.NotNil(t, s)
				return
			}
			assert.Error(t, err)
			assert.Nil(t, s)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestNewSyncer_AcceptsTheShippedDefaults guards the defaults against their
// own validation: run_timeout's default is exactly the sum of the default
// step timeouts, so lowering one default, or raising another, makes the
// out-of-the-box config NewSyncer has to widen (and warn about) before it
// can run - with or without the job index step, the only one gated off by
// default.
func TestNewSyncer_AcceptsTheShippedDefaults(t *testing.T) {
	for _, jobSync := range []bool{false, true} {
		cfg := *config.DefaultConfig
		cfg.Inventory.JobSyncEnabled = jobSync

		s, err := NewSyncer(&fakeProvider{}, "http://upstream", &cfg, prometheus.NewRegistry())
		require.NoError(t, err, "shipped defaults with job_sync_enabled=%v must construct", jobSync)
		assert.Equal(t, cfg.Inventory.RunTimeout, s.runTimeout, "the shipped defaults need no widening")
	}
}

// TestNewSyncer_AcceptsJobIndexTimeoutExactlyEqualToItsOwnSubBudgets pins
// the smallest job-index budget that needs no widening: enough for the
// label fetch plus one job's query.
func TestNewSyncer_AcceptsJobIndexTimeoutExactlyEqualToItsOwnSubBudgets(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.JobIndexTimeout = cfg.Inventory.JobIndexLabelTimeout + cfg.Inventory.JobIndexPerJobTimeout
	cfg.Inventory.RunTimeout = cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout + cfg.Inventory.JobIndexTimeout

	s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())
	require.NoError(t, err)
	assert.Equal(t, cfg.Inventory.JobIndexTimeout, s.jobIndexTimeout, "a budget that already fits must be left alone")
	assert.Equal(t, cfg.Inventory.RunTimeout, s.runTimeout, "a cycle budget that already fits must be left alone")
}

// TestNewSyncer_WidensRunTimeoutToCoverItsSteps proves a cycle budget too
// short for the steps it must run is raised to fit rather than refused:
// the operator gets the step budgets they configured, and a warning naming
// what to fix, instead of a process that won't start.
func TestNewSyncer_WidensRunTimeoutToCoverItsSteps(t *testing.T) {
	for _, configured := range []time.Duration{199 * time.Millisecond, time.Millisecond} {
		cfg := baseInventoryConfig()
		stepSum := cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout + cfg.Inventory.JobIndexTimeout
		cfg.Inventory.RunTimeout = configured

		s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

		require.NoError(t, err, "a positive run_timeout below the sum should be widened, not rejected (%s)", configured)
		assert.Equal(t, stepSum, s.runTimeout,
			"run_timeout %s covers none of the steps; the cycle must run on their sum instead", configured)
	}
}

// TestNewSyncer_WidensJobIndexTimeoutToCoverItsSubBudgets is the same
// guarantee one level down, and pins that widening the inner budget also
// widens the cycle that has to contain it.
func TestNewSyncer_WidensJobIndexTimeoutToCoverItsSubBudgets(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.JobIndexTimeout = cfg.Inventory.JobIndexLabelTimeout // enough for the labels, nothing left for a job
	// A cycle sized to the steps as configured, so widening the step below
	// leaves it too small and it has to be widened in turn.
	cfg.Inventory.RunTimeout = cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout + cfg.Inventory.JobIndexTimeout

	s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

	require.NoError(t, err)
	wantJobIndex := cfg.Inventory.JobIndexLabelTimeout + cfg.Inventory.JobIndexPerJobTimeout
	assert.Equal(t, wantJobIndex, s.jobIndexTimeout, "the step must be able to index at least one job")
	assert.Equal(t, cfg.Inventory.MetadataStepTimeout+cfg.Inventory.SummaryStepTimeout+wantJobIndex, s.runTimeout,
		"the cycle must cover the widened step, not the narrower one it was sized for")
}

func TestNewSyncer_RejectsNilConfig(t *testing.T) {
	s, err := NewSyncer(&fakeProvider{}, "http://upstream", nil, prometheus.NewRegistry())

	assert.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "config is required")
}

// TestSettleConfig_WritesTheBoundCyclesRunUnder proves settling is what makes
// run_timeout mean one thing to every reader: it rewrites the config in
// place, and a syncer built from the settled config runs on exactly those
// values.
func TestSettleConfig_WritesTheBoundCyclesRunUnder(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.JobIndexTimeout = cfg.Inventory.JobIndexLabelTimeout // too short for a single job
	cfg.Inventory.RunTimeout = time.Millisecond                        // too short for the steps

	SettleConfig(cfg)

	wantJobIndex := cfg.Inventory.JobIndexLabelTimeout + cfg.Inventory.JobIndexPerJobTimeout
	assert.Equal(t, wantJobIndex, cfg.Inventory.JobIndexTimeout, "the inner budget must be widened first")
	assert.Equal(t, cfg.Inventory.MetadataStepTimeout+cfg.Inventory.SummaryStepTimeout+wantJobIndex, cfg.Inventory.RunTimeout,
		"the cycle must be widened to cover the steps, including the step just widened")

	s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())
	require.NoError(t, err)
	assert.Equal(t, cfg.Inventory.RunTimeout, s.runTimeout, "a syncer built from a settled config must run on the settled values")
	assert.Equal(t, cfg.Inventory.JobIndexTimeout, s.jobIndexTimeout)
}

// TestSettleConfig_IsIdempotent proves settling twice is settling once - what
// lets NewSyncer settle again for a caller that never went through startup,
// without changing anything startup already decided.
func TestSettleConfig_IsIdempotent(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.RunTimeout = time.Millisecond

	SettleConfig(cfg)
	once := cfg.Inventory
	SettleConfig(cfg)

	assert.Equal(t, once, cfg.Inventory, "a settled config must survive settling unchanged")
}

// TestSettleConfig_LeavesAFittingConfigAlone proves settling only ever widens
// what cannot fit: a config an operator sized correctly must come back
// exactly as written.
func TestSettleConfig_LeavesAFittingConfigAlone(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.RunTimeout = time.Hour // generous: covers every step several times over
	before := cfg.Inventory

	SettleConfig(cfg)

	assert.Equal(t, before, cfg.Inventory)
}
