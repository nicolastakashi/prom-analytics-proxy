package ingester

import (
	"context"
	"crypto/md5"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/MichaHoffmann/prom-analytics-proxy/internal/db"
	"github.com/prometheus/prometheus/promql/parser"
)

type QueryIngester struct {
	dbProvider db.Provider
	queriesC   chan db.Query

	mu     sync.RWMutex
	closed bool

	shutdownGracePeriod time.Duration
	ingestTimeout       time.Duration
}

func NewQueryIngester(dbProvider db.Provider, bufferSize int, ingestTimeout, gracePeriod time.Duration) *QueryIngester {
	return &QueryIngester{
		dbProvider:          dbProvider,
		queriesC:            make(chan db.Query, bufferSize),
		ingestTimeout:       ingestTimeout,
		shutdownGracePeriod: gracePeriod,
	}
}

func (i *QueryIngester) Ingest(query db.Query) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if i.closed {
		//TODO(nicolastakashi): expose this to a metric
		log.Printf("closed: dropping query: %v", query)
		return
	}
	select {
	case i.queriesC <- query:
	default:
		//TODO(nicolastakashi): expose this to a metric
		log.Printf("blocked: dropping query: %v", query)
	}
}

func (i *QueryIngester) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			i.mu.Lock()
			defer i.mu.Unlock()
			i.closed = true
			close(i.queriesC)

			i.drainWithGracePeriod()
			return
		case query := <-i.queriesC:
			i.ingest(ctx, query)
		}
	}
}

func (i *QueryIngester) drainWithGracePeriod() {
	log.Printf("draining with grace period: %v", i.shutdownGracePeriod)

	graceCtx, graceCancel := context.WithTimeout(context.Background(), i.shutdownGracePeriod)
	defer graceCancel()
	for query := range i.queriesC {
		select {
		case <-graceCtx.Done():
			log.Printf("grace period expired, discarding remaining queries")
			return
		default:
			i.ingest(graceCtx, query)
		}
	}
}

// TODO(mhoffm): we should ingest in batches probably
func (i *QueryIngester) ingest(ctx context.Context, query db.Query) {
	ingestCtx, ingestCancel := context.WithTimeout(ctx, i.ingestTimeout)
	defer ingestCancel()

	fingerprint := fingerprintFromQuery(query.QueryParam)
	labelMatchers := labelMatchersFromQuery(query.QueryParam)

	query.LabelMatchers = labelMatchers
	query.Fingerprint = fingerprint

	err := i.dbProvider.Insert(ingestCtx, query)
	if err != nil {
		log.Printf("unable to insert query: %v", err)
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
