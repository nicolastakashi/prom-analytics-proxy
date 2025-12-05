package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thanos-io/thanos/pkg/tracing/otlp"
	yaml "gopkg.in/yaml.v3"
)

const DefaultMemoryLimitRatio = 0.9

type Config struct {
	Upstream        UpstreamConfig    `yaml:"upstream,omitempty"`
	Server          ServerConfig      `yaml:"server,omitempty"`
	Ingester        IngesterConfig    `yaml:"ingester,omitempty"`
	Database        DatabaseConfig    `yaml:"database,omitempty"`
	Insert          InsertConfig      `yaml:"insert,omitempty"`
	Tracing         *otlp.Config      `yaml:"tracing,omitempty"`
	MemoryLimit     MemoryLimitConfig `yaml:"memory_limit,omitempty"`
	MetadataLimit   uint64            `yaml:"metadata_limit,omitempty"`
	SeriesLimit     uint64            `yaml:"series_limit,omitempty"`
	CORS            CORSConfig        `yaml:"cors,omitempty"`
	Inventory       InventoryConfig   `yaml:"inventory,omitempty"`
	QueryProcessing QueryProcessing   `yaml:"query_processing,omitempty"`
}

type QueryProcessing struct {
	ExtractHTTPHeaders []string `yaml:"extract_http_headers,omitempty"`
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

type MemoryLimitConfig struct {
	Enabled         bool          `yaml:"enabled,omitempty"`
	Ratio           float64       `yaml:"ratio,omitempty"`
	RefreshInterval time.Duration `yaml:"refresh_interval,omitempty"`
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
	QueryProcessing: QueryProcessing{
		ExtractHTTPHeaders: []string{"user-agent"},
	},
	MemoryLimit: MemoryLimitConfig{
		Enabled:         false,
		Ratio:           DefaultMemoryLimitRatio,
		RefreshInterval: time.Minute,
	},
	Ingester: IngesterConfig{
		Protocol: string(ProtocolOTLP),
		OTLP: OtlpIngesterConfig{
			ListenAddress: ":4317",
			// Defaults to 10MiB, configurable via YAML/flags
			GRPCMaxRecvMsgSizeBytes:            10 * 1024 * 1024,
			GRPCMaxSendMsgSizeBytes:            10 * 1024 * 1024,
			DownstreamGRPCMaxRecvMsgSizeBytes:  10 * 1024 * 1024,
			DownstreamGRPCMaxSendMsgSizeBytes:  10 * 1024 * 1024,
			DownstreamRetryMaxAttempts:         2,
			DownstreamRetryInitialBackoff:      250 * time.Millisecond,
			DownstreamRetryMaxBackoff:          1 * time.Second,
			DownstreamRetryBackoffMultiplier:   1.6,
			DownstreamRetryCodes:               []string{"UNAVAILABLE"},
			DownstreamConnectMinTimeout:        500 * time.Millisecond,
			DownstreamConnectBaseDelay:         250 * time.Millisecond,
			DownstreamConnectMaxDelay:          5 * time.Second,
			DownstreamConnectBackoffMultiplier: 1.6,
		},
		MetricsListenAddress:    ":9090",
		GracefulShutdownTimeout: 30 * time.Second,
		DrainDelay:              2 * time.Second,
	},
}

type OtlpIngesterConfig struct {
	ListenAddress     string `yaml:"listen_address,omitempty"`
	DownstreamAddress string `yaml:"downstream_address,omitempty"`
	// GRPCMaxRecvMsgSizeBytes controls the maximum size of a single gRPC message
	// the OTLP server will accept.
	GRPCMaxRecvMsgSizeBytes int `yaml:"grpc_max_recv_msg_size_bytes,omitempty"`
	// GRPCMaxSendMsgSizeBytes controls the maximum size of a single gRPC message
	// the OTLP server will send.
	GRPCMaxSendMsgSizeBytes int `yaml:"grpc_max_send_msg_size_bytes,omitempty"`
	// DownstreamGRPCMaxRecvMsgSizeBytes controls the maximum receive size used
	// by the downstream OTLP client when forwarding data.
	DownstreamGRPCMaxRecvMsgSizeBytes int `yaml:"downstream_grpc_max_recv_msg_size_bytes,omitempty"`
	// DownstreamGRPCMaxSendMsgSizeBytes controls the maximum send size used
	// by the downstream OTLP client when forwarding data.
	DownstreamGRPCMaxSendMsgSizeBytes int `yaml:"downstream_grpc_max_send_msg_size_bytes,omitempty"`
	// Downstream retry policy configuration for OTLP client
	DownstreamRetryMaxAttempts       int           `yaml:"downstream_retry_max_attempts,omitempty"`
	DownstreamRetryInitialBackoff    time.Duration `yaml:"downstream_retry_initial_backoff,omitempty"`
	DownstreamRetryMaxBackoff        time.Duration `yaml:"downstream_retry_max_backoff,omitempty"`
	DownstreamRetryBackoffMultiplier float64       `yaml:"downstream_retry_backoff_multiplier,omitempty"`
	DownstreamRetryCodes             []string      `yaml:"downstream_retry_codes,omitempty"`
	// BalancerName is the gRPC load balancer name (e.g., "round_robin").
	// If empty, gRPC defaults to "pick_first".
	BalancerName string `yaml:"balancer_name,omitempty"`
	// Downstream dial settings for channel establishment/failover.
	DownstreamConnectMinTimeout        time.Duration `yaml:"downstream_connect_min_timeout,omitempty"`
	DownstreamConnectBaseDelay         time.Duration `yaml:"downstream_connect_base_delay,omitempty"`
	DownstreamConnectMaxDelay          time.Duration `yaml:"downstream_connect_max_delay,omitempty"`
	DownstreamConnectBackoffMultiplier float64       `yaml:"downstream_connect_backoff_multiplier,omitempty"`
}

type MetricIngesterProtocol string

const (
	ProtocolOTLP MetricIngesterProtocol = "otlp"
)

type IngesterConfig struct {
	Protocol string             `yaml:"protocol,omitempty"`
	OTLP     OtlpIngesterConfig `yaml:"otlp,omitempty"`
	// MetricsListenAddress is the HTTP address where Prometheus metrics are exposed.
	MetricsListenAddress string `yaml:"metrics_listen_address,omitempty"`
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
	// AllowedJobs is a list of job names that are allowed to have unused metrics dropped.
	// If empty, all jobs are subject to unused metric dropping (unless denied).
	AllowedJobs []string `yaml:"allowed_jobs,omitempty"`
	// DeniedJobs is a list of job names that are excluded from unused metric dropping.
	// Metrics from these jobs will never be dropped, even if unused.
	DeniedJobs []string `yaml:"denied_jobs,omitempty"`
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
	// Converts the http header names to lowercase for consistency since it is case insensitive anyway
	for i, header := range DefaultConfig.QueryProcessing.ExtractHTTPHeaders {
		DefaultConfig.QueryProcessing.ExtractHTTPHeaders[i] = strings.ToLower(header)
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

// RegisterMemoryLimitFlags exposes CLI overrides for automatic GOMEMLIMIT management.
func RegisterMemoryLimitFlags(flagSet *flag.FlagSet) {
	flagSet.BoolVar(&DefaultConfig.MemoryLimit.Enabled, "memory-limit-enabled", DefaultConfig.MemoryLimit.Enabled, "Enable automatic GOMEMLIMIT management via automemlimit")
	flagSet.Float64Var(&DefaultConfig.MemoryLimit.Ratio, "memory-limit-ratio", DefaultConfig.MemoryLimit.Ratio, "Ratio (0 < ratio <= 1) of detected memory limit used for GOMEMLIMIT")
	flagSet.DurationVar(&DefaultConfig.MemoryLimit.RefreshInterval, "memory-limit-refresh-interval", DefaultConfig.MemoryLimit.RefreshInterval, "Interval for refreshing the computed memory limit (0 disables refresh)")
}
