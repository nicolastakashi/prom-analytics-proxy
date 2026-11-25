package otlp

import (
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
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

func makeMonotonicSum(name, help, unit string) *metricsv1pb.Metric {
	return &metricsv1pb.Metric{
		Name:        name,
		Description: help,
		Unit:        unit,
		Data:        &metricsv1pb.Metric_Sum{Sum: &metricsv1pb.Sum{IsMonotonic: true}},
	}
}

func makeSummary(name, help, unit string) *metricsv1pb.Metric {
	return &metricsv1pb.Metric{
		Name:        name,
		Description: help,
		Unit:        unit,
		Data:        &metricsv1pb.Metric_Summary{Summary: &metricsv1pb.Summary{}},
	}
}

func makeHistogram(name, help, unit string) *metricsv1pb.Metric {
	return &metricsv1pb.Metric{
		Name:        name,
		Description: help,
		Unit:        unit,
		Data:        &metricsv1pb.Metric_Histogram{Histogram: &metricsv1pb.Histogram{}},
	}
}

func TestCatalogBuffer_AddAndDrain(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)

	bufAdd(buf, makeGauge("http_requests_total", "Total HTTP requests", ""))
	bufAdd(buf, makeGauge("http_request_duration_seconds", "Request latency", "s"))
	bufAdd(buf, makeGauge("memory_usage_bytes", "Memory usage", "By"))

	items := buf.drain()
	require.Len(t, items, 3)

	byName := make(map[string]string, len(items))
	for _, item := range items {
		byName[item.Name] = item.Type
	}
	assert.Equal(t, "gauge", byName["http_requests_total"])
	assert.Equal(t, "gauge", byName["http_request_duration_seconds"])
	assert.Equal(t, "gauge", byName["memory_usage_bytes"])
}

func TestCatalogBuffer_DrainClearsBuffer(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)
	bufAdd(buf, makeGauge("metric_a", "A", ""))

	first := buf.drain()
	require.Len(t, first, 1)

	second := buf.drain()
	assert.Empty(t, second)
}

func TestCatalogBuffer_DeduplicatesWithinFlushInterval(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)
	m := makeGauge("http_requests_total", "Total HTTP requests", "")

	bufAdd(buf, m, m, m)

	items := buf.drain()
	assert.Len(t, items, 1)
}

func TestCatalogBuffer_NormalizesMonotonicSumNames(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)

	bufAdd(buf, makeMonotonicSum("node_cpu_seconds", "CPU time", "s"))

	items := buf.drain()
	require.Len(t, items, 1)
	assert.Equal(t, "node_cpu_seconds_total", items[0].Name)
	assert.Equal(t, "counter", items[0].Type)
}

func TestCatalogBuffer_ExpandsSummaryNames(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)

	bufAdd(buf, makeSummary("http_request_duration_seconds", "Request duration", "s"))

	items := buf.drain()
	require.Len(t, items, 3)

	names := make(map[string]string, len(items))
	for _, item := range items {
		names[item.Name] = item.Type
	}
	assert.Equal(t, "summary", names["http_request_duration_seconds"])
	assert.Equal(t, "summary", names["http_request_duration_seconds_sum"])
	assert.Equal(t, "summary", names["http_request_duration_seconds_count"])
}

func TestCatalogBuffer_ExpandsHistogramNames(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)

	bufAdd(buf, makeHistogram("http_request_duration_seconds", "Request duration", "s"))

	items := buf.drain()
	require.Len(t, items, 4)

	names := make(map[string]string, len(items))
	for _, item := range items {
		names[item.Name] = item.Type
	}
	assert.Equal(t, "histogram", names["http_request_duration_seconds"])
	assert.Equal(t, "histogram", names["http_request_duration_seconds_bucket"])
	assert.Equal(t, "histogram", names["http_request_duration_seconds_sum"])
	assert.Equal(t, "histogram", names["http_request_duration_seconds_count"])
}

func TestCatalogBuffer_ReFlushesAfterTTLExpiry(t *testing.T) {
	buf := newCatalogBuffer(100, 1*time.Millisecond, true)

	bufAdd(buf, makeGauge("metric_a", "A", ""))
	first := buf.drain()
	require.Len(t, first, 1)
	buf.markFlushed(first, time.Now())

	time.Sleep(5 * time.Millisecond)

	// TTL expired - should re-queue for next flush.
	bufAdd(buf, makeGauge("metric_a", "A", ""))
	second := buf.drain()
	assert.Len(t, second, 1)
}

func TestCatalogBuffer_DropWhenFull(t *testing.T) {
	buf := newCatalogBuffer(2, time.Hour, true)

	bufAdd(buf,
		makeGauge("metric_a", "A", ""),
		makeGauge("metric_b", "B", ""),
		makeGauge("metric_c", "C", ""), // should be dropped
	)

	items := buf.drain()
	assert.Len(t, items, 2)
}

func TestCatalogBuffer_RemoteSeenSuppressesAddBatch(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, false)
	metrics := []*metricsv1pb.Metric{
		makeGauge("metric_a", "A", ""),
		makeGauge("metric_b", "B", ""),
	}

	candidates := buf.candidatesForBuffer(metrics)
	// Simulate the seen cache reporting metric_a as already seen.
	remotelySeenNames := map[string]bool{"metric_a": true}
	buf.addBatch(candidates, remotelySeenNames)

	items := buf.drain()
	require.Len(t, items, 1)
	assert.Equal(t, "metric_b", items[0].Name)
}

func TestCatalogBuffer_DrainDoesNotMarkSeenBeforeAck(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)
	m := makeGauge("metric_a", "A", "")

	bufAdd(buf, m)
	drained := buf.drain()
	require.Len(t, drained, 1)

	// Simulate a failed flush (no markFlushed call). Metric should still be re-bufferable.
	bufAdd(buf, m)
	drainedAgain := buf.drain()
	require.Len(t, drainedAgain, 1)
	assert.Equal(t, "metric_a", drainedAgain[0].Name)
}

func TestCatalogBuffer_RequeueRestoresFailedFlushItems(t *testing.T) {
	buf := newCatalogBuffer(100, time.Hour, true)

	bufAdd(buf, makeGauge("metric_a", "A", ""), makeGauge("metric_b", "B", ""))
	drained := buf.drain()
	require.Len(t, drained, 2)

	buf.requeue(drained)
	retried := buf.drain()
	require.Len(t, retried, 2)
}

func TestCatalogBuffer_RequeueRespectsMaxSize(t *testing.T) {
	buf := newCatalogBuffer(2, time.Hour, true)
	bufAdd(buf, makeGauge("metric_new", "new", ""))

	// Requeue two failed items while only one slot is available.
	buf.requeue([]db.MetricCatalogItem{
		{Name: "metric_a", Type: "gauge"},
		{Name: "metric_b", Type: "gauge"},
	})

	items := buf.drain()
	require.Len(t, items, 2)
	names := make(map[string]struct{}, len(items))
	for _, item := range items {
		names[item.Name] = struct{}{}
	}

	_, hasNew := names["metric_new"]
	_, hasA := names["metric_a"]
	assert.True(t, hasNew)
	assert.True(t, hasA)
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
