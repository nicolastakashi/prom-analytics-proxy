package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricsv1pb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// bufAdd is a test helper that mirrors the real buffering flow:
// candidatesForBuffer (L1 check) then addBatch with no remote seen names.
func bufAdd(b *catalogBuffer, metrics ...*metricsv1pb.Metric) {
	candidates := b.candidatesForBuffer(metrics)
	b.addBatch(candidates, nil)
}

// makeGauge creates a minimal gauge metric for use in tests.
func makeGauge(name, help, unit string) *metricsv1pb.Metric {
	return &metricsv1pb.Metric{
		Name:        name,
		Description: help,
		Unit:        unit,
		Data:        &metricsv1pb.Metric_Gauge{Gauge: &metricsv1pb.Gauge{}},
	}
}

func TestCatalogBuffer_AddAndSnapshot(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, nil)

	bufAdd(buf, makeGauge("http_requests_total", "Total HTTP requests", ""))
	bufAdd(buf, makeGauge("http_request_duration_seconds", "Request latency", "s"))
	bufAdd(buf, makeGauge("memory_usage_bytes", "Memory usage", "By"))

	items := buf.snapshot()
	require.Len(t, items, 3)

	byName := make(map[string]string, len(items))
	for _, item := range items {
		byName[item.Name] = item.Type
	}
	assert.Equal(t, "gauge", byName["http_requests_total"])
	assert.Equal(t, "gauge", byName["http_request_duration_seconds"])
	assert.Equal(t, "gauge", byName["memory_usage_bytes"])
}

func TestCatalogBuffer_SnapshotClearsBuffer(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, nil)
	bufAdd(buf, makeGauge("metric_a", "A", ""))

	first := buf.snapshot()
	require.Len(t, first, 1)

	second := buf.snapshot()
	assert.Empty(t, second)
}

func TestCatalogBuffer_DeduplicatesWithinFlushInterval(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, nil)
	m := makeGauge("http_requests_total", "Total HTTP requests", "")

	bufAdd(buf, m, m, m)

	items := buf.snapshot()
	assert.Len(t, items, 1)
}

func TestCatalogBuffer_SuppressesReFlushWithinTTL(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, nil)

	bufAdd(buf, makeGauge("metric_a", "A", ""))
	first := buf.snapshot()
	require.Len(t, first, 1)

	// Same metric again within TTL - should be suppressed.
	bufAdd(buf, makeGauge("metric_a", "A", ""))
	second := buf.snapshot()
	assert.Empty(t, second)
}

func TestCatalogBuffer_ReFlushesAfterTTLExpiry(t *testing.T) {
	buf := newCatalogBuffer(100, 1*time.Millisecond, nil)

	bufAdd(buf, makeGauge("metric_a", "A", ""))
	first := buf.snapshot()
	require.Len(t, first, 1)

	time.Sleep(5 * time.Millisecond)

	// TTL expired - should re-queue for next flush.
	bufAdd(buf, makeGauge("metric_a", "A", ""))
	second := buf.snapshot()
	assert.Len(t, second, 1)
}

func TestCatalogBuffer_DropWhenFull(t *testing.T) {
	buf := newCatalogBuffer(2, time.Hour, nil)

	bufAdd(buf,
		makeGauge("metric_a", "A", ""),
		makeGauge("metric_b", "B", ""),
		makeGauge("metric_c", "C", ""), // should be dropped
	)

	items := buf.snapshot()
	assert.Len(t, items, 2)
}

func TestCatalogBuffer_RemoteSeenSuppressesAddBatch(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, nil)
	metrics := []*metricsv1pb.Metric{
		makeGauge("metric_a", "A", ""),
		makeGauge("metric_b", "B", ""),
	}

	candidates := buf.candidatesForBuffer(metrics)
	// Simulate Redis saying metric_a is already seen.
	remotelySeenNames := map[string]bool{"metric_a": true}
	buf.addBatch(candidates, remotelySeenNames)

	items := buf.snapshot()
	require.Len(t, items, 1)
	assert.Equal(t, "metric_b", items[0].Name)
}

