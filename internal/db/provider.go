package db

import (
	"context"
	"database/sql"
	"fmt"
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, queries []Query) error
	Query(ctx context.Context, query string) (*QueryResult, error)
	Close() error
}

type QueryResult struct {
	Columns []string                 `json:"columns"`
	Data    []map[string]interface{} `json:"data"`
}

func GetDbProvider(ctx context.Context, dbProvider DatabaseProvider) (Provider, error) {
	switch dbProvider {
	case ClickHouse:
		return newClickHouseProvider(ctx)
	case PostGreSQL:
		return newPostGreSQLProvider(ctx)
	default:
		return nil, fmt.Errorf("invalid database type %q, only 'clickhouse' and 'postgresql' are supported", dbProvider)
	}
}
