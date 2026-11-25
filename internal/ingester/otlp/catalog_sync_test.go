package otlp

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricsv1pb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

const testSeenTTL = 2 * time.Hour

// TestCatalogSync_SkipsNamesTheSeenCacheHasSeen verifies a metric the seen
// cache reports as recently flushed is not written to the catalog again.
func TestCatalogSync_SkipsNamesTheSeenCacheHasSeen(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	seen.data["seen_metric"] = true
	store := &fakeStore{}
	ing := newCatalogIngester(t, store, seen)

	exportMetrics(t, ing, makeGauge("seen_metric", "", ""), makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())

	assert.Equal(t, [][]string{{"new_metric"}}, store.upsertedNames())
}

// TestCatalogSync_MarksFlushedNamesSeen verifies a successful flush marks
// the flushed metrics in the seen cache for the configured seen TTL.
func TestCatalogSync_MarksFlushedNamesSeen(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	ing := newCatalogIngester(t, &fakeStore{}, seen)

	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())

	assert.True(t, seen.data["new_metric"])
	assert.Equal(t, testSeenTTL, seen.ttl)
}

// TestCatalogSync_RetriesFailedFlushWithoutMarkingSeen verifies a failed
// flush marks nothing seen and writes the same metrics on the next flush.
func TestCatalogSync_RetriesFailedFlushWithoutMarkingSeen(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	store := &fakeStore{err: assert.AnError}
	ing := newCatalogIngester(t, store, seen)

	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())
	assert.False(t, seen.data["new_metric"])

	store.setErr(nil)
	ing.FlushCatalog(context.Background())
	assert.Equal(t, [][]string{{"new_metric"}, {"new_metric"}}, store.upsertedNames())
	assert.True(t, seen.data["new_metric"])
}

// TestCatalogSync_WithoutSeenCache_SkipsNamesWrittenWithinTTL verifies that
// without a seen cache, a metric written to the catalog is not written again
// until the seen TTL has passed.
func TestCatalogSync_WithoutSeenCache_SkipsNamesWrittenWithinTTL(t *testing.T) {
	store := &fakeStore{}
	ing := newCatalogIngester(t, store, nil)

	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())
	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())

	assert.Equal(t, [][]string{{"new_metric"}}, store.upsertedNames())
}

// TestCatalogSync_WithSeenCache_OnlyTheSeenCacheSuppresses verifies that with
// a seen cache, a metric is written again as soon as the seen cache no longer
// holds it, however recently it was written.
func TestCatalogSync_WithSeenCache_OnlyTheSeenCacheSuppresses(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	store := &fakeStore{}
	ing := newCatalogIngester(t, store, seen)

	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())
	delete(seen.data, "new_metric")
	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())

	assert.Equal(t, [][]string{{"new_metric"}, {"new_metric"}}, store.upsertedNames())
}

// TestCatalogSync_FlushesEveryInterval verifies the catalog sync writes its
// buffered metrics every flush interval while it runs.
func TestCatalogSync_FlushesEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeStore{}
		s := newCatalogSyncWith(store, nil, defaultCatalogBufferSize, time.Minute, testSeenTTL)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go s.run(ctx)

		s.add(ctx, []*metricsv1pb.Metric{makeGauge("new_metric", "", "")})
		time.Sleep(time.Minute)
		synctest.Wait()

		assert.Equal(t, [][]string{{"new_metric"}}, store.upsertedNames())
	})
}

// TestCatalogSync_SeenCacheLookupFailure_StillBuffers verifies a metric is
// still written to the catalog when the seen cache cannot be asked about it.
func TestCatalogSync_SeenCacheLookupFailure_StillBuffers(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	seen.hasErr = assert.AnError
	store := &fakeStore{}
	ing := newCatalogIngester(t, store, seen)

	exportMetrics(t, ing, makeGauge("new_metric", "", ""))
	ing.FlushCatalog(context.Background())

	assert.Equal(t, [][]string{{"new_metric"}}, store.upsertedNames())
}

// TestCatalogSync_SeenCacheTimeouts verifies the seen-cache lookup runs under
// a 50ms timeout and marking flushed metrics under a 200ms one.
func TestCatalogSync_SeenCacheTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seen := newFakeCatalogSeenCache()
		ing := newCatalogIngester(t, &fakeStore{}, seen)

		start := time.Now()
		exportMetrics(t, ing, makeGauge("new_metric", "", ""))
		ing.FlushCatalog(context.Background())

		assert.Equal(t, 50*time.Millisecond, seen.hasDeadline.Sub(start))
		assert.Equal(t, 200*time.Millisecond, seen.markDeadline.Sub(start))
	})
}

// TestCatalogSync_RunShutdownClosesSeenCache verifies Run closes the seen
// cache when its context is canceled.
func TestCatalogSync_RunShutdownClosesSeenCache(t *testing.T) {
	seen := newFakeCatalogSeenCache()
	ing := newCatalogIngester(t, &fakeStore{}, seen)
	ing.config.Ingester.OTLP.ListenAddress = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- ing.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	require.NoError(t, <-runDone)

	assert.True(t, seen.closed)
}

