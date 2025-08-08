package config

import (
	"fmt"
	"os"
	"time"

	"github.com/thanos-io/thanos/pkg/tracing/otlp"
	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	Upstream      UpstreamConfig `yaml:"upstream,omitempty"`
	Server        ServerConfig   `yaml:"server,omitempty"`
	Database      DatabaseConfig `yaml:"database,omitempty"`
	Insert        InsertConfig   `yaml:"insert,omitempty"`
	Tracing       *otlp.Config   `yaml:"tracing,omitempty"`
	MetadataLimit uint64         `yaml:"metadata_limit,omitempty"`
	SeriesLimit   uint64         `yaml:"series_limit,omitempty"`
	CORS          CORSConfig     `yaml:"cors,omitempty"`
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
