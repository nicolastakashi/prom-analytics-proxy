package db

import (
	"context"
	"database/sql"
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, q Query) error
	Query(ctx context.Context, query string) (*QueryResult, error)
	Close() error
}

type QueryResult struct {
	Columns []string                 `json:"columns"`
	Data    []map[string]interface{} `json:"data"`
}
