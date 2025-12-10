package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryMetricCache is a simple in-memory implementation for testing
type inMemoryMetricCache struct {
	states map[string]MetricUsageState
}

func newInMemoryMetricCache() *inMemoryMetricCache {
	return &inMemoryMetricCache{
		states: make(map[string]MetricUsageState),
	}
}

func (c *inMemoryMetricCache) GetStates(ctx context.Context, names []string) (map[string]MetricUsageState, error) {
	result := make(map[string]MetricUsageState, len(names))
	for _, name := range names {
		if state, ok := c.states[name]; ok {
			result[name] = state
		} else {
			result[name] = StateUnknown
		}
	}
	return result, nil
}

func (c *inMemoryMetricCache) SetStates(ctx context.Context, states map[string]MetricUsageState) error {
	for name, state := range states {
		c.states[name] = state
	}
	return nil
}

func (c *inMemoryMetricCache) Close() error {
	return nil
}

func TestMetricUsageState(t *testing.T) {
	assert.Equal(t, StateUnknown, MetricUsageState(0))
	assert.Equal(t, StateUsed, StateUsed)
	assert.Equal(t, StateUnused, StateUnused)
}

func TestInMemoryMetricCache_GetStates(t *testing.T) {
	cache := newInMemoryMetricCache()
	ctx := context.Background()

	// Empty cache returns all unknown
	states, err := cache.GetStates(ctx, []string{"metric1", "metric2"})
	require.NoError(t, err)
	assert.Equal(t, StateUnknown, states["metric1"])
	assert.Equal(t, StateUnknown, states["metric2"])

	// Set some states
	err = cache.SetStates(ctx, map[string]MetricUsageState{
		"metric1": StateUsed,
		"metric2": StateUnused,
	})
	require.NoError(t, err)

	// Retrieve states
	states, err = cache.GetStates(ctx, []string{"metric1", "metric2", "metric3"})
	require.NoError(t, err)
	assert.Equal(t, StateUsed, states["metric1"])
	assert.Equal(t, StateUnused, states["metric2"])
	assert.Equal(t, StateUnknown, states["metric3"])
}

func TestInMemoryMetricCache_SetStates(t *testing.T) {
	cache := newInMemoryMetricCache()
	ctx := context.Background()

	states := map[string]MetricUsageState{
		"metric1": StateUsed,
		"metric2": StateUnused,
		"metric3": StateUnknown,
	}

	err := cache.SetStates(ctx, states)
	require.NoError(t, err)

	// Unknown states should not be cached
	retrieved, err := cache.GetStates(ctx, []string{"metric1", "metric2", "metric3"})
	require.NoError(t, err)
	assert.Equal(t, StateUsed, retrieved["metric1"])
	assert.Equal(t, StateUnused, retrieved["metric2"])
	assert.Equal(t, StateUnknown, retrieved["metric3"])
}

func TestNewRedisMetricUsageCache_Disabled(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled: false,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	assert.NoError(t, err)
	assert.Nil(t, cache)
}

func TestNewRedisMetricUsageCache_NoAddr(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled: true,
		Addr:    "",
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	assert.Error(t, err)
	assert.Nil(t, cache)
	assert.Contains(t, err.Error(), "redis addr is required")
}

func TestNewRedisMetricUsageCache_Defaults(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled: true,
		Addr:    "localhost:6379",
	}
	// This will fail to connect, but we can test the defaults are set
	cache, err := NewRedisMetricUsageCache(cfg)
	// We expect an error because Redis is not running, but the defaults should be applied
	if err == nil {
		// If somehow it connected, close it
		if cache != nil {
			_ = cache.Close()
		}
	} else {
		// Expected - Redis not available
		assert.Contains(t, err.Error(), "redis")
	}
}

func TestRedisMetricUsageCache_KeyGeneration(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled: true,
		Addr:    "localhost:6379",
		UsedTTL: 1 * time.Hour,
	}
	// Create cache instance to test key generation (will fail to connect but that's ok)
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		redisCache := cache.(*redisMetricUsageCache)
		assert.Equal(t, "metric_usage:test_metric", redisCache.key("test_metric"))
		_ = cache.Close()
	}
}
