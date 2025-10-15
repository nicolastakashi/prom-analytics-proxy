package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/thanos-io/thanos/pkg/tracing/otlp"
	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	Upstream      UpstreamConfig  `yaml:"upstream,omitempty"`
	Server        ServerConfig    `yaml:"server,omitempty"`
	Ingester      IngesterConfig  `yaml:"ingester,omitempty"`
	Database      DatabaseConfig  `yaml:"database,omitempty"`
	Insert        InsertConfig    `yaml:"insert,omitempty"`
	Tracing       *otlp.Config    `yaml:"tracing,omitempty"`
	MetadataLimit uint64          `yaml:"metadata_limit,omitempty"`
	SeriesLimit   uint64          `yaml:"series_limit,omitempty"`
	CORS          CORSConfig      `yaml:"cors,omitempty"`
	Inventory     InventoryConfig `yaml:"inventory,omitempty"`
}

type DatabaseConfig struct {
	Provider   string           `yaml:"provider,omitempty"`
	PostgreSQL PostgreSQLConfig `yaml:"postgresql,omitempty"`
	SQLite     SQLiteConfig     `yaml:"sqlite,omitempty"`
}

type UpstreamConfig struct {
	URL               string `yaml:"url,omitempty"`
	IncludeQueryStats bool   `yaml:"include_query_stats,omitempty"`
}

type ServerConfig struct {
	InsecureListenAddress string `yaml:"insecure_listen_address,omitempty"`
}

type PostgreSQLConfig struct {
	Addr            string        `yaml:"addr,omitempty"`
	Database        string        `yaml:"database,omitempty"`
	DialTimeout     time.Duration `yaml:"dial_timeout,omitempty"`
	Password        string        `yaml:"password,omitempty"`
	Port            int           `yaml:"port,omitempty"`
	SSLMode         string        `yaml:"sslmode,omitempty"`
	User            string        `yaml:"user,omitempty"`
	MaxOpenConns    int           `yaml:"max_open_conns,omitempty"`
	MaxIdleConns    int           `yaml:"max_idle_conns,omitempty"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime,omitempty"`
}

type SQLiteConfig struct {
	DatabasePath string `yaml:"database_paths,omitempty"`
}

type InsertConfig struct {
	BatchSize     int           `yaml:"batch_size,omitempty"`
	BufferSize    int           `yaml:"buffer_size,omitempty"`
	FlushInterval time.Duration `yaml:"flush_interval,omitempty"`
	GracePeriod   time.Duration `yaml:"grace_period,omitempty"`
	Timeout       time.Duration `yaml:"timeout,omitempty"`
}

type CORSConfig struct {
	AllowedOrigins   []string `yaml:"allowed_origins,omitempty"`
	AllowedMethods   []string `yaml:"allowed_methods,omitempty"`
	AllowedHeaders   []string `yaml:"allowed_headers,omitempty"`
	AllowCredentials bool     `yaml:"allow_credentials,omitempty"`
	MaxAge           int      `yaml:"max_age,omitempty"`
}

type InventoryConfig struct {
	Enabled               bool          `yaml:"enabled,omitempty"`
	SyncInterval          time.Duration `yaml:"sync_interval,omitempty"`
	TimeWindow            time.Duration `yaml:"time_window,omitempty"`
	RunTimeout            time.Duration `yaml:"run_timeout,omitempty"`
	MetadataStepTimeout   time.Duration `yaml:"metadata_step_timeout,omitempty"`
	SummaryStepTimeout    time.Duration `yaml:"summary_step_timeout,omitempty"`
	JobIndexLabelTimeout  time.Duration `yaml:"job_index_label_timeout,omitempty"`
	JobIndexPerJobTimeout time.Duration `yaml:"job_index_per_job_timeout,omitempty"`
	JobIndexWorkers       int           `yaml:"job_index_workers,omitempty"`
}

