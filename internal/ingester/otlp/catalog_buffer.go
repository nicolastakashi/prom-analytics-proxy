package otlp

import (
	"log/slog"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// catalogBuffer is a thread-safe in-memory buffer of metric catalog entries seen in OTLP traffic.
//
// Deduplication strategy depends on whether a distributed seen cache (e.g. Redis) is configured:
//
//   - No seenCache: an in-memory seen map (name → last flush time) suppresses re-queuing within
//     SeenTTL. Simple and fast, but the state is lost on restart causing a one-time re-flush burst.
//
//   - With seenCache (e.g. Redis): the in-memory seen map is omitted entirely to avoid
//     redundant memory usage. All deduplication is delegated to the distributed cache, which
//     survives restarts and is shared across instances.
//
// In both modes, the pending map deduplicates metrics within a single flush interval.
type catalogBuffer struct {
	mu      sync.Mutex
	pending map[string]db.MetricCatalogItem
	// seen is only populated when seenCache is nil (in-memory deduplication mode).
	seen    map[string]time.Time
	maxSize int
	seenTTL time.Duration

	// seenCache is an optional distributed seen cache (e.g. Redis). When non-nil, the
	// in-memory seen map is not used and all cross-cycle deduplication goes through here.
	seenCache CatalogSeenCache
}

func newCatalogBuffer(maxSize int, seenTTL time.Duration, seenCache CatalogSeenCache) *catalogBuffer {
	b := &catalogBuffer{
		pending:   make(map[string]db.MetricCatalogItem, maxSize),
		maxSize:   maxSize,
		seenTTL:   seenTTL,
		seenCache: seenCache,
	}
	// Only allocate the in-memory seen map when there is no distributed cache.
	if seenCache == nil {
		b.seen = make(map[string]time.Time)
	}
	return b
}

// candidatesForBuffer returns metrics from the request that are not already suppressed.
// When a distributed seenCache is configured, only the pending map is checked here
// (no in-memory seen map). The caller is responsible for the remote seen lookup.
// When no seenCache is configured, the in-memory seen map is also checked.
func (b *catalogBuffer) candidatesForBuffer(metrics []*metricspb.Metric) []*metricspb.Metric {
	b.mu.Lock()
	defer b.mu.Unlock()

	var candidates []*metricspb.Metric
	for _, m := range metrics {
		name := m.GetName()
		if name == "" {
			continue
		}
		if _, inPending := b.pending[name]; inPending {
			continue
		}
		// In-memory seen check only when Redis is not handling deduplication.
		if b.seen != nil {
			if flushedAt, inSeen := b.seen[name]; inSeen && time.Since(flushedAt) < b.seenTTL {
				continue
			}
		}
		candidates = append(candidates, m)
	}
	return candidates
}

// addBatch queues candidates for the next flush, skipping names present in remotelySeenNames.
// When using in-memory deduplication (no seenCache), remotelySeenNames is always nil.
func (b *catalogBuffer) addBatch(candidates []*metricspb.Metric, remotelySeenNames map[string]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, m := range candidates {
		name := m.GetName()
		if remotelySeenNames[name] {
			continue
		}
		if _, inPending := b.pending[name]; inPending {
			continue
		}
		if len(b.pending) >= b.maxSize {
			slog.Debug("ingester.catalog.buffer.full", "dropped_metric", name, "buffer_size", b.maxSize)
			catalogBufferDroppedTotal.Inc()
			continue
		}
		b.pending[name] = db.MetricCatalogItem{
			Name: name,
			Type: otlpTypeToPrometheus(m),
			Help: m.GetDescription(),
			Unit: m.GetUnit(),
		}
		catalogBufferSize.Set(float64(len(b.pending)))
	}
}

// snapshot atomically returns all pending items and clears the pending queue.
// When using in-memory deduplication (no seenCache), flush timestamps are recorded
// in the seen map so re-queuing is suppressed within SeenTTL.
func (b *catalogBuffer) snapshot() []db.MetricCatalogItem {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.pending) == 0 {
		return nil
	}

	out := make([]db.MetricCatalogItem, 0, len(b.pending))
	for name, item := range b.pending {
		out = append(out, item)
		if b.seen != nil {
			b.seen[name] = time.Now()
		}
	}
	b.pending = make(map[string]db.MetricCatalogItem, b.maxSize)
	catalogBufferSize.Set(0)
	return out
}

// otlpTypeToPrometheus maps an OTLP metric data type to the equivalent Prometheus type string
// used in metrics_catalog.
func otlpTypeToPrometheus(m *metricspb.Metric) string {
	switch m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return "gauge"
	case *metricspb.Metric_Sum:
		if m.GetSum().IsMonotonic {
			return "counter"
		}
		return "gauge"
	case *metricspb.Metric_Histogram:
		return "histogram"
	case *metricspb.Metric_ExponentialHistogram:
		return "histogram"
	case *metricspb.Metric_Summary:
		return "summary"
	default:
		return "unknown"
	}
}
