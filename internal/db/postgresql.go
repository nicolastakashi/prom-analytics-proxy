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

	createPostgresRulesUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS RulesUsage (
			serie TEXT NOT NULL,
			group_name TEXT NOT NULL,
			name TEXT NOT NULL,
			expression TEXT NOT NULL,
			kind TEXT NOT NULL,
			labels JSONB, -- JSONB is used for better performance with JSON data
			created_at TIMESTAMP NOT NULL
		);`

	createPostgresDashboardUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS DashboardUsage (
			id TEXT NOT NULL,
			serie TEXT NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
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

	if _, err := db.ExecContext(ctx, createPostgresRulesUsageTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create rules usage table: %w", err)
	}

	if _, err := db.ExecContext(ctx, createPostgresDashboardUsageTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create dashboard usage table: %w", err)
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

func (p *PostGreSQLProvider) InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error {
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
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	createdAt := time.Now()

	// Iterate over the rulesUsage slice and execute the insert statement
	for _, rule := range rulesUsage {
		// Convert the Labels field to JSON
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
			string(labelsJSON),
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

func (p *PostGreSQLProvider) GetRulesUsage(ctx context.Context, serie string, kind string, page int, pageSize int) (*PagedResult, error) {
	// Calculate offset for pagination
	offset := page * pageSize

	// Query for total count of distinct rules
	countQuery := `
		SELECT COUNT(DISTINCT name || group_name)
		FROM RulesUsage
		WHERE serie = $1
		AND kind = $2
		AND created_at >= NOW() - INTERVAL '30 days';
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
			WHERE serie = $1 AND kind = $2 AND created_at >= NOW() - INTERVAL '30 days'
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
		LIMIT $3 OFFSET $4;
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
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error {
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
		INSERT INTO DashboardUsage (
			id, serie, name, url, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	createdAt := time.Now()

	// Iterate over the rulesUsage slice and execute the insert statement
	for _, dashboard := range dashboardUsage {
		// Execute the insert statement
		_, err = stmt.ExecContext(ctx,
			dashboard.Id,
			dashboard.Serie,
			dashboard.Name,
			dashboard.URL,
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

func (p *PostGreSQLProvider) GetDashboardUsage(ctx context.Context, serie string, page, pageSize int) (*PagedResult, error) {
	// Calculate offset for pagination
	offset := page * pageSize

	// Query for total count of distinct rules
	countQuery := `
		SELECT COUNT(DISTINCT name)
		FROM DashboardUsage
		WHERE serie = $1
		AND created_at >= NOW() - INTERVAL '30 days';
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, serie).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	// Calculate total pages
	totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))

	// Query for paginated results
	query := `
		WITH latest_rules AS (
			SELECT 
				id,
				serie,
				name,
				url,
				created_at,
				ROW_NUMBER() OVER (PARTITION BY serie, name ORDER BY created_at DESC) AS rank
			FROM RulesUsage
			WHERE serie = $1 AND created_at >= NOW() - INTERVAL '30 days'
		)
		SELECT 
			id,
			serie,
			name,
			url,
			created_at,
		FROM latest_rules
		WHERE rank = 1
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4;
	`

	rows, err := p.db.QueryContext(ctx, query, serie, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query rules usage: %w", err)
	}
	defer rows.Close()

	results := []DashboardUsage{}
	for rows.Next() {
		var (
			id        string
			serie     string
			name      string
			url       string
			createdAt time.Time
		)

		// Scan each row
		if err := rows.Scan(&id, &serie, &name, &url, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Append to results
		results = append(results, DashboardUsage{
			Id:        id,
			Serie:     serie,
			Name:      name,
			URL:       url,
			CreatedAt: createdAt,
		})
	}

	// Check for errors after iteration
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) QueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	query := `
		SELECT
			COUNT(*) AS total_queries,
			SUM(CASE WHEN type = 'instant' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS instant_percent,
			SUM(CASE WHEN type = 'range' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS range_percent
		FROM queries
		WHERE ts BETWEEN $1 AND $2;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query types: %w", err)
	}
	defer rows.Close()

	result := &QueryTypesResult{}
	if !rows.Next() {
		return nil, fmt.Errorf("no results found")
	}

	if err := rows.Scan(&result.TotalQueries, &result.InstantPercent, &result.RangePercent); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *PostGreSQLProvider) AverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
	query := `
		WITH current AS (
			SELECT AVG(duration) AS avg_current
			FROM queries
			WHERE ts BETWEEN $1 AND $2
		),
		previous AS (
			SELECT AVG(duration) AS avg_previous 
			FROM queries
			WHERE ts BETWEEN $3 AND $4
		)
		SELECT
			ROUND(COALESCE(avg_current, 0)::numeric, 2),
			CASE 
				WHEN avg_previous IS NULL OR avg_previous = 0 THEN 0
				ELSE ROUND(((avg_current - avg_previous) * 100.0 / avg_previous)::numeric, 2)
			END AS delta_percent
		FROM current, previous;
	`

	from, to := tr.Format(ISOTimeFormat)
	previousFrom, previousTo := tr.Previous().Format(ISOTimeFormat)

	rows, err := p.db.QueryContext(ctx, query, from, to, previousFrom, previousTo)
	if err != nil {
		return nil, fmt.Errorf("failed to query average duration: %w", err)
	}
	defer rows.Close()

	result := &AverageDurationResult{}

	if !rows.Next() {
		return nil, fmt.Errorf("no results found")
	}

	if err := rows.Scan(&result.AvgDuration, &result.DeltaPercent); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *PostGreSQLProvider) GetQueryRate(ctx context.Context, tr TimeRange, metricName string) (*QueryRateResult, error) {
	query := `
		SELECT
			SUM(CASE WHEN statusCode >= 200 AND statusCode < 300 THEN 1 ELSE 0 END) AS successful_queries,
			ROUND(
				SUM(CASE WHEN statusCode >= 200 AND statusCode < 300 THEN 1 ELSE 0 END) * 100.0 / COUNT(*),
				2
			) AS success_rate_percent,
			SUM(CASE WHEN statusCode >= 400 THEN 1 ELSE 0 END) AS failed_queries,
			ROUND(
				SUM(CASE WHEN statusCode >= 400 THEN 1 ELSE 0 END) * 100.0 / COUNT(*),
				2
			) AS error_rate_percent
		FROM queries
		WHERE ts BETWEEN $1 AND $2
		AND CASE 
			WHEN $3 != '' THEN 
				labelMatchers @> $4::jsonb
			ELSE 
				TRUE
			END;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, to, metricName, fmt.Sprintf(`[{"__name__": "%s"}]`, metricName))
	if err != nil {
		return nil, fmt.Errorf("failed to query query rate: %w", err)
	}
	defer rows.Close()

	result := &QueryRateResult{}

	if !rows.Next() {
		return nil, fmt.Errorf("no results found")
	}

	if err := rows.Scan(
		&result.SuccessTotal,
		&result.SuccessRatePercent,
		&result.ErrorTotal,
		&result.ErrorRatePercent,
	); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *PostGreSQLProvider) GetQueryStatusDistribution(ctx context.Context, tr TimeRange) ([]QueryStatusDistributionResult, error) {
	interval := getIntervalByTimeRange(tr.From, tr.To)

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			date_trunc('minute', $1::timestamp) as bucket_start,
			date_trunc('minute', $1::timestamp + $2::interval) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			date_trunc('minute', bucket_end + $2::interval)
		FROM time_buckets 
		WHERE bucket_start < date_trunc('minute', $3::timestamp)
	)
	SELECT 
		bucket_start as time,
		COALESCE(SUM(CASE WHEN statusCode >= 200 AND statusCode < 300 THEN 1 ELSE 0 END), 0) as status2xx,
		COALESCE(SUM(CASE WHEN statusCode >= 400 AND statusCode < 500 THEN 1 ELSE 0 END), 0) as status4xx,
		COALESCE(SUM(CASE WHEN statusCode >= 500 AND statusCode < 600 THEN 1 ELSE 0 END), 0) as status5xx
	FROM time_buckets b
	LEFT JOIN queries q ON 
		q.ts >= b.bucket_start AND 
		q.ts < b.bucket_end
	GROUP BY bucket_start
	ORDER BY bucket_start;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, interval, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query status distribution: %w", err)
	}
	defer rows.Close()

	var results []QueryStatusDistributionResult
	for rows.Next() {
		var result QueryStatusDistributionResult
		if err := rows.Scan(&result.Time, &result.Status2xx, &result.Status4xx, &result.Status5xx); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string) ([]QueryLatencyTrendsResult, error) {
	interval := getIntervalByTimeRange(tr.From, tr.To)

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			date_trunc('minute', $1::timestamp) as bucket_start,
			date_trunc('minute', $1::timestamp + $2::interval) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			date_trunc('minute', bucket_end + $2::interval)
		FROM time_buckets 
		WHERE bucket_start < date_trunc('minute', $3::timestamp)
	)
	SELECT 
		b.bucket_start as time,
		COALESCE(ROUND(AVG(q.duration)::numeric, 2), 0) as value,
		COALESCE(ROUND(percentile_cont(0.95) WITHIN GROUP (ORDER BY q.duration)::numeric, 2), 0) as p95
	FROM time_buckets b
	LEFT JOIN queries q ON 
		q.ts >= b.bucket_start AND 
		q.ts < b.bucket_end
		AND CASE 
			WHEN $4 != '' THEN 
				q.labelMatchers @> $5::jsonb
			ELSE 
				TRUE
			END
	GROUP BY b.bucket_start
	ORDER BY b.bucket_start;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, interval, to, metricName, fmt.Sprintf(`[{"__name__": "%s"}]`, metricName))
	if err != nil {
		return nil, fmt.Errorf("failed to query latency trends: %w", err)
	}
	defer rows.Close()

	var results []QueryLatencyTrendsResult
	for rows.Next() {
		var result QueryLatencyTrendsResult
		var p95 float64
		if err := rows.Scan(&result.Time, &result.Value, &p95); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result.P95 = int(p95)
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error) {
	interval := getIntervalByTimeRange(tr.From, tr.To)

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			date_trunc('minute', $1::timestamp) as bucket_start,
			date_trunc('minute', $1::timestamp + $2::interval) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			date_trunc('minute', bucket_end + $2::interval)
		FROM time_buckets 
		WHERE bucket_start < date_trunc('minute', $3::timestamp)
	)
	SELECT 
		b.bucket_start as time,
		COALESCE(COUNT(q.ts), 0) as value
	FROM time_buckets b
	LEFT JOIN queries q ON 
		q.ts >= b.bucket_start AND 
		q.ts < b.bucket_end
	GROUP BY b.bucket_start
	ORDER BY b.bucket_start;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, interval, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query throughput analysis: %w", err)
	}
	defer rows.Close()

	var results []QueryThroughputAnalysisResult
	for rows.Next() {
		var result QueryThroughputAnalysisResult
		if err := rows.Scan(&result.Time, &result.Value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error) {
	interval := getIntervalByTimeRange(tr.From, tr.To)

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			date_trunc('minute', $1::timestamp) as bucket_start,
			date_trunc('minute', $1::timestamp + $2::interval) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			date_trunc('minute', bucket_end + $2::interval)
		FROM time_buckets 
		WHERE bucket_start < date_trunc('minute', $3::timestamp)
	)
	SELECT 
		b.bucket_start as time,
		COALESCE(SUM(CASE 
			WHEN q.statusCode >= 400 THEN 1 
			ELSE 0 
		END), 0) as value
	FROM time_buckets b
	LEFT JOIN queries q ON 
		q.ts >= b.bucket_start AND 
		q.ts < b.bucket_end
	GROUP BY b.bucket_start
	ORDER BY b.bucket_start;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, interval, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query error analysis: %w", err)
	}
	defer rows.Close()

	var results []QueryErrorAnalysisResult
	for rows.Next() {
		var result QueryErrorAnalysisResult
		if err := rows.Scan(&result.Time, &result.Value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetRecentQueries(ctx context.Context, params RecentQueriesParams) (PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "timestamp"
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}

	validSortFields := map[string]bool{
		"queryParam": true,
		"duration":   true,
		"samples":    true,
		"status":     true,
		"timestamp":  true,
	}
	if !validSortFields[params.SortBy] {
		params.SortBy = "timestamp"
	}

	from, to := params.TimeRange.Format(ISOTimeFormat)

	query := `
	WITH filtered_queries AS (
		SELECT 
			queryParam,
			statusCode,
			MAX(duration) as duration,
			MAX(peakSamples) as peakSamples,
			MAX(ts) as ts
		FROM queries
		WHERE 
			ts BETWEEN $1::timestamp AND $2::timestamp
			AND CASE 
				WHEN $3 != '' THEN 
					queryParam ILIKE '%' || $3 || '%'
				ELSE 
					TRUE
				END
		GROUP BY queryParam, statusCode
	),
	counted_queries AS (
		SELECT COUNT(*) as total_count 
		FROM filtered_queries
	)
	SELECT 
		q.queryParam,
		q.statusCode,
		q.duration,
		q.peakSamples,
		q.ts,
		cq.total_count
	FROM 
		filtered_queries q,
		counted_queries cq
	ORDER BY
		CASE WHEN $4 = 'asc' THEN
			CASE $5
				WHEN 'queryParam' THEN q.queryParam::text
				WHEN 'duration' THEN q.duration::text
				WHEN 'samples' THEN q.peakSamples::text
				WHEN 'status' THEN q.statusCode::text
				WHEN 'timestamp' THEN q.ts::text
			END
		END ASC,
		CASE WHEN $4 = 'desc' THEN
			CASE $5
				WHEN 'queryParam' THEN q.queryParam::text
				WHEN 'duration' THEN q.duration::text
				WHEN 'samples' THEN q.peakSamples::text
				WHEN 'status' THEN q.statusCode::text
				WHEN 'timestamp' THEN q.ts::text
			END
		END DESC
	LIMIT $6 OFFSET $7;
	`

	args := []interface{}{
		from, to,
		params.Filter,
		params.SortOrder,
		params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return PagedResult{}, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	var results []RecentQueriesResult
	var totalCount int

	for rows.Next() {
		var result RecentQueriesResult
		if err := rows.Scan(
			&result.QueryParam,
			&result.Status,
			&result.Duration,
			&result.Samples,
			&result.Timestamp,
			&totalCount,
		); err != nil {
			return PagedResult{}, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return PagedResult{}, fmt.Errorf("row iteration error: %w", err)
	}

	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

	return PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error) {
	return MetricUsageStatics{}, nil
}

func (p *PostGreSQLProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error) {
	return MetricQueryPerformanceStatistics{}, nil
}
