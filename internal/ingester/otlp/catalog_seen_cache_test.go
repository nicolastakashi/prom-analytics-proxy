package otlp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRedisTTLSeconds(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want int64
	}{
		{name: "negative_defaults_to_one_hour", ttl: -1 * time.Second, want: 3600},
		{name: "zero_defaults_to_one_hour", ttl: 0, want: 3600},
		{name: "one_nanosecond_rounds_up_to_one_second", ttl: 1 * time.Nanosecond, want: 1},
		{name: "sub_second_rounds_up_to_one_second", ttl: 500 * time.Millisecond, want: 1},
		{name: "exact_one_second", ttl: 1 * time.Second, want: 1},
		{name: "one_and_half_seconds_rounds_up", ttl: 1500 * time.Millisecond, want: 2},
		{name: "exact_two_seconds", ttl: 2 * time.Second, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, redisTTLSeconds(tt.ttl))
		})
	}
}

func TestNewRedisCatalogSeenCache_EmptyAddr(t *testing.T) {
	_, err := newRedisCatalogSeenCache("", "", "", 0, 0, 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis addr is required")
}

func TestNewRedisCatalogSeenCache_DefaultsApplied(t *testing.T) {
	cache, err := newRedisCatalogSeenCache("localhost:6379", "", "", 0, 0, 0, 0)
	if err == nil && cache != nil {
		redisCache := cache.(*redisCatalogSeenCache)
		assert.Equal(t, 100, redisCache.maxBatchSize)
		_ = cache.Close()
	}
}

func TestNewRedisCatalogSeenCache_CustomBatchSize(t *testing.T) {
	cache, err := newRedisCatalogSeenCache("localhost:6379", "", "", 0, 5*time.Second, 10*time.Second, 75)
	if err == nil && cache != nil {
		redisCache := cache.(*redisCatalogSeenCache)
		assert.Equal(t, 75, redisCache.maxBatchSize)
		_ = cache.Close()
	}
}

func TestRedisCatalogSeenCache_KeyGeneration(t *testing.T) {
	cache, err := newRedisCatalogSeenCache("localhost:6379", "", "", 0, 0, 0, 0)
	if err == nil && cache != nil {
		redisCache := cache.(*redisCatalogSeenCache)
		assert.Equal(t, "catalog_seen:test_metric", redisCache.key("test_metric"))
		_ = cache.Close()
	}
}
