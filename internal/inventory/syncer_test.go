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

// wantStepOverrunAllowance is stepOverrunAllowance's shipped value, spelled
// out rather than read from the constant: a test reading it couldn't tell a
// changed allowance from a changed formula.
const wantStepOverrunAllowance = 10 * time.Second

// TestSyncer_SlowMetadataStepDoesNotPermanentlyStarveSummaryRefresh reproduces
// https://github.com/nicolastakashi/prom-analytics-proxy/issues/572: a
// metadata step that (legitimately, within its own per-step timeout) runs
// close to that timeout must still leave the summary step its own full,
// independent window - not whatever happens to be left of a shared budget -
// or newly-catalogued metrics never get their placeholder summary rows
// refreshed with real usage data. The timeouts are the shipped defaults and
// runTimeout is exactly their sum - tighter than the floor NewSyncer would
// settle on, deliberately, since the guarantee has to hold with no overrun
// allowance at all to spend. The synctest bubble's clock is what makes those
// durations free to use.
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
			runTimeout:              metadataStepTimeout + summaryStepTimeout, // the step budgets alone, no allowance to spend
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

// TestSyncer_EachBudgetBoundsItsOwnScope proves the nesting every other
// budget guarantee rests on: a cycle derives one cycleCtx from its caller,
// bounded by runTimeout, and each step nests its own window inside that, so
// whichever of the two is tighter is what cuts the work off. Every other
// timing test in this package leaves only the innermost budget in play, so
// a step re-parented to an unbounded context - or one whose WithTimeout
// became a WithCancel - would go unnoticed by all of them while quietly
// putting back the starvation independent step budgets exist to prevent.
func TestSyncer_EachBudgetBoundsItsOwnScope(t *testing.T) {
	const generous = time.Hour // long enough that it can never be the limiter

	for _, tc := range []struct {
		name                string
		runTimeout          time.Duration
		metadataStepTimeout time.Duration
		summaryStepTimeout  time.Duration
		metadataDelay       time.Duration
		summaryDelay        time.Duration
		wantElapsed         time.Duration
	}{
		{
			// Tighter than either step's own budget, so the cycle is what
			// ends the run: a step never gets to outlive the cycle holding it.
			name:                "run_timeout bounds the cycle",
			runTimeout:          60 * time.Second,
			metadataStepTimeout: generous,
			summaryStepTimeout:  generous,
			metadataDelay:       generous,
			wantElapsed:         60 * time.Second,
		},
		{
			name:                "metadata_step_timeout bounds the catalog step",
			runTimeout:          generous,
			metadataStepTimeout: 30 * time.Second,
			summaryStepTimeout:  generous,
			metadataDelay:       generous,
			wantElapsed:         30 * time.Second,
		},
		{
			name:                "summary_step_timeout bounds the refresh",
			runTimeout:          generous,
			metadataStepTimeout: generous,
			summaryStepTimeout:  30 * time.Second,
			summaryDelay:        generous,
			wantElapsed:         30 * time.Second,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := &fakeProvider{summaryDelay: tc.summaryDelay}
				api := &fakePromAPI{
					metadataDelay: tc.metadataDelay,
					meta:          map[string][]v1.Metadata{"up": {{Type: "gauge", Help: "up metric"}}},
				}
				s := &Syncer{
					dbProvider:             provider,
					promAPI:                api,
					timeWindow:             time.Hour,
					metadataSyncEnabled:    true,
					runTimeout:             tc.runTimeout,
					metadataStepTimeout:    tc.metadataStepTimeout,
					summaryStepTimeout:     tc.summaryStepTimeout,
					syncDuration:           newHistogram(),
					syncSuccess:            newCounter(),
					syncFailure:            newCounter(),
					catalogSummaryMismatch: newCounter(),
				}

				start := time.Now()
				s.RunOnce(context.Background())

				assert.Equal(t, tc.wantElapsed, time.Since(start),
					"the tightest budget in scope must be what ends the work")
			})
		})
	}
}

