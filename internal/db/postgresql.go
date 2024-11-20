package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type PostGreSQLProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

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
	flagSet.DurationVar(&config.DefaultConfig.Database.PostgreSQL.DialTimeout, "postgresql-dial-timeout", 5*time.Second, "Timeout to dial postgresql.")
	flagSet.StringVar(&config.DefaultConfig.Database.PostgreSQL.Addr, "postgresql-addr", "localhost", "Address of the postgresql server.")
	flagSet.IntVar(&config.DefaultConfig.Database.PostgreSQL.Port, "postgresql-port", 5432, "Port of the postgresql server.")
	flagSet.StringVar(&config.DefaultConfig.Database.PostgreSQL.User, "postgresql-user", os.Getenv("POSTGRESQL_USER"), "Username for the postgresql server, can also be set via POSTGRESQL_USER env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.PostgreSQL.Password, "postgresql-password", os.Getenv("POSTGRESQL_PASSWORD"), "Password for the postgresql server, can also be set via POSTGRESQL_PASSWORD env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.PostgreSQL.Database, "postgresql-database", os.Getenv("POSTGRESQL_DATABASE"), "Database for the postgresql server, can also be set via POSTGRESQL_DATABASE env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.PostgreSQL.SSLMode, "postgresql-sslmode", "disable", "SSL mode for the postgresql server.")
}

func newPostGreSQLProvider(ctx context.Context) (Provider, error) {
	postgresConfig := config.DefaultConfig.Database.PostgreSQL

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+"password=%s dbname=%s sslmode=disable",
		postgresConfig.Addr, postgresConfig.Port, postgresConfig.User, postgresConfig.Password, postgresConfig.Database)

	db, err := otelsql.Open("postgres", psqlInfo, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
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
			q.Duration.Milliseconds(),
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
	if err := ValidateSQLQuery(query); err != nil {
		return nil, fmt.Errorf("query not allowed: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	data := []map[string]interface{}{}
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

func (p *PostGreSQLProvider) QueryShortCuts() []QueryShortCut {
	return commonQueryShortCuts
}

func (p *PostGreSQLProvider) GetQueriesBySerieName(
	ctx context.Context,
	serieName string,
	page int,
	pageSize int) (*PagedResult, error) {

	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // 30 days ago

	totalCount, err := p.getQueriesBySerieNameTotalCount(ctx, serieName, startTime, endTime)
	if err != nil {
		return nil, err
	}

	// Calculate total pages
	totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))

	data, err := p.getQueriesBySerieNameQueryData(ctx, serieName, startTime, endTime, page, pageSize)
	if err != nil {
		return nil, err
	}

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       data,
	}, nil
}

func (p *PostGreSQLProvider) getQueriesBySerieNameTotalCount(ctx context.Context, serieName string, startTime, endTime time.Time) (int, error) {
	countQuery := `
		SELECT COUNT(DISTINCT queryParam) AS TotalCount
		FROM queries
		WHERE
			labelMatchers @> $1::jsonb
			AND ts BETWEEN $2 AND $3;
	`

	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, fmt.Sprintf(`[{"__name__": "%s"}]`, serieName), startTime, endTime).Scan(&totalCount)
	if err != nil {
		return 0, fmt.Errorf("failed to count rows: %w", err)
	}

	return totalCount, nil
}

func (p *PostGreSQLProvider) getQueriesBySerieNameQueryData(ctx context.Context, serieName string, startTime, endTime time.Time, page, pageSize int) ([]QueriesBySerieNameResult, error) {
	query := `
		SELECT
			queryParam AS Query,
			AVG(duration) AS AvgDuration,
			AVG(peakSamples) AS AvgPeakSamples,
			MAX(peakSamples) AS MaxPeakSamples
		FROM
			queries
		WHERE
			labelMatchers @> $1::jsonb
			AND ts BETWEEN $2 AND $3
		GROUP BY
			queryParam
		ORDER BY
			AvgDuration DESC
		LIMIT $4 OFFSET $5;
	`

	rows, err := p.db.QueryContext(ctx, query, fmt.Sprintf(`[{"__name__": "%s"}]`, serieName), startTime, endTime, pageSize, page*pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	var data []QueriesBySerieNameResult
	for rows.Next() {
		var r QueriesBySerieNameResult
		if err := rows.Scan(&r.QueryParam, &r.AvgDuration, &r.AvgPeakySamples, &r.MaxPeakSamples); err != nil {
			return nil, fmt.Errorf("unable to scan row: %w", err)
		}
		data = append(data, r)
	}

	return data, nil
}
