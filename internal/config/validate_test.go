package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validInventory and validRetention are the shipped defaults, which every
// case below starts from so it only has to state the one field it's about.
func validInventory() InventoryConfig {
	c := DefaultConfig.Inventory
	c.JobSyncEnabled = true // so the job-index step's own values are in play
	return c
}

func validRetention() RetentionConfig {
	c := DefaultConfig.Retention
	c.Enabled = true
	return c
}

func TestInventoryConfig_Validate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tweak    func(*InventoryConfig)
		wantErr  string   // empty means the config must be accepted
		wantErrs []string // every message the one error must carry
	}{
		{name: "shipped defaults", tweak: func(*InventoryConfig) {}},
		{
			name:    "sync_interval zero",
			tweak:   func(c *InventoryConfig) { c.SyncInterval = 0 },
			wantErr: "inventory.sync_interval must be positive",
		},
		{
			name:    "sync_interval negative",
			tweak:   func(c *InventoryConfig) { c.SyncInterval = -time.Minute },
			wantErr: "inventory.sync_interval must be positive",
		},
		{
			// A window ending before it starts silently finds no usage at
			// all, which reads as "every metric is unused".
			name:    "time_window zero",
			tweak:   func(c *InventoryConfig) { c.TimeWindow = 0 },
			wantErr: "inventory.time_window must be positive",
		},
		{
			name:    "run_timeout zero",
			tweak:   func(c *InventoryConfig) { c.RunTimeout = 0 },
			wantErr: "inventory.run_timeout must be positive",
		},
		{
			name:    "run_timeout negative",
			tweak:   func(c *InventoryConfig) { c.RunTimeout = -time.Second },
			wantErr: "inventory.run_timeout must be positive",
		},
		{
			// Never gated, so it's checked whatever else is switched off.
			name: "summary_step_timeout zero with every other step disabled",
			tweak: func(c *InventoryConfig) {
				c.SummaryStepTimeout = 0
				c.MetadataSyncEnabled, c.JobSyncEnabled = false, false
			},
			wantErr: "inventory.summary_step_timeout must be positive",
		},
		{
			name:    "metadata_step_timeout negative while its step is enabled",
			tweak:   func(c *InventoryConfig) { c.MetadataStepTimeout = -5 * time.Second },
			wantErr: "inventory.metadata_step_timeout must be positive",
		},
		{
			name: "metadata_step_timeout negative while its step is disabled",
			tweak: func(c *InventoryConfig) {
				c.MetadataSyncEnabled = false
				c.MetadataStepTimeout = -5 * time.Second
			},
		},
		{
			// The one that would otherwise index nothing in silence: a
			// label fetch on an already-expired context fails, and that
			// error is treated as "no series carry a job label".
			name:    "job_index_label_timeout zero while its step is enabled",
			tweak:   func(c *InventoryConfig) { c.JobIndexLabelTimeout = 0 },
			wantErr: "inventory.job_index_label_timeout must be positive",
		},
		{
			name:    "job_index_per_job_timeout negative while its step is enabled",
			tweak:   func(c *InventoryConfig) { c.JobIndexPerJobTimeout = -time.Second },
			wantErr: "inventory.job_index_per_job_timeout must be positive",
		},
		{
			name:    "job_index_workers zero while its step is enabled",
			tweak:   func(c *InventoryConfig) { c.JobIndexWorkers = 0 },
			wantErr: "inventory.job_index_workers must be positive",
		},
		{
			name: "every job-index value unusable while its step is disabled",
			tweak: func(c *InventoryConfig) {
				c.JobSyncEnabled = false
				c.JobIndexLabelTimeout, c.JobIndexPerJobTimeout = 0, -time.Second
				c.JobIndexWorkers = 0
			},
		},
		{
			// Every bad value at once: an operator with several typos should
			// see all of them, not one per restart.
			name: "several unusable values",
			tweak: func(c *InventoryConfig) {
				c.SyncInterval, c.RunTimeout, c.SummaryStepTimeout = 0, 0, 0
			},
			wantErrs: []string{
				"inventory.sync_interval must be positive",
				"inventory.run_timeout must be positive",
				"inventory.summary_step_timeout must be positive",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validInventory()
			tc.tweak(&cfg)

			err := cfg.Validate()

			if tc.wantErr == "" && tc.wantErrs == nil {
				assert.NoError(t, err, "a disabled step's own values can't affect anything, so they don't have to be usable")
				return
			}
			require.Error(t, err)
			if tc.wantErr != "" {
				assert.Contains(t, err.Error(), tc.wantErr)
			}
			for _, want := range tc.wantErrs {
				assert.Contains(t, err.Error(), want, "every unusable value has to be reported, not just the first")
			}
		})
	}
}

