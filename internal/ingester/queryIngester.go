package ingester

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

type QueryIngester struct {
	db      *sql.DB
	queries chan Query
}

type Query struct {
	TS         time.Time
	QueryParam string
	TimeParam  time.Time
	Duration   time.Duration
}

func NewQueryIngester(db *sql.DB, bufferSize int) *QueryIngester {
	return &QueryIngester{
		db:      db,
		queries: make(chan Query, bufferSize),
	}
}

func (i *QueryIngester) Ingest(ctx context.Context, query Query) {
	select {
	case i.queries <- query:
	case <-ctx.Done():
		log.Printf("Ingestion stopped, closing queries channel")
		close(i.queries)
	default:
		//TODO:(nicolastakashi) expose this to a metric
		log.Printf("dropping query: %v", query)
	}
}

const insertQuery = `INSERT INTO queries VALUES (?, ?, ?, ?, ?, ?)`

func (i *QueryIngester) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("stopping query ingester")
			return
		case query, ok := <-i.queries:
			if !ok {
				log.Printf("channel closed, stopping query ingester")
				return
			}
			fingerprint := fingerprintFromQuery(query.QueryParam)
			labelMatchers := labelMatchersFromQuery(query.QueryParam)

			labelMatchersBlob, err := json.Marshal(labelMatchers)
			if err != nil {
				log.Printf("unable to marshal label matchers: %v", err)
				continue
			}

			_, err = i.db.ExecContext(
				ctx,
				insertQuery,
				query.TS,
				fingerprint,
				query.QueryParam,
				query.TimeParam,
				labelMatchersBlob,
				query.Duration.Milliseconds())

			if err != nil {
				log.Printf("unable to insert query: %v", err)
			}
		}
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
