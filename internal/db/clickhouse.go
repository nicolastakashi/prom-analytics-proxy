package db

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type ClickHouseProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

const createClickHouseTableStmt = `
CREATE TABLE IF NOT EXISTS queries (
    TS DateTime,
    QueryParam String,
    TimeParam DateTime,
    Duration UInt64,
    StatusCode Int32,
    BodySize Int32,
    Fingerprint String,
    LabelMatchers Nested (
        key String,
        value String
    ),
	Type String,
	Step Float64,
	Start DateTime,
	End DateTime,
	TotalQueryableSamples Int32,
	PeakSamples Int32
) ENGINE = MergeTree()
ORDER BY TS;
`

const insertQueryStmt = `INSERT INTO queries VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

type ClickHouseProviderConfig struct {
	Addr        string
	DiamTimeout time.Duration
}

func NewClickHouseProvider(ctx context.Context, config ClickHouseProviderConfig) (Provider, error) {
	db := clickhouse.OpenDB(&clickhouse.Options{
		Addr:        strings.Split(config.Addr, ","),
		DialTimeout: config.DiamTimeout,
	})
	if _, err := db.ExecContext(ctx, createClickHouseTableStmt); err != nil {
		return nil, err
	}

	return &ClickHouseProvider{
		db: db,
	}, nil
}

func (c *ClickHouseProvider) WithDB(f func(db *sql.DB)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f(c.db)
}

func (c *ClickHouseProvider) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db.Close()
}

func (c *ClickHouseProvider) Insert(ctx context.Context, query Query) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var keys, values []string
	for _, matcher := range query.LabelMatchers {
		for key, value := range matcher {
			keys = append(keys, key)
			values = append(values, value)
		}
	}

	_, err := c.db.ExecContext(ctx, insertQueryStmt,
		query.TS,
		query.QueryParam,
		query.TimeParam,
		query.Duration.Milliseconds(), // Store Duration as nanoseconds
		query.StatusCode,
		query.BodySize,
		query.Fingerprint,
		keys,
		values,
		query.Type,
		query.Step,
		query.Start,
		query.End,
		query.TotalQueryableSamples,
		query.PeakSamples,
	)

	return err
}

func (c *ClickHouseProvider) Query(ctx context.Context, query string) (*sql.Rows, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db.QueryContext(ctx, query)
}
