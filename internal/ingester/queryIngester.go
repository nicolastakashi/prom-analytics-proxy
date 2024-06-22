package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

type QueryIngester struct {
	db      *sql.DB
	queries chan Query
}

type Query struct {
	TS            time.Time
	QueryParam    string
	TimeParam     time.Time
	LabelMatchers []map[string]string
	Duration      time.Duration
	Fingerprint   string
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

			labelMatchersBlob, err := json.Marshal(query.LabelMatchers)
			if err != nil {
				log.Printf("unable to marshal label matchers: %v", err)
				continue
			}

			_, err = i.db.ExecContext(
				ctx,
				insertQuery,
				query.TS,
				query.Fingerprint,
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
