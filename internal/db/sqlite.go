package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	_ "modernc.org/sqlite"
)

type SQLiteProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

const (
	createSqliteTableStmt = `
		CREATE TABLE IF NOT EXISTS queries (
			ts TIMESTAMP,
			queryParam TEXT,
			timeParam TIMESTAMP,
			duration INTEGER,
			statusCode INTEGER,
			bodySize INTEGER,
			fingerprint TEXT,
			labelMatchers TEXT,
			type TEXT,
			step REAL,
			start TIMESTAMP,
			"end" TIMESTAMP,
			totalQueryableSamples INTEGER,
			peakSamples INTEGER
		);
	`
	configureSqliteStmt = `
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = normal;
		PRAGMA journal_size_limit = 6144000;
	`

	createSqliteRulesUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS RulesUsage (
			serie TEXT NOT NULL,
			group_name TEXT NOT NULL,
			name TEXT NOT NULL,
			expression TEXT NOT NULL,
			kind TEXT NOT NULL,
			labels TEXT,
			created_at DATETIME NOT NULL
		);
	`
)

func RegisterSqliteFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&config.DefaultConfig.Database.SQLite.DatabasePath, "sqlite-database-path", "prom-analytics-proxy.db", "Path to the sqlite database.")
}

func newSqliteProvider(ctx context.Context) (Provider, error) {
	db, err := otelsql.Open("sqlite", config.DefaultConfig.Database.SQLite.DatabasePath, otelsql.WithAttributes(semconv.DBSystemSqlite))
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	if _, err := db.ExecContext(ctx, createSqliteTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	if _, err := db.Exec(configureSqliteStmt); err != nil {
		return nil, fmt.Errorf("failed to configure sqlite database: %w)", err)
	}

	if _, err := db.ExecContext(ctx, createSqliteRulesUsageTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create rules usage table: %w", err)
	}

	return &SQLiteProvider{
		db: db,
	}, nil
}

func (p *SQLiteProvider) Close() error {
	return p.db.Close()
}

func (p *SQLiteProvider) WithDB(f func(db *sql.DB)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	f(p.db)
}

func (p *SQLiteProvider) Insert(ctx context.Context, queries []Query) error {
	p.mu.Lock()
	defer p.mu.Unlock()
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

		placeholders += "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

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

func (p *SQLiteProvider) Query(ctx context.Context, query string) (*QueryResult, error) {
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

func (p *SQLiteProvider) QueryShortCuts() []QueryShortCut {
	return commonQueryShortCuts
}

func (p *SQLiteProvider) GetQueriesBySerieName(
	ctx context.Context,
	serieName string,
	page int,
	pageSize int) (*PagedResult, error) {

	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // 30 days ago

	// Format timestamps for SQLite (YYYY-MM-DD HH:MM:SS)
	startTimeFormatted := startTime.Format("2006-01-02 15:04:05")
	endTimeFormatted := endTime.Format("2006-01-02 15:04:05")

	totalCount, err := p.getQueriesBySerieNameTotalCount(ctx, serieName, startTimeFormatted, endTimeFormatted)
	if err != nil {
		return nil, err
	}

	// Calculate total pages
	totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))

	data, err := p.getQueriesBySerieNameQueryData(ctx, serieName, startTimeFormatted, endTimeFormatted, page, pageSize)
	if err != nil {
		return nil, err
	}

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       data,
	}, nil
}

func (p *SQLiteProvider) getQueriesBySerieNameTotalCount(ctx context.Context, serieName, startTime, endTime string) (int, error) {
	countQuery := `
		SELECT COUNT(DISTINCT queryParam) AS TotalCount
		FROM queries
		WHERE
			json_extract(labelMatchers, '$[0].__name__') = ?
			AND ts BETWEEN ? AND ?;
	`

	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, serieName, startTime, endTime).Scan(&totalCount)
	if err != nil {
		return 0, fmt.Errorf("failed to count rows: %w", err)
	}

	return totalCount, nil
}

func (p *SQLiteProvider) getQueriesBySerieNameQueryData(ctx context.Context, serieName, startTime, endTime string, page, pageSize int) ([]QueriesBySerieNameResult, error) {
	query := `
		SELECT
			queryParam AS query,
			AVG(duration) AS avgDuration,
			AVG(peakSamples) AS avgPeakySamples,
			MAX(peakSamples) AS maxPeakSamples
		FROM
			queries
		WHERE
			json_extract(labelMatchers, '$[0].__name__') = ?
			AND ts BETWEEN ? AND ?
		GROUP BY
			queryParam
		ORDER BY
			avgDuration DESC
		LIMIT ? OFFSET ?;
	`

	rows, err := p.db.QueryContext(ctx, query, serieName, startTime, endTime, pageSize, page*pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	data := []QueriesBySerieNameResult{}
	for rows.Next() {
		var r QueriesBySerieNameResult
		if err := rows.Scan(&r.QueryParam, &r.AvgDuration, &r.AvgPeakySamples, &r.MaxPeakSamples); err != nil {
			return nil, fmt.Errorf("unable to scan row: %w", err)
		}
		data = append(data, r)
	}

	return data, nil
}

func (p *SQLiteProvider) InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error {
	// Begin a transaction
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		// Rollback the transaction if it's not committed
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Prepare the SQL statement for insertion
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO RulesUsage (
			serie, group_name, name, expression, kind, labels, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	createdAt := time.Now()

	// Iterate over the rulesUsage slice and execute the insert statement
	for _, rule := range rulesUsage {
		// Convert the Labels field to JSON or a comma-separated string
		labelsJSON, err := json.Marshal(rule.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels to JSON: %w", err)
		}

		// Execute the insert statement
		_, err = stmt.ExecContext(ctx,
			rule.Serie,
			rule.GroupName,
			rule.Name,
			rule.Expression,
			rule.Kind,
			string(labelsJSON), // Pass the JSON string representation
			createdAt,
		)
		if err != nil {
			return fmt.Errorf("failed to execute insert statement: %w", err)
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (p *SQLiteProvider) GetRulesUsage(ctx context.Context, serie string, kind string, page, pageSize int) (*PagedResult, error) {
	offset := (page - 1) * pageSize

	// Query for total count
	countQuery := `
		SELECT COUNT(DISTINCT name || group_name)
		FROM RulesUsage
		WHERE serie = ? 
		AND kind = ?
		AND created_at >= datetime('now', '-30 days');
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, serie, kind).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	// Calculate total pages
	totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))

	// Query for paginated results
	query := `
		WITH latest_rules AS (
			SELECT 
				serie,
				group_name,
				name,
				expression,
				kind,
				labels,
				created_at,
				ROW_NUMBER() OVER (PARTITION BY serie, name ORDER BY created_at DESC) AS rank
			FROM RulesUsage
			WHERE serie = ? AND kind = ? AND created_at >= datetime('now', '-30 days')
		)
		SELECT 
			serie,
			group_name,
			name,
			expression,
			kind,
			labels,
			created_at
		FROM latest_rules
		WHERE rank = 1
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?;
	`

	rows, err := p.db.QueryContext(ctx, query, serie, kind, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query rules usage: %w", err)
	}
	defer rows.Close()

	results := []RulesUsage{}
	for rows.Next() {
		var (
			serie      string
			groupName  string
			name       string
			expression string
			kind       string
			labelsJSON string
			createdAt  time.Time
		)

		// Scan each row
		if err := rows.Scan(&serie, &groupName, &name, &expression, &kind, &labelsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Parse JSON labels
		var labels []string
		if labelsJSON != "" {
			if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
			}
		}

		// Append to results
		results = append(results, RulesUsage{
			Serie:      serie,
			GroupName:  groupName,
			Name:       name,
			Expression: expression,
			Kind:       kind,
			Labels:     labels,
			CreatedAt:  createdAt,
		})
	}

	// Check for errors after iteration
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return &PagedResult{
		Total:      len(results),
		TotalPages: totalPages,
		Data:       results,
	}, nil
}
