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

type MetricUsageState int8

const (
	StateUnknown MetricUsageState = iota
	StateUsed
	StateUnused
)

type CacheValue int64

const (
	CacheValueUnused CacheValue = 0
	CacheValueUsed CacheValue = 1
)

func (cv CacheValue) Int64() int64 {
	return int64(cv)
}

type MetricUsageCache interface {
	GetStates(ctx context.Context, names []string) (map[string]MetricUsageState, error)
	SetStates(ctx context.Context, states map[string]MetricUsageState) error
	Close() error
}

type redisMetricUsageCache struct {
	client    rueidis.Client
	usedTTL   time.Duration
	unusedTTL time.Duration
	usedOnly  bool
}

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

func (c *redisMetricUsageCache) key(name string) string {
	return fmt.Sprintf("metric_usage:%s", name)
}

func (c *redisMetricUsageCache) GetStates(ctx context.Context, names []string) (map[string]MetricUsageState, error) {
	if len(names) == 0 {
		return make(map[string]MetricUsageState), nil
	}

	cmds := make([]rueidis.Completed, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, c.client.B().Get().Key(c.key(name)).Build())
	}

	results := make([]rueidis.RedisResult, len(cmds))
	for i, cmd := range cmds {
		results[i] = c.client.Do(ctx, cmd)
	}

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
				ttlSeconds = 3600
			}
		case StateUnused:
			if c.usedOnly {
				continue
			}
			cacheValue = CacheValueUnused
			ttlSeconds = int64(c.unusedTTL.Seconds())
			if ttlSeconds <= 0 {
				ttlSeconds = 120
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
