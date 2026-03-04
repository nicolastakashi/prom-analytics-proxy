package otlp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/redis/rueidis"
)

// CatalogSeenCache tracks which metrics have been recently flushed to the catalog,
// allowing suppression of redundant DB upserts across flush cycles.
// Implementations must be safe for concurrent use.
type CatalogSeenCache interface {
	// HasMany returns the set of names that are currently marked as "recently flushed".
	HasMany(ctx context.Context, names []string) (map[string]bool, error)
	// MarkMany records that the given metric names were flushed, expiring after ttl.
	MarkMany(ctx context.Context, names []string, ttl time.Duration) error
	Close() error
}

// redisCatalogSeenCache is a Redis-backed CatalogSeenCache. It uses a dedicated
// key prefix ("catalog_seen:") to avoid collisions with the metric usage cache.
type redisCatalogSeenCache struct {
	client       rueidis.Client
	maxBatchSize int
}

func newRedisCatalogSeenCache(addr, username, password string, db int, dialTimeout, connWriteTimeout time.Duration, batchSize int) (CatalogSeenCache, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis addr is required")
	}

	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	if connWriteTimeout <= 0 {
		connWriteTimeout = 10 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	opts := rueidis.ClientOption{
		InitAddress: []string{addr},
		Dialer: net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 1 * time.Second,
		},
		ConnWriteTimeout: connWriteTimeout,
	}
	if username != "" {
		opts.Username = username
	}
	if password != "" {
		opts.Password = password
	}
	if db > 0 {
		opts.SelectDB = db
	}
	client, err := rueidis.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client for catalog seen cache: %w", err)
	}
	return &redisCatalogSeenCache{client: client, maxBatchSize: batchSize}, nil
}

func (c *redisCatalogSeenCache) key(name string) string {
	return fmt.Sprintf("catalog_seen:%s", name)
}

func (c *redisCatalogSeenCache) HasMany(ctx context.Context, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return make(map[string]bool), nil
	}

	out := make(map[string]bool, len(names))

	for i := 0; i < len(names); i += c.maxBatchSize {
		end := i + c.maxBatchSize
		if end > len(names) {
			end = len(names)
		}
		batch := names[i:end]

		cmds := make([]rueidis.Completed, 0, len(batch))
		for _, name := range batch {
			cmds = append(cmds, c.client.B().Exists().Key(c.key(name)).Build())
		}

		results := c.client.DoMulti(ctx, cmds...)
		for j, name := range batch {
			n, err := results[j].AsInt64()
			if err != nil {
				out[name] = false
				continue
			}
			out[name] = n > 0
		}
	}

	return out, nil
}

func redisTTLSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 3600
	}
	ttlSeconds := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		ttlSeconds++
	}
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	return ttlSeconds
}

func (c *redisCatalogSeenCache) MarkMany(ctx context.Context, names []string, ttl time.Duration) error {
	if len(names) == 0 {
		return nil
	}
	ttlSeconds := redisTTLSeconds(ttl)

	var firstErr error
	for i := 0; i < len(names); i += c.maxBatchSize {
		end := i + c.maxBatchSize
		if end > len(names) {
			end = len(names)
		}
		batch := names[i:end]

		cmds := make([]rueidis.Completed, 0, len(batch))
		for _, name := range batch {
			cmds = append(cmds, c.client.B().Set().Key(c.key(name)).Value("1").ExSeconds(ttlSeconds).Build())
		}

		results := c.client.DoMulti(ctx, cmds...)
		for _, result := range results {
			if err := result.Error(); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}

	return firstErr
}

func (c *redisCatalogSeenCache) Close() error {
	if c.client != nil {
		c.client.Close()
	}
	return nil
}
