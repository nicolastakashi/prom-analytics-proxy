package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFlagSet(configFile *string) *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(configFile, "config-file", "", "")
	fs.StringVar(&DefaultConfig.Upstream.URL, "upstream", "", "")
	fs.StringVar(&DefaultConfig.Database.Provider, "database-provider", "", "")
	fs.IntVar(&DefaultConfig.Insert.BatchSize, "insert-batch-size", 10, "")
	fs.IntVar(&DefaultConfig.Insert.BufferSize, "insert-buffer-size", 100, "")
	return fs
}

func TestEnvVarName(t *testing.T) {
	assert.Equal(t, "PROM_ANALYTICS_PROXY_UPSTREAM", EnvVarName("upstream"))
	assert.Equal(t, "PROM_ANALYTICS_PROXY_DATABASE_PROVIDER", EnvVarName("database-provider"))
	assert.Equal(t, "PROM_ANALYTICS_PROXY_INVENTORY_SYNC_INTERVAL", EnvVarName("inventory-sync-interval"))
}

func TestAnnotateUsage(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("database-provider", "", "The database provider.")

	AnnotateUsage(fs)

	assert.Equal(t, "The database provider. (env PROM_ANALYTICS_PROXY_DATABASE_PROVIDER)", fs.Lookup("database-provider").Usage)
}

func TestApplyEnv(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	upstream := fs.String("upstream", "", "")
	interval := fs.Duration("inventory-sync-interval", time.Minute, "")
	enabled := fs.Bool("inventory-enabled", false, "")

	t.Setenv("PROM_ANALYTICS_PROXY_UPSTREAM", "http://prometheus:9090")
	t.Setenv("PROM_ANALYTICS_PROXY_INVENTORY_SYNC_INTERVAL", "15m")
	t.Setenv("PROM_ANALYTICS_PROXY_INVENTORY_ENABLED", "true")

	require.NoError(t, ApplyEnv(fs))
	assert.Equal(t, "http://prometheus:9090", *upstream)
	assert.Equal(t, 15*time.Minute, *interval)
	assert.True(t, *enabled)
}

func TestApplyEnv_InvalidValue(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Duration("inventory-sync-interval", time.Minute, "")

	t.Setenv("PROM_ANALYTICS_PROXY_INVENTORY_SYNC_INTERVAL", "abc")

	err := ApplyEnv(fs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid value "abc" for PROM_ANALYTICS_PROXY_INVENTORY_SYNC_INTERVAL`)
}

func TestApplyEnv_EmptyValue(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	interval := fs.Duration("inventory-sync-interval", time.Minute, "")
	batchSize := fs.Int("insert-batch-size", 10, "")

	t.Setenv("PROM_ANALYTICS_PROXY_INVENTORY_SYNC_INTERVAL", "")
	t.Setenv("PROM_ANALYTICS_PROXY_INSERT_BATCH_SIZE", "")

	require.NoError(t, ApplyEnv(fs))
	assert.Equal(t, time.Minute, *interval)
	assert.Equal(t, 10, *batchSize)
}

func TestParseFlags(t *testing.T) {
	original := *DefaultConfig
	t.Cleanup(func() { *DefaultConfig = original })

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
upstream:
  url: "http://from-file:9090"
database:
  provider: "sqlite"
insert:
  batch_size: 20
  buffer_size: 200
`), 0o600)
	require.NoError(t, err)

	t.Run("config file", func(t *testing.T) {
		var configFile string
		fs := newTestFlagSet(&configFile)

		require.NoError(t, ParseFlags(fs, []string{"-config-file", configPath}, &configFile))
		assert.Equal(t, "http://from-file:9090", DefaultConfig.Upstream.URL)
		assert.Equal(t, 20, DefaultConfig.Insert.BatchSize)
	})

	t.Run("env overrides file and flag overrides env", func(t *testing.T) {
		t.Setenv("PROM_ANALYTICS_PROXY_UPSTREAM", "http://from-env:9090")
		t.Setenv("PROM_ANALYTICS_PROXY_DATABASE_PROVIDER", "postgresql")
		t.Setenv("PROM_ANALYTICS_PROXY_INSERT_BATCH_SIZE", "30")

		var configFile string
		fs := newTestFlagSet(&configFile)

		require.NoError(t, ParseFlags(fs, []string{"-config-file", configPath, "-insert-batch-size", "40"}, &configFile))
		assert.Equal(t, "http://from-env:9090", DefaultConfig.Upstream.URL)
		assert.Equal(t, "postgresql", DefaultConfig.Database.Provider)
		assert.Equal(t, 40, DefaultConfig.Insert.BatchSize)
		assert.Equal(t, 200, DefaultConfig.Insert.BufferSize)
	})

	t.Run("config file from env", func(t *testing.T) {
		t.Setenv("PROM_ANALYTICS_PROXY_CONFIG_FILE", configPath)

		var configFile string
		fs := newTestFlagSet(&configFile)

		require.NoError(t, ParseFlags(fs, nil, &configFile))
		assert.Equal(t, configPath, configFile)
		assert.Equal(t, "http://from-file:9090", DefaultConfig.Upstream.URL)
	})

	t.Run("invalid env value", func(t *testing.T) {
		t.Setenv("PROM_ANALYTICS_PROXY_INSERT_BATCH_SIZE", "abc")

		var configFile string
		fs := newTestFlagSet(&configFile)

		err := ParseFlags(fs, nil, &configFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid value "abc" for PROM_ANALYTICS_PROXY_INSERT_BATCH_SIZE`)
	})
}
