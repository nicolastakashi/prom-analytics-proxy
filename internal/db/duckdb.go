package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/marcboeker/go-duckdb"
	"github.com/oklog/ulid/v2"
)

const flushStmt = `COPY (SELECT * FROM queries) TO '%s/%s.parquet' (FORMAT 'parquet');`

type dbProvider struct {
	dbDir string

	mu sync.RWMutex

	curDB *sql.DB
}

func NewDBDuckProvider(ctx context.Context, dbDir string) (*dbProvider, error) {
	if err := os.MkdirAll(dbDir, os.ModePerm); err != nil {
		return nil, err
	}

	curDB, err := connectToDb(ctx)
	if err != nil {
		return nil, err
	}
	return &dbProvider{dbDir: dbDir, curDB: curDB}, nil
}

func (dbp *dbProvider) WithDB(f func(db *sql.DB)) {
	dbp.mu.RLock()
	defer dbp.mu.RUnlock()

	f(dbp.curDB)
}

func (dbp *dbProvider) NextDB() error {
	dbp.mu.Lock()
	defer dbp.mu.Unlock()

	// flush into parquet file
	log.Println("flushing parquet file from DB")

	if _, err := dbp.curDB.ExecContext(context.Background(), fmt.Sprintf(flushStmt, dbp.dbDir, ulid.Make())); err != nil {
		log.Println("flushing parquet file from DB failed: ", err)
	}

	newDB, err := connectToDb(context.Background())
	if err != nil {
		return err
	}
	dbp.curDB = newDB

	return nil
}

func (dbp *dbProvider) Close() {
	dbp.mu.Lock()
	defer dbp.mu.Unlock()

	// flush into parquet file
	log.Println("flushing parquet file from DB before closing")

	if _, err := dbp.curDB.ExecContext(context.Background(), fmt.Sprintf(flushStmt, dbp.dbDir, ulid.Make())); err != nil {
		log.Println("flushing parquet file from DB before closing failed: ", err)
	}

	dbp.curDB.Close()
}

func connectToDb(ctx context.Context) (*sql.DB, error) {
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		bootQueries := []string{
			"INSTALL 'json'",
			"LOAD 'json'",
			"CREATE TABLE IF NOT EXISTS queries (ts TIMESTAMP, fingerprint VARCHAR, query_param VARCHAR, time_param TIMESTAMP, label_matchers_list JSON, duration_ms BIGINT, status_code INT, body_size_bytes BIGINT)",
		}

		for _, query := range bootQueries {
			_, err := execer.ExecContext(ctx, query, nil)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("unable to open DB connector: %v", err)
	}

	return sql.OpenDB(connector), nil
}