func TestCatalogBuffer_RemoteSeenDoesNotPopulateLocalSeen(t *testing.T) {
	// When seenCache is nil, there is no Redis. RemotelySeenNames should not
	// interfere with the in-memory seen map - that map is only written by snapshot().
	buf := newCatalogBuffer(100, time.Hour, nil)
	m := makeGauge("metric_a", "A", "")

	candidates := buf.candidatesForBuffer([]*metricsv1pb.Metric{m})
	buf.addBatch(candidates, map[string]bool{"metric_a": true})

	// metric_a was skipped by addBatch (remote seen), but the in-memory seen map
	// is only updated via snapshot(), so it is still a candidate until flushed.
	candidates2 := buf.candidatesForBuffer([]*metricsv1pb.Metric{m})
	assert.Len(t, candidates2, 1)
}

// fakeCatalogSeenCache is an in-memory CatalogSeenCache for testing Redis path behaviour.
type fakeCatalogSeenCache struct {
	data map[string]bool
}

func newFakeCatalogSeenCache() *fakeCatalogSeenCache {
	return &fakeCatalogSeenCache{data: make(map[string]bool)}
}

func (f *fakeCatalogSeenCache) HasMany(_ context.Context, names []string) (map[string]bool, error) {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = f.data[name]
	}
	return out, nil
}

func (f *fakeCatalogSeenCache) MarkMany(_ context.Context, names []string, _ time.Duration) error {
	for _, name := range names {
		f.data[name] = true
	}
	return nil
}

func (f *fakeCatalogSeenCache) Close() error { return nil }

func TestCatalogBuffer_WithSeenCache_NoInMemorySeenMap(t *testing.T) {
	fakeCache := newFakeCatalogSeenCache()
	buf := newCatalogBuffer(100, time.Hour, fakeCache)

	// When seenCache is set, the in-memory seen map is nil (not allocated).
	assert.Nil(t, buf.seen)
}

func TestCatalogBuffer_WithSeenCache_SuppressesAfterRemoteMark(t *testing.T) {
	fakeCache := newFakeCatalogSeenCache()
	buf := newCatalogBuffer(100, time.Hour, fakeCache)

	// Simulate a previous flush cycle having marked metric_a in Redis.
	_ = fakeCache.MarkMany(context.Background(), []string{"metric_a"}, time.Hour)

	m := makeGauge("metric_a", "A", "")
	// With seenCache set, candidatesForBuffer only checks pending, not seen map.
	// metric_a is not in pending so it comes back as a candidate.
	candidates := buf.candidatesForBuffer([]*metricsv1pb.Metric{m})
	require.Len(t, candidates, 1)

	// The caller (bufferCatalogEntries) does the remote lookup and passes results to addBatch.
	seen, _ := fakeCache.HasMany(context.Background(), []string{"metric_a"})
	buf.addBatch(candidates, seen)

	items := buf.snapshot()
	assert.Empty(t, items) // suppressed by Redis
}

func TestOtlpTypeToPrometheus(t *testing.T) {
	tests := []struct {
		name     string
		metric   *metricsv1pb.Metric
		expected string
	}{
		{
			name: "gauge",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_Gauge{Gauge: &metricsv1pb.Gauge{}},
			},
			expected: "gauge",
		},
		{
			name: "monotonic_sum_is_counter",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_Sum{Sum: &metricsv1pb.Sum{IsMonotonic: true}},
			},
			expected: "counter",
		},
		{
			name: "non_monotonic_sum_is_gauge",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_Sum{Sum: &metricsv1pb.Sum{IsMonotonic: false}},
			},
			expected: "gauge",
		},
		{
			name: "histogram",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_Histogram{Histogram: &metricsv1pb.Histogram{}},
			},
			expected: "histogram",
		},
		{
			name: "exponential_histogram",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_ExponentialHistogram{ExponentialHistogram: &metricsv1pb.ExponentialHistogram{}},
			},
			expected: "histogram",
		},
		{
			name: "summary",
			metric: &metricsv1pb.Metric{
				Data: &metricsv1pb.Metric_Summary{Summary: &metricsv1pb.Summary{}},
			},
			expected: "summary",
		},
		{
			name:     "unknown_when_no_data",
			metric:   &metricsv1pb.Metric{},
			expected: "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := otlpTypeToPrometheus(tc.metric)
			assert.Equal(t, tc.expected, got)
		})
	}
}