// TestNewSyncer_DisabledJobSyncIsSettledPastRatherThanWidened proves a
// disabled step's budgets are left exactly as configured: the step never
// runs, so neither its own unusable value gets widened to fit sub-budgets
// it will never spend, nor does the cycle get widened to contain it.
func TestNewSyncer_DisabledJobSyncIsSettledPastRatherThanWidened(t *testing.T) {
	cfg := baseInventoryConfig()
	cfg.Inventory.JobSyncEnabled = false
	cfg.Inventory.JobIndexTimeout = time.Millisecond // far too short for its own sub-budgets
	cfg.Inventory.RunTimeout = cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout + 2*wantStepOverrunAllowance

	s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

	require.NoError(t, err)
	assert.Equal(t, time.Millisecond, s.jobIndexTimeout,
		"a disabled step's budget must not be widened to fit sub-budgets it will never spend")
	assert.Equal(t, cfg.Inventory.RunTimeout, s.runTimeout,
		"a disabled step must not count toward the cycle it never runs in")
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
// distinct value so a test can tell at a glance which one a given assertion
// is about, and RunTimeout sized exactly to the floor NewSyncer's settling
// accepts: the step sum plus one overrun allowance per enabled step. Nothing
// ever waits on these durations - every test built on this config only
// constructs a Syncer - but they are second-scale all the same, so the
// allowance stays the small share of a cycle it is meant to be rather than
// dwarfing the budgets it protects.
func baseInventoryConfig() *config.Config {
	return &config.Config{
		Inventory: config.InventoryConfig{
			Enabled:               true,
			MetadataSyncEnabled:   true,
			JobSyncEnabled:        true,
			SyncInterval:          10 * time.Minute,
			TimeWindow:            time.Hour,
			RunTimeout:            230 * time.Second, // = 100 + 50 + 50, plus 10 per step
			MetadataStepTimeout:   100 * time.Second,
			SummaryStepTimeout:    50 * time.Second,
			JobIndexTimeout:       50 * time.Second,
			JobIndexLabelTimeout:  10 * time.Second,
			JobIndexPerJobTimeout: 10 * time.Second,
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
			name:  "run_timeout exactly the settled floor",
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
// own settling: run_timeout's default is exactly the floor - the default
// step timeouts plus their overrun allowance - so lowering one default, or
// raising another, makes the out-of-the-box config one NewSyncer has to
// widen (and warn about) before it can run, with or without the job index
// step, the only one gated off by default.
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
	cfg.Inventory.RunTimeout = cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout +
		cfg.Inventory.JobIndexTimeout + 3*wantStepOverrunAllowance

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
	// 200s is the bare step sum: a cycle covering every step in full and
	// nothing else still has to be widened to make room for the allowance,
	// or a step's own overrun comes out of the next step's window.
	for _, configured := range []time.Duration{200 * time.Second, 199 * time.Second, time.Millisecond} {
		cfg := baseInventoryConfig()
		wantFloor := cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout +
			cfg.Inventory.JobIndexTimeout + 3*wantStepOverrunAllowance
		cfg.Inventory.RunTimeout = configured

		s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

		require.NoError(t, err, "a positive run_timeout below the floor should be widened, not rejected (%s)", configured)
		assert.Equal(t, wantFloor, s.runTimeout,
			"run_timeout %s covers none of the steps; the cycle must run on their floor instead", configured)
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
	cfg.Inventory.RunTimeout = cfg.Inventory.MetadataStepTimeout + cfg.Inventory.SummaryStepTimeout +
		cfg.Inventory.JobIndexTimeout + 3*wantStepOverrunAllowance

	s, err := NewSyncer(&fakeProvider{}, "http://upstream", cfg, prometheus.NewRegistry())

	require.NoError(t, err)
	wantJobIndex := cfg.Inventory.JobIndexLabelTimeout + cfg.Inventory.JobIndexPerJobTimeout
	assert.Equal(t, wantJobIndex, s.jobIndexTimeout, "the step must be able to index at least one job")
	assert.Equal(t, cfg.Inventory.MetadataStepTimeout+cfg.Inventory.SummaryStepTimeout+wantJobIndex+3*wantStepOverrunAllowance, s.runTimeout,
		"the cycle must cover the widened step, not the narrower one it was sized for")
}

// TestNewSyncer_RegistersItsMetricsUnderTheirPublishedNames asserts the
// names alerts and dashboards query for, spelled out as literals: read off
// the Syncer's own fields instead, a rename would move production and
// expectation together and leave the suite green while every external
// consumer broke.
func TestNewSyncer_RegistersItsMetricsUnderTheirPublishedNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, err := NewSyncer(&fakeProvider{}, "http://upstream", baseInventoryConfig(), reg)
	require.NoError(t, err)

	families, err := reg.Gather()
	require.NoError(t, err)
	names := make([]string, 0, len(families))
	for _, f := range families {
		names = append(names, f.GetName())
	}

	assert.ElementsMatch(t, []string{
		"inventory_sync_duration_seconds",
		"inventory_sync_success_total",
		"inventory_sync_failure_total",
		"inventory_catalog_summary_mismatch_total",
		"inventory_job_index_failure_total",
	}, names)
}

func TestNewSyncer_RejectsNilConfig(t *testing.T) {
	s, err := NewSyncer(&fakeProvider{}, "http://upstream", nil, prometheus.NewRegistry())

	assert.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "config is required")
}
