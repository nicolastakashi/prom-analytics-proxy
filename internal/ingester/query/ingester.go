package query

import (
	"context"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
)

type QueryIngester struct {
	dbProvider          db.Provider
	queriesC            chan db.Query
	closed              bool
	ingestTimeout       time.Duration
	shutdownGracePeriod time.Duration
	batchSize           int
	batchFlushInterval  time.Duration
}

type QueryIngesterOption func(*QueryIngester)

func WithBufferSize(size int) QueryIngesterOption {
	return func(qi *QueryIngester) { qi.queriesC = make(chan db.Query, size) }
}
func WithIngestTimeout(d time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) { qi.ingestTimeout = d }
}
func WithShutdownGracePeriod(d time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) { qi.shutdownGracePeriod = d }
}
func WithBatchSize(n int) QueryIngesterOption { return func(qi *QueryIngester) { qi.batchSize = n } }
func WithBatchFlushInterval(d time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) { qi.batchFlushInterval = d }
}

func NewQueryIngester(dbProvider db.Provider, opts ...QueryIngesterOption) *QueryIngester {
	qi := &QueryIngester{dbProvider: dbProvider, queriesC: make(chan db.Query)}
	for _, o := range opts {
		o(qi)
	}
	return qi
}

func (i *QueryIngester) Ingest(query db.Query) {
	if i.closed {
		return
	}
	select {
	case i.queriesC <- query:
	default:
	}
}

func (i *QueryIngester) Run(ctx context.Context) {
	ticker := time.NewTicker(i.batchFlushInterval)
	defer ticker.Stop()
	batch := make([]db.Query, 0, i.batchSize)

	for {
		select {
		case <-ctx.Done():
			i.drainWithGracePeriod(batch)
			return
		case q := <-i.queriesC:
			batch = append(batch, q)
			if i.batchSize > 0 && len(batch) >= i.batchSize {
				i.ingest(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				i.ingest(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

func (i *QueryIngester) drainWithGracePeriod(batch []db.Query) {
	if i.shutdownGracePeriod <= 0 {
		if len(batch) > 0 {
			_ = i.dbProvider.Insert(context.Background(), batch)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), i.shutdownGracePeriod)
	defer cancel()
	if len(batch) > 0 {
		_ = i.dbProvider.Insert(ctx, batch)
	}
}

func (i *QueryIngester) ingest(ctx context.Context, queries []db.Query) {
	cctx := ctx
	if i.ingestTimeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, i.ingestTimeout)
		defer cancel()
	}
	_ = i.dbProvider.Insert(cctx, queries)
}