// TestNewCatalogSync_Settings verifies the configured flush interval, seen
// TTL and buffer size are applied, and that 0 or a negative value means 30s,
// 1h and 10000.
func TestNewCatalogSync_Settings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured time.Duration
		bufferSize int
		wantFlush  time.Duration
		wantTTL    time.Duration
		wantBuffer int
	}{
		{name: "configured", configured: 2 * time.Minute, bufferSize: 3, wantFlush: 2 * time.Minute, wantTTL: 2 * time.Minute, wantBuffer: 3},
		{name: "zero", wantFlush: 30 * time.Second, wantTTL: time.Hour, wantBuffer: 10000},
		{name: "negative", configured: -time.Minute, bufferSize: -1, wantFlush: 30 * time.Second, wantTTL: time.Hour, wantBuffer: 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Ingester: config.IngesterConfig{CatalogSync: config.CatalogSyncConfig{
				Enabled:       true,
				FlushInterval: tc.configured,
				SeenTTL:       tc.configured,
				BufferSize:    tc.bufferSize,
			}}}

			t.Run("flush interval", func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					store := &fakeStore{}
					s := newCatalogSync(cfg, store)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					go s.run(ctx)
					s.add(ctx, []*metricsv1pb.Metric{makeGauge("new_metric", "", "")})

					time.Sleep(tc.wantFlush - time.Nanosecond)
					synctest.Wait()
					assert.Empty(t, store.upsertedNames(), "flushed before the interval")
					time.Sleep(time.Nanosecond)
					synctest.Wait()
					assert.Len(t, store.upsertedNames(), 1, "not flushed at the interval")
				})
			})

			t.Run("seen TTL", func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					store := &fakeStore{}
					s := newCatalogSync(cfg, store)
					ctx := context.Background()
					m := makeGauge("new_metric", "", "")
					s.add(ctx, []*metricsv1pb.Metric{m})
					s.flush(ctx)

					time.Sleep(tc.wantTTL - time.Nanosecond)
					s.add(ctx, []*metricsv1pb.Metric{m})
					s.flush(ctx)
					assert.Len(t, store.upsertedNames(), 1, "written again within the seen TTL")
					time.Sleep(time.Nanosecond)
					s.add(ctx, []*metricsv1pb.Metric{m})
					s.flush(ctx)
					assert.Len(t, store.upsertedNames(), 2, "not written again after the seen TTL")
				})
			})

			t.Run("buffer size", func(t *testing.T) {
				store := &fakeStore{}
				s := newCatalogSync(cfg, store)
				ctx := context.Background()
				metrics := make([]*metricsv1pb.Metric, tc.wantBuffer+1)
				for i := range metrics {
					metrics[i] = makeGauge(fmt.Sprintf("metric_%d", i), "", "")
				}
				s.add(ctx, metrics)
				s.flush(ctx)
				upserted := store.upsertedNames()
				require.Len(t, upserted, 1)
				assert.Len(t, upserted[0], tc.wantBuffer)
			})
		})
	}
}

// newCatalogIngester returns an ingester with catalog sync enabled, writing
// to store and tracking flushed metrics in seen, or in memory when seen is
// nil.
func newCatalogIngester(t *testing.T, store Store, seen *fakeCatalogSeenCache) *OtlpIngester {
	t.Helper()
	cfg := &config.Config{Ingester: config.IngesterConfig{
		CatalogSync: config.CatalogSyncConfig{Enabled: true, SeenTTL: testSeenTTL},
	}}
	ing, err := NewOtlpIngester(cfg, store)
	require.NoError(t, err)
	var seenCache CatalogSeenCache
	if seen != nil {
		seenCache = seen
	}
	ing.catalog = newCatalogSyncWith(store, seenCache, defaultCatalogBufferSize, defaultCatalogFlushInterval, testSeenTTL)
	return ing
}

func exportMetrics(t *testing.T, ing *OtlpIngester, metrics ...*metricsv1pb.Metric) {
	t.Helper()
	_, err := ing.Export(context.Background(), &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1pb.ResourceMetrics{{
			ScopeMetrics: []*metricsv1pb.ScopeMetrics{{Metrics: metrics}},
		}},
	})
	require.NoError(t, err)
}

// fakeStore answers every usage lookup with no records and records each
// catalog upsert, failing it with err. It is safe for concurrent use.
type fakeStore struct {
	mu      sync.Mutex
	err     error
	upserts [][]db.MetricCatalogItem
}

func (s *fakeStore) GetSeriesMetadataByNames(context.Context, []string, string) ([]models.MetricMetadata, error) {
	return nil, nil
}

func (s *fakeStore) UpsertMetricsCatalog(_ context.Context, items []db.MetricCatalogItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, items)
	return s.err
}

// setErr makes later upserts fail with err.
func (s *fakeStore) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeStore) upsertedNames() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, items := range s.upserts {
		var names []string
		for _, item := range items {
			names = append(names, item.Name)
		}
		out = append(out, names)
	}
	return out
}

// fakeCatalogSeenCache is an in-memory CatalogSeenCache for testing Redis path behaviour.
type fakeCatalogSeenCache struct {
	data   map[string]bool
	ttl    time.Duration // of the last MarkMany
	hasErr error         // returned by HasMany

	hasDeadline, markDeadline time.Time // of the last HasMany, MarkMany
	closed                    bool
}

func newFakeCatalogSeenCache() *fakeCatalogSeenCache {
	return &fakeCatalogSeenCache{data: make(map[string]bool)}
}

func (f *fakeCatalogSeenCache) HasMany(ctx context.Context, names []string) (map[string]bool, error) {
	f.hasDeadline, _ = ctx.Deadline()
	if f.hasErr != nil {
		return nil, f.hasErr
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = f.data[name]
	}
	return out, nil
}

func (f *fakeCatalogSeenCache) MarkMany(ctx context.Context, names []string, ttl time.Duration) error {
	f.markDeadline, _ = ctx.Deadline()
	f.ttl = ttl
	for _, name := range names {
		f.data[name] = true
	}
	return nil
}

func (f *fakeCatalogSeenCache) Close() error {
	f.closed = true
	return nil
}