var DefaultConfig = &Config{
	CORS: CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           300,
	},
	Upstream: UpstreamConfig{
		IncludeQueryStats: true,
	},
	Inventory: InventoryConfig{
		Enabled:               true,
		SyncInterval:          10 * time.Minute,
		TimeWindow:            30 * 24 * time.Hour,
		RunTimeout:            300 * time.Second,
		MetadataStepTimeout:   30 * time.Second,
		SummaryStepTimeout:    30 * time.Second,
		JobIndexLabelTimeout:  30 * time.Second,
		JobIndexPerJobTimeout: 30 * time.Second,
		JobIndexWorkers:       10,
	},
	Ingester: IngesterConfig{
		Protocol: string(ProtocolOTLP),
		OTLP: OtlpIngesterConfig{
			ListenAddress: ":4317",
		},
		GracefulShutdownTimeout: 30 * time.Second,
		DrainDelay:              2 * time.Second,
	},
}

type OtlpIngesterConfig struct {
	ListenAddress     string `yaml:"listen_address,omitempty"`
	DownstreamAddress string `yaml:"downstream_address,omitempty"`
}

type MetricIngesterProtocol string

const (
	ProtocolOTLP MetricIngesterProtocol = "otlp"
)

type IngesterConfig struct {
	Protocol string             `yaml:"protocol,omitempty"`
	OTLP     OtlpIngesterConfig `yaml:"otlp,omitempty"`
	// DryRun when true, performs filtering analysis and records metrics
	// but does not actually drop any data
	DryRun bool `yaml:"dry_run,omitempty"`
	// GracefulShutdownTimeout bounds the time we wait for in-flight RPCs and
	// internal buffers to drain before forcing shutdown.
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout,omitempty"`
	// DrainDelay is an optional delay after marking health as NOT_SERVING to
	// allow external load balancers to stop sending new traffic before we
	// begin graceful shutdown.
	DrainDelay time.Duration `yaml:"drain_delay,omitempty"`
}

func LoadConfig(path string) error {
	f, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	err = yaml.Unmarshal(f, DefaultConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config file: %w", err)
	}
	return nil
}

func (c *Config) IsTracingEnabled() bool {
	if c == nil {
		return false
	}
	return c.Tracing != nil
}

func (c *Config) GetTracingServiceName() string {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		if c == nil || c.Tracing == nil {
			return ""
		}
		return c.Tracing.ServiceName
	}
	return serviceName
}

// SanitizedConfig represents a safe version of the configuration
// that excludes sensitive information

// GetSanitizedConfig returns a safe version of the configuration
// that excludes sensitive information
func (c *Config) GetSanitizedConfig() *Config {

	// Copy Database config
	if c.Database.Provider == "postgresql" {
		c.Database.PostgreSQL.User = ""
		c.Database.SQLite.DatabasePath = ""
	}

	return c
}

// RegisterInventoryFlags registers all inventory-related command-line flags
func RegisterInventoryFlags(flagSet *flag.FlagSet) {
	flagSet.DurationVar(&DefaultConfig.Inventory.SyncInterval, "inventory-sync-interval", DefaultConfig.Inventory.SyncInterval, "Interval between inventory sync runs")
	flagSet.DurationVar(&DefaultConfig.Inventory.TimeWindow, "inventory-time-window", DefaultConfig.Inventory.TimeWindow, "Time window for inventory data collection")
	flagSet.DurationVar(&DefaultConfig.Inventory.RunTimeout, "inventory-run-timeout", DefaultConfig.Inventory.RunTimeout, "Timeout for the entire inventory sync run")
	flagSet.DurationVar(&DefaultConfig.Inventory.MetadataStepTimeout, "inventory-metadata-timeout", DefaultConfig.Inventory.MetadataStepTimeout, "Timeout for metadata collection step")
	flagSet.DurationVar(&DefaultConfig.Inventory.SummaryStepTimeout, "inventory-summary-timeout", DefaultConfig.Inventory.SummaryStepTimeout, "Timeout for summary refresh step")
	flagSet.DurationVar(&DefaultConfig.Inventory.JobIndexLabelTimeout, "inventory-job-index-label-timeout", DefaultConfig.Inventory.JobIndexLabelTimeout, "Timeout for job label values collection")
	flagSet.DurationVar(&DefaultConfig.Inventory.JobIndexPerJobTimeout, "inventory-job-index-per-job-timeout", DefaultConfig.Inventory.JobIndexPerJobTimeout, "Timeout for processing each individual job")
	flagSet.IntVar(&DefaultConfig.Inventory.JobIndexWorkers, "inventory-job-index-workers", DefaultConfig.Inventory.JobIndexWorkers, "Number of worker goroutines for job index processing")
}
