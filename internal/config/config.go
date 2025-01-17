package config

import (
	"fmt"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/thanos-io/thanos/pkg/tracing/otlp"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Upstream      UpstreamConfig `yaml:"upstream"`
	Server        ServerConfig   `yaml:"server"`
	Database      DatabaseConfig `yaml:"database"`
	Insert        InsertConfig   `yaml:"insert"`
	Tracing       *otlp.Config   `yaml:"tracing"`
	MetadataLimit uint64         `yaml:"metadata_limit"`
	SeriesLimit   uint64         `yaml:"series_limit"`
}

type DatabaseConfig struct {
	Provider   string           `yaml:"provider"`
	ClickHouse ClickHouseConfig `yaml:"clickhouse"`
	PostgreSQL PostgreSQLConfig `yaml:"postgresql"`
	SQLite     SQLiteConfig     `yaml:"sqlite"`
}

type UpstreamConfig struct {
	URL               string `yaml:"url"`
	IncludeQueryStats bool   `yaml:"include_query_stats"`
}

type ServerConfig struct {
	InsecureListenAddress string `yaml:"insecure_listen_address"`
}

type ClickHouseConfig struct {
	Addr        string          `yaml:"addr"`
	DialTimeout time.Duration   `yaml:"dial_timeout"`
	Auth        clickhouse.Auth `yaml:"auth"`
}

type PostgreSQLConfig struct {
	Addr        string        `yaml:"addr"`
	Database    string        `yaml:"database"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
	Password    string        `yaml:"password"`
	Port        int           `yaml:"port"`
	SSLMode     string        `yaml:"sslmode"`
	User        string        `yaml:"user"`
}

type SQLiteConfig struct {
	DatabasePath string `yaml:"database_path"`
}

type InsertConfig struct {
	BatchSize     int           `yaml:"batch_size"`
	BufferSize    int           `yaml:"buffer_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	GracePeriod   time.Duration `yaml:"grace_period"`
	Timeout       time.Duration `yaml:"timeout"`
}

var DefaultConfig = &Config{}

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
	return c.Tracing != nil
}

func (c *Config) GetTracingServiceName() string {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		return c.Tracing.ServiceName
	}
	return serviceName
}
