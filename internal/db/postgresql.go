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
			labels JSONB,
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
		return nil, ConnectionError(err, "PostgreSQL", "failed to open connection")
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, ConnectionError(err, "PostgreSQL", "failed to ping database")
	}

	if _, err := db.ExecContext(ctx, createPostgresTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "queries")
	}

	if _, err := db.ExecContext(ctx, createPostgresRulesUsageTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "RulesUsage")
	}

	if _, err := db.ExecContext(ctx, createPostgresDashboardUsageTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "DashboardUsage")
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

	if len(queries) == 0 {
		return nil
	}

	query := `
		INSERT INTO queries (
			ts, queryParam, timeParam, duration, statusCode, bodySize, fingerprint, labelMatchers, type, step, start, "end", totalQueryableSamples, peakSamples
		) VALUES `

	qc := NewPostgreSQLQueryContext()
	placeholders, _, _ := qc.CreateInsertPlaceholders(14, len(queries))
	query += placeholders

	values := make([]interface{}, 0, len(queries)*14)
	for _, q := range queries {
		labelMatchersJSON, err := json.Marshal(q.LabelMatchers)
		if err != nil {
			return QueryError(err, "marshaling label matchers", "")
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

	_, err := p.db.ExecContext(ctx, query, values...)
	if err != nil {
		return QueryError(err, "executing insert query", "")
	}

	return nil
}

func (p *PostGreSQLProvider) GetQueriesBySerieName(
	ctx context.Context,
	params QueriesBySerieNameParams) (*PagedResult, error) {

	// Set default values using common helpers
	ValidatePagination(&params.Page, &params.PageSize, 10)

	validSortFields := map[string]bool{
		"queryParam":      true,
		"avgDuration":     true,
		"avgPeakySamples": true,
		"maxPeakSamples":  true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "avgDuration")
	SetDefaultTimeRange(&params.TimeRange)

	query := `
	WITH filtered_queries AS (
		SELECT
			queryParam,
			AVG(duration) AS avgDuration,
			AVG(peakSamples) AS avgPeakySamples,
			MAX(peakSamples) AS maxPeakSamples
		FROM
			queries
		WHERE
			labelMatchers @> $1::jsonb
			AND ts BETWEEN $2 AND $3
			AND CASE 
				WHEN $4 != '' THEN 
					queryParam LIKE '%' || $4 || '%'
				ELSE 
					TRUE
				END
		GROUP BY
			queryParam
	),
	counted_queries AS (
		SELECT COUNT(*) as total_count 
		FROM filtered_queries
	)
	SELECT 
		q.*,
		cq.total_count
	FROM 
		filtered_queries q,
		counted_queries cq
	ORDER BY
		CASE WHEN $5 = 'asc' THEN
			CASE $6
				WHEN 'queryParam' THEN q.queryParam
				WHEN 'avgDuration' THEN q.avgDuration
				WHEN 'avgPeakySamples' THEN q.avgPeakySamples
				WHEN 'maxPeakSamples' THEN q.maxPeakSamples
			END
		END ASC NULLS LAST,
		CASE WHEN $5 = 'desc' THEN
			CASE $6
				WHEN 'queryParam' THEN q.queryParam
				WHEN 'avgDuration' THEN q.avgDuration
				WHEN 'avgPeakySamples' THEN q.avgPeakySamples
				WHEN 'maxPeakSamples' THEN q.maxPeakSamples
			END
		END DESC NULLS LAST
	LIMIT $7 OFFSET $8;
	`

	args := []interface{}{
		fmt.Sprintf(`[{"__name__": "%s"}]`, params.SerieName),
		params.TimeRange.From,
		params.TimeRange.To,
		params.Filter,
		params.SortOrder,
		params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueriesBySerieNameResult
	var totalCount int

	for rows.Next() {
		var result QueriesBySerieNameResult
		if err := rows.Scan(
			&result.Query,
			&result.AvgDuration,
			&result.AvgPeakySamples,
			&result.MaxPeakSamples,
			&totalCount,
		); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}

	totalPages := CalculateTotalPages(totalCount, params.PageSize)

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO RulesUsage (
			serie, group_name, name, expression, kind, labels, created_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	createdAt := time.Now().UTC()

	for _, rule := range rulesUsage {
		labelsJSON, err := json.Marshal(rule.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels to JSON: %w", err)
		}

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (p *PostGreSQLProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "created_at"
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	if params.TimeRange.From.IsZero() {
		params.TimeRange.From = time.Now().UTC().Add(-30 * 24 * time.Hour)
	}
	if params.TimeRange.To.IsZero() {
		params.TimeRange.To = time.Now().UTC()
	}

	validSortFields := map[string]bool{
		"name":       true,
		"group_name": true,
		"expression": true,
		"created_at": true,
	}
	if !validSortFields[params.SortBy] {
		params.SortBy = "created_at"
	}

	startTime, endTime := params.TimeRange.Format(ISOTimeFormat)

	countQuery := `
		SELECT COUNT(DISTINCT name || group_name)
		FROM RulesUsage
		WHERE serie = $1 
		AND kind = $2
		AND created_at BETWEEN $3 AND $4
		AND CASE 
			WHEN $5 != '' THEN 
				(name LIKE '%' || $5 || '%' OR expression LIKE '%' || $5 || '%')
			ELSE 
				TRUE
			END;
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, params.Serie, params.Kind, startTime, endTime,
		params.Filter).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

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
			WHERE serie = $1 AND kind = $2 
			AND created_at BETWEEN $3 AND $4
			AND CASE 
				WHEN $5 != '' THEN 
					(name LIKE '%' || $5 || '%' OR expression LIKE '%' || $5 || '%')
				ELSE 
					TRUE
				END
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
		ORDER BY
			CASE WHEN $6 = 'asc' THEN
				CASE $7
					WHEN 'name' THEN name
					WHEN 'group_name' THEN group_name
					WHEN 'expression' THEN expression
					WHEN 'created_at' THEN created_at
				END
			END ASC,
			CASE WHEN $6 = 'desc' THEN
				CASE $7
					WHEN 'name' THEN name
					WHEN 'group_name' THEN group_name
					WHEN 'expression' THEN expression
					WHEN 'created_at' THEN created_at
				END
			END DESC
		LIMIT $8 OFFSET $9;
	`

	args := []interface{}{
		params.Serie, params.Kind, startTime, endTime,
		params.Filter,
		params.SortOrder, params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
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

		if err := rows.Scan(&serie, &groupName, &name, &expression, &kind, &labelsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var labels []string
		if labelsJSON != "" {
			if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
			}
		}

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
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO DashboardUsage (
			id, serie, name, url, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	createdAt := time.Now().UTC()

	for _, dashboard := range dashboardUsage {
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (p *PostGreSQLProvider) GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (*PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "created_at"
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	if params.TimeRange.From.IsZero() {
		params.TimeRange.From = time.Now().UTC().Add(-30 * 24 * time.Hour)
	}
	if params.TimeRange.To.IsZero() {
		params.TimeRange.To = time.Now().UTC()
	}

	validSortFields := map[string]bool{
		"name":       true,
		"url":        true,
		"created_at": true,
	}
	if !validSortFields[params.SortBy] {
		params.SortBy = "created_at"
	}

	from, to := params.TimeRange.Format(ISOTimeFormat)

	countQuery := `
		SELECT COUNT(DISTINCT name)
		FROM DashboardUsage
		WHERE serie = $1
		AND created_at BETWEEN $2 AND $3
		AND CASE 
			WHEN $4 != '' THEN 
				(name ILIKE '%' || $4 || '%' OR url ILIKE '%' || $4 || '%')
			ELSE 
				TRUE
			END;
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery,
		params.Serie, from, to, params.Filter).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

	query := `
		WITH latest_dashboards AS (
			SELECT 
				id,
				serie,
				name,
				url,
				created_at,
				ROW_NUMBER() OVER (PARTITION BY serie, name ORDER BY created_at DESC) AS rank
			FROM DashboardUsage
			WHERE serie = $1 
			AND created_at BETWEEN $2 AND $3
			AND CASE 
				WHEN $4 != '' THEN 
					(name ILIKE '%' || $4 || '%' OR url ILIKE '%' || $4 || '%')
				ELSE 
					TRUE
				END
		)
		SELECT 
			id,
			serie,
			name,
			url,
			created_at
		FROM latest_dashboards
		WHERE rank = 1
		ORDER BY
			CASE WHEN $5 = 'asc' THEN
				CASE $6
					WHEN 'name' THEN name
					WHEN 'url' THEN url
					WHEN 'created_at' THEN created_at
				END
			END ASC,
			CASE WHEN $5 = 'desc' THEN
				CASE $6
					WHEN 'name' THEN name
					WHEN 'url' THEN url
					WHEN 'created_at' THEN created_at
				END
			END DESC
		LIMIT $7 OFFSET $8;
	`

	offset := (params.Page - 1) * params.PageSize
	rows, err := p.db.QueryContext(ctx, query,
		params.Serie, from, to, params.Filter,
		params.SortOrder, params.SortBy,
		params.PageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query dashboard usage: %w", err)
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

		if err := rows.Scan(&id, &serie, &name, &url, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		results = append(results, DashboardUsage{
			Id:        id,
			Serie:     serie,
			Name:      name,
			URL:       url,
			CreatedAt: createdAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) GetQueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	SetDefaultTimeRange(&tr)
	startTime, endTime := PrepareTimeRange(tr, "postgresql")

	query := `
		WITH total AS (
			SELECT COUNT(*) AS count
			FROM queries
			WHERE ts BETWEEN $1 AND $2
		),
		types AS (
			SELECT 
				COUNT(CASE WHEN type = 'instant' THEN 1 END) AS instant_count,
				COUNT(CASE WHEN type = 'range' THEN 1 END) AS range_count
			FROM queries
			WHERE ts BETWEEN $3 AND $4
		)
		SELECT 
			t.count,
			CASE WHEN t.count > 0 THEN ROUND(ty.instant_count * 100.0 / t.count, 2) ELSE 0 END,
			CASE WHEN t.count > 0 THEN ROUND(ty.range_count * 100.0 / t.count, 2) ELSE 0 END
		FROM 
			total t, 
			types ty;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, startTime, endTime, startTime, endTime)
	if err != nil {
		return nil, err
	}

	var result QueryTypesResult
	err = ScanSingleRow(rows, &result.TotalQueries, &result.InstantPercent, &result.RangePercent)
	if err != nil {
		if IsNoResults(err) {
			return &QueryTypesResult{
				TotalQueries:   new(int),
				InstantPercent: new(float64),
				RangePercent:   new(float64),
			}, nil
		}
		return nil, err
	}

	return &result, nil
}

func (p *PostGreSQLProvider) GetAverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
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
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueryStatusDistributionResult
	for rows.Next() {
		var result QueryStatusDistributionResult
		if err := rows.Scan(&result.Time, &result.Status2xx, &result.Status4xx, &result.Status5xx); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string) ([]QueryLatencyTrendsResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to, metricName, fmt.Sprintf(`[{"__name__": "%s"}]`, metricName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueryLatencyTrendsResult
	for rows.Next() {
		var result QueryLatencyTrendsResult
		var p95 float64
		if err := rows.Scan(&result.Time, &result.Value, &p95); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		result.P95 = int(p95)
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueryThroughputAnalysisResult
	for rows.Next() {
		var result QueryThroughputAnalysisResult
		if err := rows.Scan(&result.Time, &result.Value); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}

	return results, nil
}

func (p *PostGreSQLProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueryErrorAnalysisResult
	for rows.Next() {
		var result QueryErrorAnalysisResult
		if err := rows.Scan(&result.Time, &result.Value); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
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
	query := `
		WITH metric_stats AS (
			SELECT 
				COALESCE(SUM(CASE WHEN r.kind = 'alert' THEN 1 ELSE 0 END), 0) as alert_count,
				COALESCE(SUM(CASE WHEN r.kind = 'record' THEN 1 ELSE 0 END), 0) as record_count,
				COALESCE(COUNT(DISTINCT CASE WHEN d.created_at BETWEEN $1 AND $2 THEN d.name END), 0) as dashboard_count
			FROM RulesUsage r
			LEFT JOIN DashboardUsage d ON r.serie = d.serie
			WHERE r.serie = $3 
			AND r.created_at BETWEEN $4 AND $5
		),
		total_stats AS (
			SELECT 
				COALESCE(SUM(CASE WHEN kind = 'alert' THEN 1 ELSE 0 END), 0) as total_alerts,
				COALESCE(SUM(CASE WHEN kind = 'record' THEN 1 ELSE 0 END), 0) as total_records,
				COALESCE(COUNT(DISTINCT name), 0) as total_dashboards
			FROM RulesUsage
			WHERE created_at BETWEEN $6 AND $7
		)
		SELECT 
			ms.alert_count,
			ms.record_count,
			ms.dashboard_count,
			ts.total_alerts,
			ts.total_records,
			ts.total_dashboards
		FROM metric_stats ms, total_stats ts;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query,
		from, to,
		metricName, from, to,
		from, to,
	)
	if err != nil {
		return MetricUsageStatics{}, fmt.Errorf("failed to query metric statistics: %w", err)
	}
	defer rows.Close()

	result := MetricUsageStatics{}
	if !rows.Next() {
		return MetricUsageStatics{}, nil
	}

	if err := rows.Scan(
		&result.AlertCount,
		&result.RecordCount,
		&result.DashboardCount,
		&result.TotalAlerts,
		&result.TotalRecords,
		&result.TotalDashboards,
	); err != nil {
		return MetricUsageStatics{}, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return MetricUsageStatics{}, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *PostGreSQLProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error) {
	query := `
		SELECT 
			COUNT(*) as total_queries,
			ROUND(AVG(totalQueryableSamples)::numeric, 2) as average_samples,
			MAX(peakSamples) as peak_samples,
			ROUND(AVG(duration)::numeric, 2) as average_duration
		FROM queries 
		WHERE labelMatchers @> $1::jsonb
		AND ts BETWEEN $2 AND $3;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, fmt.Sprintf(`[{"__name__": "%s"}]`, metricName), from, to)
	if err != nil {
		return MetricQueryPerformanceStatistics{}, fmt.Errorf("failed to query metric query performance statistics: %w", err)
	}
	defer rows.Close()

	result := MetricQueryPerformanceStatistics{}
	if !rows.Next() {
		return MetricQueryPerformanceStatistics{}, nil
	}

	if err := rows.Scan(&result.TotalQueries, &result.AverageSamples, &result.PeakSamples, &result.AverageDuration); err != nil {
		return MetricQueryPerformanceStatistics{}, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return MetricQueryPerformanceStatistics{}, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}
