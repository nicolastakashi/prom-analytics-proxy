package otlp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildServiceConfigJSON_WithRoundRobinBalancer(t *testing.T) {
	opts := ExporterOptions{
		BalancerName: "round_robin",
		Retry: RetryPolicy{
			MaxAttempts:          3,
			InitialBackoff:       100 * time.Millisecond,
			MaxBackoff:           2 * time.Second,
			BackoffMultiplier:    1.5,
			RetryableStatusCodes: []string{"UNAVAILABLE", "RESOURCE_EXHAUSTED"},
		},
		MaxSendMsgSizeBytes: 10 * 1024 * 1024,
		MaxRecvMsgSizeBytes: 10 * 1024 * 1024,
	}

	configJSON, err := buildServiceConfigJSON(opts)
	require.NoError(t, err)

	var cfg grpcServiceConfigJSON
	err = json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	// Verify load balancing config is present
	assert.NotNil(t, cfg.LoadBalancingConfig)
	assert.Len(t, cfg.LoadBalancingConfig, 1)
	assert.Contains(t, cfg.LoadBalancingConfig[0], "round_robin")

	// Verify method config is still present
	assert.Len(t, cfg.MethodConfig, 1)
	assert.Equal(t, "opentelemetry.proto.collector.metrics.v1.MetricsService", cfg.MethodConfig[0].Name[0].Service)
	assert.Equal(t, "Export", cfg.MethodConfig[0].Name[0].Method)
	assert.Equal(t, 3, cfg.MethodConfig[0].RetryPolicy.MaxAttempts)
}

func TestBuildServiceConfigJSON_WithoutBalancer(t *testing.T) {
	opts := ExporterOptions{
		BalancerName: "",
		Retry: RetryPolicy{
			MaxAttempts:          2,
			InitialBackoff:       250 * time.Millisecond,
			MaxBackoff:           1 * time.Second,
			BackoffMultiplier:    1.6,
			RetryableStatusCodes: []string{"UNAVAILABLE"},
		},
		MaxSendMsgSizeBytes: 10 * 1024 * 1024,
		MaxRecvMsgSizeBytes: 10 * 1024 * 1024,
	}

	configJSON, err := buildServiceConfigJSON(opts)
	require.NoError(t, err)

	var cfg grpcServiceConfigJSON
	err = json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	// Verify load balancing config is NOT present when balancer name is empty
	assert.Nil(t, cfg.LoadBalancingConfig)

	// Verify method config is still present
	assert.Len(t, cfg.MethodConfig, 1)
	assert.Equal(t, "opentelemetry.proto.collector.metrics.v1.MetricsService", cfg.MethodConfig[0].Name[0].Service)
	assert.Equal(t, "Export", cfg.MethodConfig[0].Name[0].Method)
}

func TestBuildServiceConfigJSON_UnknownBalancer(t *testing.T) {
	opts := ExporterOptions{
		BalancerName: "unknown_balancer",
		Retry: RetryPolicy{
			MaxAttempts:          2,
			InitialBackoff:       250 * time.Millisecond,
			MaxBackoff:           1 * time.Second,
			BackoffMultiplier:    1.6,
			RetryableStatusCodes: []string{"UNAVAILABLE"},
		},
		MaxSendMsgSizeBytes: 10 * 1024 * 1024,
		MaxRecvMsgSizeBytes: 10 * 1024 * 1024,
	}

	configJSON, err := buildServiceConfigJSON(opts)
	require.NoError(t, err)

	var cfg grpcServiceConfigJSON
	err = json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	// Unknown balancer should not add load balancing config
	assert.Nil(t, cfg.LoadBalancingConfig)

	// Verify method config is still present
	assert.Len(t, cfg.MethodConfig, 1)
}
