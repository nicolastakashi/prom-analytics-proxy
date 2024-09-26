package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

type PostGreSQLProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

type PostGreSQLProviderConfig struct {
	Addr        string
	Port        int
	DiamTimeout time.Duration
	User        string
	Password    string
	Database    string
	SSLMode     string
}

var (
	postgresConfig PostGreSQLProviderConfig = PostGreSQLProviderConfig{}
)

const (
	createPostgresTableStmt = `
		CREATE TABLE IF NOT EXISTS queries (
			ts TIMESTAMP,
			queryParam TEXT,
			timeParam TIMESTAMP,
			duration BIGINT,
			statusCode SMALLINT,
			bodySize INTEGER,
			fingerprint TEXT,
			labelMatchers JSONB,
			type TEXT,
			step DOUBLE PRECISION,
			start TIMESTAMP,
			"end" TIMESTAMP,
			totalQueryableSamples INTEGER,
			peakSamples INTEGER
		);`
)

func RegisterPostGreSQLFlags(flagSet *flag.FlagSet) {
	flagSet.DurationVar(&postgresConfig.DiamTimeout, "postgresql-dial-timeout", 5*time.Second, "Timeout to dial postgresql.")
	flagSet.StringVar(&postgresConfig.Addr, "postgresql-addr", "localhost", "Address of the postgresql server.")
	flagSet.IntVar(&postgresConfig.Port, "postgresql-port", 5432, "Port of the postgresql server.")
	flagSet.StringVar(&postgresConfig.User, "postgresql-user", os.Getenv("POSTGRESQL_USER"), "Username for the postgresql server, can also be set via POSTGRESQL_USER env var.")
	flagSet.StringVar(&postgresConfig.Password, "postgresql-password", os.Getenv("POSTGRESQL_PASSWORD"), "Password for the postgresql server, can also be set via POSTGRESQL_PASSWORD env var.")
	flagSet.StringVar(&postgresConfig.Database, "postgresql-database", os.Getenv("POSTGRESQL_DATABASE"), "Database for the postgresql server, can also be set via POSTGRESQL_DATABASE env var.")
	flagSet.StringVar(&postgresConfig.SSLMode, "postgresql-sslmode", "disable", "SSL mode for the postgresql server.")
}

func newPostGreSQLProvider(ctx context.Context) (Provider, error) {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+"password=%s dbname=%s sslmode=disable",
		postgresConfig.Addr, postgresConfig.Port, postgresConfig.User, postgresConfig.Password, postgresConfig.Database)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgresql connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping postgresql: %w", err)
	}

	if _, err := db.ExecContext(ctx, createPostgresTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return &PostGreSQLProvider{
		db: db,
	}, nil
}

func (p *PostGreSQLProvider) WithDB(f func(db *sql.DB)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	f(p.db)
}

func (p *PostGreSQLProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.db.Close()
}

func (p *PostGreSQLProvider) Insert(ctx context.Context, queries []Query) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	query := `
		INSERT INTO queries (
			ts, queryParam, timeParam, duration, statusCode, bodySize, fingerprint, labelMatchers, type, step, start, "end", totalQueryableSamples, peakSamples
		) VALUES `

	values := make([]interface{}, 0, len(queries)*14)
	placeholders := ""

	for i, q := range queries {
		labelMatchersJSON, err := json.Marshal(q.LabelMatchers)
		if err != nil {
			return fmt.Errorf("failed to marshal label matchers: %w", err)
		}

		// This is required to build a string like
		// "($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14), ($15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)"
		placeholders += fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			i*14+1, i*14+2, i*14+3, i*14+4, i*14+5, i*14+6, i*14+7, i*14+8, i*14+9, i*14+10, i*14+11, i*14+12, i*14+13, i*14+14,
		)

		if i < len(queries)-1 {
			placeholders += ", "
		}

		values = append(values,
			q.TS,
			q.QueryParam,
			q.TimeParam,
			q.Duration,
			q.StatusCode,
			q.BodySize,
			q.Fingerprint,
			labelMatchersJSON,
			q.Type,
			q.Step,
			q.Start,
			q.End,
			q.TotalQueryableSamples,
			q.PeakSamples,
		)
	}

	query += placeholders

	_, err := p.db.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("failed to execute insert query: %w", err)
	}

	return nil
}

func (p *PostGreSQLProvider) Query(ctx context.Context, query string) (*QueryResult, error) {
	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
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

	return &QueryResult{
		Columns: columns,
		Data:    data,
	}, nil
}
