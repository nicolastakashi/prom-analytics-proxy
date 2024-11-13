package db

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
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

func RegisterClickHouseFlags(flagSet *flag.FlagSet) {
	flagSet.DurationVar(&config.DefaultConfig.Database.ClickHouse.DialTimeout, "clickhouse-dial-timeout", 5*time.Second, "Timeout to dial clickhouse.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Addr, "clickhouse-addr", "localhost:9000", "Address of the clickhouse server, comma separated for multiple servers.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Database, "clickhouse-database", "default", "Database for the clickhouse server, can also be set via CLICKHOUSE_DATABASE env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Username, "clickhouse-username", os.Getenv("CLICKHOUSE_USER"), "Username for the clickhouse server, can also be set via CLICKHOUSE_USER env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Password, "clickhouse-password", os.Getenv("CLICKHOUSE_PASSWORD"), "Password for the clickhouse server, can also be set via CLICKHOUSE_PASSWORD env var.")
}

func newClickHouseProvider(ctx context.Context) (Provider, error) {
	config := config.DefaultConfig.Database.ClickHouse
	opts := &clickhouse.Options{
		Addr:        strings.Split(config.Addr, ","),
		DialTimeout: config.DialTimeout,
	}

	if config.Auth.Username != "" {
		opts.Auth = config.Auth
	}

	if config.Auth.Password != "" {
		opts.Auth.Password = config.Auth.Password
	}

	if config.Auth.Database != "" {
		opts.Auth.Database = config.Auth.Database
	}

	db := clickhouse.OpenDB(opts)
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

func (c *ClickHouseProvider) Insert(ctx context.Context, queries []Query) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	args := make([]interface{}, 0, len(queries)*15)

	for _, query := range queries {
		keys := make([]string, 0, len(query.LabelMatchers))
		values := make([]string, 0, len(query.LabelMatchers))
		for _, matcher := range query.LabelMatchers {
			for key, value := range matcher {
				keys = append(keys, key)
				values = append(values, value)
			}
		}

		args = append(args,
			query.TS,
			query.QueryParam,
			query.TimeParam,
			query.Duration.Milliseconds(), // Store Duration as milliseconds
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
	}

	stmt := fmt.Sprintf("INSERT INTO queries VALUES %s", strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", len(queries)-1)+"(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	_, err := c.db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("unable to execute batch insert: %w", err)
	}

	return nil
}

func (c *ClickHouseProvider) Query(ctx context.Context, query string) (*QueryResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if err := ValidateSQLQuery(query); err != nil {
		return nil, fmt.Errorf("query not allowed: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("unable to query clickhouse: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("unable to get columns: %w", err)
	}

	var data []map[string]interface{}
	for rows.Next() {
		columnPointers := make([]interface{}, len(columns))
		columnValues := make([]interface{}, len(columns))
		for i := range columnValues {
			columnPointers[i] = &columnValues[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, fmt.Errorf("unable to scan row: %w", err)
		}

		rowMap := make(map[string]interface{})
		for i, colName := range columns {
			var v interface{}
			switch columnValues[i].(type) {
			case []uint8:
				v = string(columnValues[i].([]uint8))
			case []string:
				v = columnValues[i].([]string)
			case nil:
				v = nil
			default:
				v = columnValues[i]
			}
			rowMap[colName] = v
		}

		data = append(data, rowMap)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return &QueryResult{
		Columns: columns,
		Data:    data,
	}, nil
}

func (p *ClickHouseProvider) QueryShortCuts() []QueryShortCut {
	return commonQueryShortCuts
}