func TestRetentionConfig_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tweak   func(*RetentionConfig)
		wantErr string
	}{
		{name: "shipped defaults", tweak: func(*RetentionConfig) {}},
		{
			name:    "interval zero",
			tweak:   func(c *RetentionConfig) { c.Interval = 0 },
			wantErr: "retention.interval must be positive",
		},
		{
			name:    "run_timeout negative",
			tweak:   func(c *RetentionConfig) { c.RunTimeout = -time.Minute },
			wantErr: "retention.run_timeout must be positive",
		},
		{
			name:    "queries_max_age zero",
			tweak:   func(c *RetentionConfig) { c.QueriesMaxAge = 0 },
			wantErr: "retention.queries_max_age must be positive",
		},
		{
			// Checked whether or not the worker runs: an unusable value is
			// an operator's typo either way, and enabling the job later
			// shouldn't be what surfaces it.
			name: "queries_max_age zero while the job is disabled",
			tweak: func(c *RetentionConfig) {
				c.Enabled = false
				c.QueriesMaxAge = 0
			},
			wantErr: "retention.queries_max_age must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validRetention()
			tc.tweak(&cfg)

			err := cfg.Validate()

			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestConfig_Validate_ReportsEverySectionItRuns proves the aggregate reaches
// both jobs' sections and reports them together, so startup can't pass on a
// config either of them would reject, and an operator sees every problem in
// one go.
func TestConfig_Validate_ReportsEverySectionItRuns(t *testing.T) {
	cfg := *DefaultConfig
	cfg.Inventory.Enabled, cfg.Retention.Enabled = true, true
	cfg.Inventory.SyncInterval = 0
	cfg.Retention.QueriesMaxAge = 0

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inventory.sync_interval")
	assert.Contains(t, err.Error(), "retention.queries_max_age", "both sections' problems must arrive together")

	var nilCfg *Config
	require.ErrorContains(t, nilCfg.Validate(), "config is required")
}

// TestConfig_Validate_SkipsSectionsThisProcessWontRun proves a job switched
// off is not a reason to refuse startup: its values are never read, and the
// ingester command reads neither section, so a config carrying a stale
// disabled block still starts.
func TestConfig_Validate_SkipsSectionsThisProcessWontRun(t *testing.T) {
	cfg := *DefaultConfig
	cfg.Inventory.Enabled, cfg.Retention.Enabled = false, false
	cfg.Inventory.SyncInterval = 0
	cfg.Retention.QueriesMaxAge = 0

	assert.NoError(t, cfg.Validate())
}

// TestConfig_Validate_AcceptsTheShippedDefaults guards the defaults against
// their own range rules: every default a job reads has to be usable as
// shipped, with or without the steps that are gated off by default.
func TestConfig_Validate_AcceptsTheShippedDefaults(t *testing.T) {
	require.NoError(t, DefaultConfig.Validate())

	withJobSync := *DefaultConfig
	withJobSync.Inventory.JobSyncEnabled = true
	require.NoError(t, withJobSync.Validate())
}
