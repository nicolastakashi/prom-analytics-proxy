package ingester

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

type QueryIngester struct {
	db       *sql.DB
	queriesC chan Query

	mu     sync.RWMutex
	closed bool

	shutdownGracePeriod time.Duration
	ingestTimeout       time.Duration
}

type Query struct {
	TS         time.Time
	QueryParam string
	TimeParam  time.Time
	Duration   time.Duration
}

func NewQueryIngester(db *sql.DB, bufferSize int, ingestTimeout, gracePeriod time.Duration) *QueryIngester {
	return &QueryIngester{
		db:                  db,
		queriesC:            make(chan Query, bufferSize),
		ingestTimeout:       ingestTimeout,
		shutdownGracePeriod: gracePeriod,
	}
}

func (i *QueryIngester) Ingest(query Query) {
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

const insertQuery = `INSERT INTO queries VALUES (?, ?, ?, ?, ?, ?)`

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
func (i *QueryIngester) ingest(ctx context.Context, query Query) {
	ingestCtx, ingestCancel := context.WithTimeout(ctx, i.ingestTimeout)
	defer ingestCancel()

	fingerprint := fingerprintFromQuery(query.QueryParam)
	labelMatchers := labelMatchersFromQuery(query.QueryParam)

	labelMatchersBlob, err := json.Marshal(labelMatchers)
	if err != nil {
		log.Printf("unable to marshal label matchers: %v", err)
		return
	}

	_, err = i.db.ExecContext(
		ingestCtx,
		insertQuery,
		query.TS,
		fingerprint,
		query.QueryParam,
		query.TimeParam,
		labelMatchersBlob,
		query.Duration.Milliseconds())

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
