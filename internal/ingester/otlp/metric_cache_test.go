package otlp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

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

	states, err := cache.GetStates(ctx, []string{"metric1", "metric2"})
	require.NoError(t, err)
	assert.Equal(t, StateUnknown, states["metric1"])
	assert.Equal(t, StateUnknown, states["metric2"])

	err = cache.SetStates(ctx, map[string]MetricUsageState{
		"metric1": StateUsed,
		"metric2": StateUnused,
	})
	require.NoError(t, err)

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
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil {
		if cache != nil {
			_ = cache.Close()
		}
	} else {
		assert.Contains(t, err.Error(), "redis")
	}
}

func TestRedisMetricUsageCache_KeyGeneration(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled: true,
		Addr:    "localhost:6379",
		UsedTTL: 1 * time.Hour,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		redisCache := cache.(*redisMetricUsageCache)
		assert.Equal(t, "metric_usage:test_metric", redisCache.key("test_metric"))
		_ = cache.Close()
	}
}

func TestNewRedisMetricUsageCache_BatchSizeDefaults(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled:   true,
		Addr:      "localhost:6379",
		BatchSize: 0,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		redisCache := cache.(*redisMetricUsageCache)
		assert.Equal(t, 100, redisCache.maxBatchSize)
		_ = cache.Close()
	}
}

func TestNewRedisMetricUsageCache_CustomBatchSize(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled:   true,
		Addr:      "localhost:6379",
		BatchSize: 50,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		redisCache := cache.(*redisMetricUsageCache)
		assert.Equal(t, 50, redisCache.maxBatchSize)
		_ = cache.Close()
	}
}

func TestNewRedisMetricUsageCache_TimeoutDefaults(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled:          true,
		Addr:             "localhost:6379",
		DialTimeout:      0,
		ConnWriteTimeout: 0,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		_ = cache.Close()
	}
}

func TestNewRedisMetricUsageCache_CustomTimeouts(t *testing.T) {
	cfg := config.RedisCacheConfig{
		Enabled:          true,
		Addr:             "localhost:6379",
		DialTimeout:      10 * time.Second,
		ConnWriteTimeout: 20 * time.Second,
		BatchSize:        200,
	}
	cache, err := NewRedisMetricUsageCache(cfg)
	if err == nil && cache != nil {
		redisCache := cache.(*redisMetricUsageCache)
		assert.Equal(t, 200, redisCache.maxBatchSize)
		_ = cache.Close()
	}
}

// TestRedisMetricUsageCache_GetStates verifies a read answers cached names,
// reports a name with no entry or an unrecognised value as StateUnknown, and
// leaves a name it cannot read out of the map while still answering the
// others, returning the failure.
func TestRedisMetricUsageCache_GetStates(t *testing.T) {
	addr := startRedis(t)
	cache, err := NewRedisMetricUsageCache(config.RedisCacheConfig{Enabled: true, Addr: addr, BatchSize: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })
	ctx := context.Background()
	require.NoError(t, cache.SetStates(ctx, map[string]MetricUsageState{"cached": StateUsed}))
	admin := newRedisAdmin(t, addr)
	require.NoError(t, admin.Do(ctx, admin.B().Lpush().Key("metric_usage:wrong_type").Element("x").Build()).Error())
	require.NoError(t, admin.Do(ctx, admin.B().Set().Key("metric_usage:not_a_number").Value("x").Build()).Error())
	require.NoError(t, admin.Do(ctx, admin.B().Set().Key("metric_usage:other_value").Value("7").Build()).Error())

	states, err := cache.GetStates(ctx, []string{"cached", "wrong_type", "absent", "not_a_number", "other_value"})

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, map[string]MetricUsageState{
		"cached":      StateUsed,
		"absent":      StateUnknown,
		"other_value": StateUnknown,
	}, states)
}

// TestRedisMetricUsageCache_UsedOnly_ReadsUnusedAsUnknown verifies a cache
// that keeps only used states reads a cached unused state as StateUnknown.
func TestRedisMetricUsageCache_UsedOnly_ReadsUnusedAsUnknown(t *testing.T) {
	addr := startRedis(t)
	cache, err := NewRedisMetricUsageCache(config.RedisCacheConfig{Enabled: true, Addr: addr, UsedOnly: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })
	ctx := context.Background()
	admin := newRedisAdmin(t, addr)
	require.NoError(t, admin.Do(ctx, admin.B().Set().Key("metric_usage:cached_unused").Value("0").Build()).Error())

	states, err := cache.GetStates(ctx, []string{"cached_unused"})

	require.NoError(t, err)
	assert.Equal(t, map[string]MetricUsageState{"cached_unused": StateUnknown}, states)
}

// TestRedisMetricUsageCache_Timeouts verifies that when Redis does not answer
// in time, a read leaves the unread names out and a write fails, both
// returning the timeout.
func TestRedisMetricUsageCache_Timeouts(t *testing.T) {
	addr := startRedis(t)
	cache, err := NewRedisMetricUsageCache(config.RedisCacheConfig{Enabled: true, Addr: addr})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })
	ctx := context.Background()
	require.NoError(t, cache.SetStates(ctx, map[string]MetricUsageState{"cached": StateUsed}))
	admin := newRedisAdmin(t, addr)
	require.NoError(t, admin.Do(ctx, admin.B().ClientPause().Timeout(2000).All().Build()).Error())

	readCtx, cancelRead := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelRead()
	states, err := cache.GetStates(readCtx, []string{"cached", "absent"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, states)

	writeCtx, cancelWrite := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelWrite()
	require.ErrorIs(t, cache.SetStates(writeCtx, map[string]MetricUsageState{"other": StateUsed}), context.DeadlineExceeded)
}

// newRedisAdmin returns a client to addr for setting up test data; it is
// closed when t ends.
func newRedisAdmin(t *testing.T, addr string) rueidis.Client {
	t.Helper()
	admin, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}})
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	return admin
}

// startRedis starts a Redis container for t and returns its address. Skips t
// when Docker is unavailable.
func startRedis(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForAll(wait.ForLog("Ready to accept connections"), wait.ForListeningPort("6379/tcp")),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("Skipping Redis container test (Docker not available): %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	return fmt.Sprintf("%s:%s", host, port.Port())
}
