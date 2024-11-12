package ingester

import (
	"context"
	"crypto/md5"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/prometheus/promql/parser"
	"go.opentelemetry.io/otel"
)

type QueryIngester struct {
	dbProvider db.Provider
	queriesC   chan db.Query

	mu     sync.RWMutex
	closed bool

	shutdownGracePeriod time.Duration
	ingestTimeout       time.Duration
	batchSize           int
	batchFlushInterval  time.Duration
}

type QueryIngesterOption func(*QueryIngester)

func WithBufferSize(bufferSize int) QueryIngesterOption {
	return func(qi *QueryIngester) {
		qi.queriesC = make(chan db.Query, bufferSize)
	}
}

func WithIngestTimeout(timeout time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) {
		qi.ingestTimeout = timeout
	}
}

func WithShutdownGracePeriod(gracePeriod time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) {
		qi.shutdownGracePeriod = gracePeriod
	}
}

func WithBatchSize(batchSize int) QueryIngesterOption {
	return func(qi *QueryIngester) {
		qi.batchSize = batchSize
	}
}

func WithBatchFlushInterval(interval time.Duration) QueryIngesterOption {
	return func(qi *QueryIngester) {
		qi.batchFlushInterval = interval
	}
}

func NewQueryIngester(dbProvider db.Provider, opts ...QueryIngesterOption) *QueryIngester {
	qi := &QueryIngester{
		dbProvider: dbProvider,
	}

	for _, opt := range opts {
		opt(qi)
	}

	return qi
}

func (i *QueryIngester) Ingest(query db.Query) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if i.closed {
		//TODO(nicolastakashi): expose this to a metric
		slog.Error(fmt.Sprintf("closed: dropping query: %v", query))
		return
	}
	select {
	case i.queriesC <- query:
	default:
		//TODO(nicolastakashi): expose this to a metric
		slog.Error(fmt.Sprintf("blocked: dropping query: %v", query))
	}
}

func (i *QueryIngester) Run(ctx context.Context) {
	batch := make([]db.Query, 0, i.batchSize)
	ticker := time.NewTicker(i.batchFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			i.mu.Lock()
			defer i.mu.Unlock()
			i.closed = true
			close(i.queriesC)

			i.drainWithGracePeriod(batch)
			return
		case query := <-i.queriesC:
			query.Fingerprint = fingerprintFromQuery(query.QueryParam)
			query.LabelMatchers = labelMatchersFromQuery(query.QueryParam)

			batch = append(batch, query)
			if len(batch) >= i.batchSize {
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
	slog.Debug(fmt.Sprintf("draining with grace period: %v", i.shutdownGracePeriod))

	graceCtx, graceCancel := context.WithTimeout(context.Background(), i.shutdownGracePeriod)
	defer graceCancel()
	for query := range i.queriesC {
		batch = append(batch, query)
		if len(batch) >= i.batchSize {
			i.ingest(graceCtx, batch)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		i.ingest(graceCtx, batch)
	}
}

func (i *QueryIngester) ingest(ctx context.Context, queries []db.Query) {
	ingestCtx, ingestCancel := context.WithTimeout(ctx, i.ingestTimeout)
	defer ingestCancel()

	traceContext, span := otel.Tracer("query-ingester").Start(ingestCtx, "ingest")
	defer span.End()

	err := i.dbProvider.Insert(traceContext, queries)
	if err != nil {
		slog.Error("unable to insert query", "err", err)
		return
	}
}

func fingerprintFromQuery(query string) string {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return ""
	}

	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			for _, m := range n.LabelMatchers {
				if m.Name != "__name__" {
					m.Value = "MASKED"
				}
			}
		}
		return nil
	})
	return fmt.Sprintf("%x", (md5.Sum([]byte(expr.String()))))
}

func labelMatchersFromQuery(query string) []map[string]string {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return nil
	}
	res := make([]map[string]string, 0)
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			v := make(map[string]string, 0)
			for _, m := range n.LabelMatchers {
				v[m.Name] = m.Value
			}
			res = append(res, v)
		}
		return nil
	})
	return res
}
