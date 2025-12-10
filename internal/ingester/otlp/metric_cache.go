package otlp

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/redis/rueidis"
)

// MetricUsageState represents the cached state of a metric's usage.
type MetricUsageState int8

const (
	// StateUnknown indicates the metric state is not cached.
	StateUnknown MetricUsageState = iota
	// StateUsed indicates the metric is used (has alerts, records, dashboards, or queries).
	StateUsed
	// StateUnused indicates the metric is unused (no alerts, records, dashboards, or queries).
	StateUnused
)

// CacheValue represents the integer value stored in Redis for a metric usage state.
// Using integers (0/1) is more efficient than strings for storage and conversion.
type CacheValue int64

const (
	// CacheValueUnused is stored in Redis as 0 for unused metrics.
	CacheValueUnused CacheValue = 0
	// CacheValueUsed is stored in Redis as 1 for used metrics.
	CacheValueUsed CacheValue = 1
)

// Int64 returns the int64 representation of the cache value for Redis storage.
func (cv CacheValue) Int64() int64 {
	return int64(cv)
}

// MetricUsageCache provides an interface for caching metric usage states.
type MetricUsageCache interface {
	// GetStates retrieves the cached usage states for the given metric names.
	// Returns a map from metric name to state, and any error encountered.
	GetStates(ctx context.Context, names []string) (map[string]MetricUsageState, error)
	// SetStates writes the given usage states to the cache.
	SetStates(ctx context.Context, states map[string]MetricUsageState) error
	// Close closes the cache and releases any resources.
	Close() error
}

// redisMetricUsageCache implements MetricUsageCache using Redis via rueidis.
type redisMetricUsageCache struct {
	client    rueidis.Client
	usedTTL   time.Duration
	unusedTTL time.Duration
	usedOnly  bool
}

// NewRedisMetricUsageCache creates a new Redis-based metric usage cache.
func NewRedisMetricUsageCache(cfg config.RedisCacheConfig) (MetricUsageCache, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Addr == "" {
		return nil, fmt.Errorf("redis addr is required when redis is enabled")
	}

	opts := rueidis.ClientOption{
		InitAddress: []string{cfg.Addr},
	}
	if cfg.Username != "" {
		opts.Username = cfg.Username
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB > 0 {
		opts.SelectDB = cfg.DB
	}

	client, err := rueidis.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client: %w", err)
	}

	usedTTL := cfg.UsedTTL
	if usedTTL <= 0 {
		usedTTL = 1 * time.Hour
	}
	unusedTTL := cfg.UnusedTTL
	if unusedTTL <= 0 {
		unusedTTL = 2 * time.Minute
	}

	return &redisMetricUsageCache{
		client:    client,
		usedTTL:   usedTTL,
		unusedTTL: unusedTTL,
		usedOnly:  cfg.UsedOnly,
	}, nil
}

// key returns the Redis key for a metric usage state.
func (c *redisMetricUsageCache) key(name string) string {
	return fmt.Sprintf("metric_usage:%s", name)
}

// GetStates retrieves cached usage states for the given metric names.
func (c *redisMetricUsageCache) GetStates(ctx context.Context, names []string) (map[string]MetricUsageState, error) {
	if len(names) == 0 {
		return make(map[string]MetricUsageState), nil
	}

	// Build GET commands for each metric (single key per metric)
	cmds := make([]rueidis.Completed, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, c.client.B().Get().Key(c.key(name)).Build())
	}

	// Execute all commands in parallel
	results := make([]rueidis.RedisResult, len(cmds))
	for i, cmd := range cmds {
		results[i] = c.client.Do(ctx, cmd)
	}

	// Parse results - values are stored as integers: 1 = used, 0 = unused
	states := make(map[string]MetricUsageState, len(names))
	for i, name := range names {
		val, err := results[i].AsInt64()
		if err != nil {
			states[name] = StateUnknown
			continue
		}

		switch CacheValue(val) {
		case CacheValueUsed:
			states[name] = StateUsed
		case CacheValueUnused:
			if !c.usedOnly {
				states[name] = StateUnused
			} else {
				states[name] = StateUnknown
			}
		default:
			states[name] = StateUnknown
		}
	}

	return states, nil
}

func (c *redisMetricUsageCache) SetStates(ctx context.Context, states map[string]MetricUsageState) error {
	if len(states) == 0 {
		return nil
	}

	cmds := make([]rueidis.Completed, 0, len(states))
	for name, state := range states {
		var cacheValue CacheValue
		var ttlSeconds int64

		switch state {
		case StateUsed:
			cacheValue = CacheValueUsed
			ttlSeconds = int64(c.usedTTL.Seconds())
			if ttlSeconds <= 0 {
				ttlSeconds = 3600 // Default to 1 hour
			}
		case StateUnused:
			if c.usedOnly {
				continue
			}
			cacheValue = CacheValueUnused
			ttlSeconds = int64(c.unusedTTL.Seconds())
			if ttlSeconds <= 0 {
				ttlSeconds = 120 // Default to 2 minutes
			}
		case StateUnknown:
			continue
		}

		cmds = append(cmds, c.client.B().Set().Key(c.key(name)).Value(strconv.FormatInt(cacheValue.Int64(), 10)).ExSeconds(ttlSeconds).Build())
	}

	if len(cmds) == 0 {
		return nil
	}

	results := c.client.DoMulti(ctx, cmds...)
	for i, result := range results {
		if err := result.Error(); err != nil {
			slog.DebugContext(ctx, "ingester.cache.set.failed", "index", i, "err", err)
		}
	}

	return nil
}

func (c *redisMetricUsageCache) Close() error {
	if c.client != nil {
		c.client.Close()
	}
	return nil
}
