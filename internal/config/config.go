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
	Retention       RetentionConfig   `yaml:"retention,omitempty"`
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
	InsecureListenAddress   string        `yaml:"insecure_listen_address,omitempty"`
	PushMetricsUsageTimeout time.Duration `yaml:"push_metrics_usage_timeout,omitempty"`
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
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time,omitempty"`
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
	// MetadataSyncEnabled controls whether the syncer fetches metric metadata from
	// Prometheus and populates metrics_catalog. Set to false when the OTLP ingester
	// handles catalog population, so the syncer only refreshes usage summaries.
	MetadataSyncEnabled   bool          `yaml:"metadata_sync_enabled,omitempty"`
	SyncInterval          time.Duration `yaml:"sync_interval,omitempty"`
	TimeWindow            time.Duration `yaml:"time_window,omitempty"`
	RunTimeout            time.Duration `yaml:"run_timeout,omitempty"`
	MetadataStepTimeout   time.Duration `yaml:"metadata_step_timeout,omitempty"`
	SummaryStepTimeout    time.Duration `yaml:"summary_step_timeout,omitempty"`
	JobIndexLabelTimeout  time.Duration `yaml:"job_index_label_timeout,omitempty"`
	JobIndexPerJobTimeout time.Duration `yaml:"job_index_per_job_timeout,omitempty"`
	JobIndexWorkers       int           `yaml:"job_index_workers,omitempty"`
}

type RetentionConfig struct {
	Enabled       bool          `yaml:"enabled,omitempty"`
	Interval      time.Duration `yaml:"interval,omitempty"`
	RunTimeout    time.Duration `yaml:"run_timeout,omitempty"`
	QueriesMaxAge time.Duration `yaml:"queries_max_age,omitempty"`
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
	Server: ServerConfig{
		PushMetricsUsageTimeout: 30 * time.Second,
	},
	Inventory: InventoryConfig{
		Enabled:               true,
		MetadataSyncEnabled:   true,
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
	Retention: RetentionConfig{
		Enabled:       false,
		Interval:      1 * time.Hour,
		RunTimeout:    5 * time.Minute,
		QueriesMaxAge: 30 * 24 * time.Hour,
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
			LookupChunkSize:                    500,
		},
		MetricsListenAddress:    ":9090",
		GracefulShutdownTimeout: 30 * time.Second,
		DrainDelay:              2 * time.Second,
		Redis: RedisCacheConfig{
			Enabled:   false,
			UsedTTL:   1 * time.Hour,
			UnusedTTL: 2 * time.Minute,
			UsedOnly:  false,
		},
		CatalogSync: CatalogSyncConfig{
			Enabled:       false,
			FlushInterval: 30 * time.Second,
			BufferSize:    10000,
			SeenTTL:       1 * time.Hour,
		},
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
	// LookupChunkSize is the batch size for database lookups when checking metric usage.
	// Larger values reduce database queries but increase memory usage and query time.
	// Default: 1000 (was 500). PostgreSQL supports much larger values, SQLite limit is 999.
	LookupChunkSize int `yaml:"lookup_chunk_size,omitempty"`
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
	// Redis configuration for metric usage caching
	Redis RedisCacheConfig `yaml:"redis,omitempty"`
	// CatalogSync controls catalog population from OTLP traffic.
	// When enabled, the ingester populates metrics_catalog directly instead of
	// relying on the Prometheus metadata sync (set inventory.metadata_sync_enabled=false).
	CatalogSync CatalogSyncConfig `yaml:"catalog_sync,omitempty"`
}

type CatalogSyncConfig struct {
	// Enabled controls whether the ingester populates metrics_catalog from OTLP traffic.
	// When true, metrics seen in OTLP requests are buffered and periodically flushed to the DB.
	Enabled       bool          `yaml:"enabled,omitempty"`
	FlushInterval time.Duration `yaml:"flush_interval,omitempty"`
	BufferSize    int           `yaml:"buffer_size,omitempty"`
	// SeenTTL is how long a metric is suppressed from re-flushing after its first write.
	// Prevents repeated upserts for metrics that are already in the catalog.
	// After the TTL expires the metric is re-flushed to refresh last_synced_at.
	// Defaults to 1h.
	SeenTTL time.Duration `yaml:"seen_ttl,omitempty"`
}

type RedisCacheConfig struct {
	// Enabled enables Redis-based metric usage caching
	Enabled bool `yaml:"enabled,omitempty"`
	// Addr is the Redis server address (host:port)
	Addr string `yaml:"addr,omitempty"`
	// Username is the Redis username (optional)
	Username string `yaml:"username,omitempty"`
	// Password is the Redis password (optional)
	Password string `yaml:"password,omitempty"`
	// DB is the Redis database number (default 0)
	DB int `yaml:"db,omitempty"`
	// UsedTTL is the TTL for caching "used" metric states (default 1h)
	UsedTTL time.Duration `yaml:"used_ttl,omitempty"`
	// UnusedTTL is the TTL for caching "unused" metric states (default 2m)
	UnusedTTL time.Duration `yaml:"unused_ttl,omitempty"`
	// UsedOnly when true, only caches "used" states and never caches "unused" states
	UsedOnly bool `yaml:"used_only,omitempty"`
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
		c.Database.PostgreSQL.Password = ""
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
	flagSet.BoolVar(&DefaultConfig.Inventory.MetadataSyncEnabled, "inventory-metadata-sync-enabled", DefaultConfig.Inventory.MetadataSyncEnabled, "Enable Prometheus metadata sync to populate metrics catalog (disable when OTLP ingester handles catalog population)")
}

// RegisterMemoryLimitFlags exposes CLI overrides for automatic GOMEMLIMIT management.
func RegisterMemoryLimitFlags(flagSet *flag.FlagSet) {
	flagSet.BoolVar(&DefaultConfig.MemoryLimit.Enabled, "memory-limit-enabled", DefaultConfig.MemoryLimit.Enabled, "Enable automatic GOMEMLIMIT management via automemlimit")
	flagSet.Float64Var(&DefaultConfig.MemoryLimit.Ratio, "memory-limit-ratio", DefaultConfig.MemoryLimit.Ratio, "Ratio (0 < ratio <= 1) of detected memory limit used for GOMEMLIMIT")
	flagSet.DurationVar(&DefaultConfig.MemoryLimit.RefreshInterval, "memory-limit-refresh-interval", DefaultConfig.MemoryLimit.RefreshInterval, "Interval for refreshing the computed memory limit (0 disables refresh)")
}

func RegisterRetentionFlags(flagSet *flag.FlagSet) {
	flagSet.BoolVar(&DefaultConfig.Retention.Enabled, "retention-enabled", DefaultConfig.Retention.Enabled, "Enable the retention worker to delete old data")
	flagSet.DurationVar(&DefaultConfig.Retention.Interval, "retention-interval", DefaultConfig.Retention.Interval, "Interval between retention runs")
	flagSet.DurationVar(&DefaultConfig.Retention.RunTimeout, "retention-run-timeout", DefaultConfig.Retention.RunTimeout, "Timeout for each retention run")
	flagSet.DurationVar(&DefaultConfig.Retention.QueriesMaxAge, "retention-queries-max-age", DefaultConfig.Retention.QueriesMaxAge, "Maximum age for queries before deletion")
}
