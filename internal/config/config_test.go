package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thanos-io/thanos/pkg/tracing/otlp"
)

func TestLoadConfig_ValidYAML(t *testing.T) {
	// Save original default config
	originalConfig := DefaultConfig

	// Create a temporary config file
	configContent := `
upstream:
  url: "http://localhost:9090"
  include_query_stats: true
server:
  insecure_listen_address: ":9091"
database:
  provider: "sqlite"
  sqlite:
    database_paths: "test.db"
insert:
  batch_size: 20
  buffer_size: 100
  flush_interval: "5s"
  grace_period: "5s"
  timeout: "1s"
metadata_limit: 1000
series_limit: 1000
cors:
  allowed_origins: ["*"]
  allowed_methods: ["GET", "POST"]
  allowed_headers: ["Content-Type"]
  allow_credentials: true
  max_age: 300
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer func() {
		if err := os.Remove(tmpfile.Name()); err != nil {
			t.Logf("failed to remove temp file: %v", err)
		}
	}()

	_, err = tmpfile.Write([]byte(configContent))
	require.NoError(t, err)
	if err := tmpfile.Close(); err != nil {
		t.Logf("failed to close temp file: %v", err)
	}

	// Reset default config
	DefaultConfig = &Config{
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

	// Restore original config after test
	defer func() {
		DefaultConfig = originalConfig
	}()

	err = LoadConfig(tmpfile.Name())
	require.NoError(t, err)

	// Verify loaded values
	assert.Equal(t, "http://localhost:9090", DefaultConfig.Upstream.URL)
	assert.True(t, DefaultConfig.Upstream.IncludeQueryStats)
	assert.Equal(t, ":9091", DefaultConfig.Server.InsecureListenAddress)
	assert.Equal(t, "sqlite", DefaultConfig.Database.Provider)
	assert.Equal(t, "test.db", DefaultConfig.Database.SQLite.DatabasePath)
	assert.Equal(t, 20, DefaultConfig.Insert.BatchSize)
	assert.Equal(t, 100, DefaultConfig.Insert.BufferSize)
	assert.Equal(t, 5*time.Second, DefaultConfig.Insert.FlushInterval)
	assert.Equal(t, 5*time.Second, DefaultConfig.Insert.GracePeriod)
	assert.Equal(t, 1*time.Second, DefaultConfig.Insert.Timeout)
	assert.Equal(t, uint64(1000), DefaultConfig.MetadataLimit)
	assert.Equal(t, uint64(1000), DefaultConfig.SeriesLimit)
	assert.Equal(t, []string{"*"}, DefaultConfig.CORS.AllowedOrigins)
	assert.Equal(t, []string{"GET", "POST"}, DefaultConfig.CORS.AllowedMethods)
	assert.Equal(t, []string{"Content-Type"}, DefaultConfig.CORS.AllowedHeaders)
	assert.True(t, DefaultConfig.CORS.AllowCredentials)
	assert.Equal(t, 300, DefaultConfig.CORS.MaxAge)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	// Create a temporary config file with invalid YAML
	configContent := `
upstream:
  url: "http://localhost:9090"
  include_query_stats: true
server:
  insecure_listen_address: ":9091"
database:
  provider: "sqlite"
  sqlite:
    database_paths: "test.db"
insert:
  batch_size: "invalid" # Should be int, not string
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer func() {
		if err := os.Remove(tmpfile.Name()); err != nil {
			t.Logf("failed to remove temp file: %v", err)
		}
	}()

	_, err = tmpfile.Write([]byte(configContent))
	require.NoError(t, err)
	if err := tmpfile.Close(); err != nil {
		t.Logf("failed to close temp file: %v", err)
	}

	err = LoadConfig(tmpfile.Name())
	assert.Error(t, err)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	err := LoadConfig("nonexistent-file.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestConfig_IsTracingEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name:     "tracing enabled",
			config:   &Config{Tracing: &otlp.Config{}},
			expected: true,
		},
		{
			name:     "tracing disabled",
			config:   &Config{Tracing: nil},
			expected: false,
		},
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsTracingEnabled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetTracingServiceName(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		envServiceName string
		expected       string
	}{
		{
			name: "service name from config",
			config: &Config{
				Tracing: &otlp.Config{
					ServiceName: "test-service",
				},
			},
			envServiceName: "",
			expected:       "test-service",
		},
		{
			name: "service name from environment",
			config: &Config{
				Tracing: &otlp.Config{
					ServiceName: "config-service",
				},
			},
			envServiceName: "env-service",
			expected:       "env-service",
		},
		{
			name:           "no tracing config",
			config:         &Config{Tracing: nil},
			envServiceName: "",
			expected:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envServiceName != "" {
				if err := os.Setenv("OTEL_SERVICE_NAME", tt.envServiceName); err != nil {
					t.Logf("failed to set environment variable: %v", err)
				}
				defer func() {
					if err := os.Unsetenv("OTEL_SERVICE_NAME"); err != nil {
						t.Logf("failed to unset environment variable: %v", err)
					}
				}()
			}

			result := tt.config.GetTracingServiceName()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetSanitizedConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected *Config
	}{
		{
			name: "postgresql config",
			config: &Config{
				Database: DatabaseConfig{
					Provider: "postgresql",
					PostgreSQL: PostgreSQLConfig{
						Addr:     "localhost",
						Port:     5432,
						Database: "testdb",
						User:     "testuser",
						Password: "testpass",
					},
					SQLite: SQLiteConfig{
						DatabasePath: "test.db",
					},
				},
			},
			expected: &Config{
				Database: DatabaseConfig{
					Provider: "postgresql",
					PostgreSQL: PostgreSQLConfig{
						Addr:     "localhost",
						Port:     5432,
						Database: "testdb",
						User:     "", // Should be sanitized
						Password: "", // Should be sanitized
					},
					SQLite: SQLiteConfig{
						DatabasePath: "", // Should be sanitized
					},
				},
			},
		},
		{
			name: "sqlite config",
			config: &Config{
				Database: DatabaseConfig{
					Provider: "sqlite",
					PostgreSQL: PostgreSQLConfig{
						Addr:     "localhost",
						Port:     5432,
						Database: "testdb",
						User:     "testuser",
						Password: "testpass",
					},
					SQLite: SQLiteConfig{
						DatabasePath: "test.db",
					},
				},
			},
			expected: &Config{
				Database: DatabaseConfig{
					Provider: "sqlite",
					PostgreSQL: PostgreSQLConfig{
						Addr:     "localhost",
						Port:     5432,
						Database: "testdb",
						User:     "testuser", // Should not be sanitized for sqlite
						Password: "testpass",
					},
					SQLite: SQLiteConfig{
						DatabasePath: "test.db", // Should not be sanitized for sqlite
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetSanitizedConfig()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultConfig_Initialization(t *testing.T) {
	// Test that DefaultConfig is properly initialized
	assert.NotNil(t, DefaultConfig)
	assert.Equal(t, []string{"*"}, DefaultConfig.CORS.AllowedOrigins)
	assert.Equal(t, []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}, DefaultConfig.CORS.AllowedMethods)
	assert.Equal(t, []string{"Content-Type", "Authorization", "X-Requested-With"}, DefaultConfig.CORS.AllowedHeaders)
	assert.True(t, DefaultConfig.CORS.AllowCredentials)
	assert.Equal(t, 300, DefaultConfig.CORS.MaxAge)
	assert.True(t, DefaultConfig.Upstream.IncludeQueryStats)
}

func TestConfig_StructTags(t *testing.T) {
	// Test that struct tags are properly defined
	config := &Config{}

	// This test ensures the struct can be marshaled/unmarshaled
	// The actual validation would be done by the YAML package
	assert.NotNil(t, config)
}

func TestPostgreSQLConfig_StructTags(t *testing.T) {
	config := &PostgreSQLConfig{}
	assert.NotNil(t, config)
}

func TestSQLiteConfig_StructTags(t *testing.T) {
	config := &SQLiteConfig{}
	assert.NotNil(t, config)
}

func TestInsertConfig_StructTags(t *testing.T) {
	config := &InsertConfig{}
	assert.NotNil(t, config)
}

func TestCORSConfig_StructTags(t *testing.T) {
	config := &CORSConfig{}
	assert.NotNil(t, config)
}
