package otlp

import (
	"context"
	"fmt"
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
	client rueidis.Client
}

func newRedisCatalogSeenCache(addr, username, password string, db int) (CatalogSeenCache, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis addr is required")
	}
	opts := rueidis.ClientOption{
		InitAddress: []string{addr},
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
	return &redisCatalogSeenCache{client: client}, nil
}

func (c *redisCatalogSeenCache) key(name string) string {
	return fmt.Sprintf("catalog_seen:%s", name)
}

func (c *redisCatalogSeenCache) HasMany(ctx context.Context, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return make(map[string]bool), nil
	}

	cmds := make([]rueidis.Completed, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, c.client.B().Exists().Key(c.key(name)).Build())
	}

	results := c.client.DoMulti(ctx, cmds...)
	out := make(map[string]bool, len(names))
	for i, name := range names {
		n, err := results[i].AsInt64()
		if err != nil {
			out[name] = false
			continue
		}
		out[name] = n > 0
	}
	return out, nil
}

func (c *redisCatalogSeenCache) MarkMany(ctx context.Context, names []string, ttl time.Duration) error {
	if len(names) == 0 {
		return nil
	}
	ttlSeconds := int64(ttl.Seconds())
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}

	cmds := make([]rueidis.Completed, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, c.client.B().Set().Key(c.key(name)).Value("1").ExSeconds(ttlSeconds).Build())
	}

	results := c.client.DoMulti(ctx, cmds...)
	for _, result := range results {
		if err := result.Error(); err != nil {
			return err
		}
	}
	return nil
}

func (c *redisCatalogSeenCache) Close() error {
	if c.client != nil {
		c.client.Close()
	}
	return nil
}
