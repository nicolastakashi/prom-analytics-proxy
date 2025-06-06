package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
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
	createSqliteDashboardUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS DashboardUsage (
			id TEXT NOT NULL,
			serie TEXT NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
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
		return nil, ConnectionError(err, "SQLite", "failed to open connection")
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, ConnectionError(err, "SQLite", "failed to ping database")
	}

	if _, err := db.ExecContext(ctx, createSqliteTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "queries")
	}

	if _, err := db.Exec(configureSqliteStmt); err != nil {
		return nil, SchemaError(err, "configuration", "SQLite settings")
	}

	if _, err := db.ExecContext(ctx, createSqliteRulesUsageTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "RulesUsage")
	}

	if _, err := db.ExecContext(ctx, createSqliteDashboardUsageTableStmt); err != nil {
		return nil, SchemaError(err, "creation", "DashboardUsage")
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

	if len(queries) == 0 {
		return nil
	}

	query := `
		INSERT INTO queries (
			ts, queryParam, timeParam, duration, statusCode, bodySize, fingerprint, labelMatchers, type, step, start, "end", totalQueryableSamples, peakSamples
		) VALUES `

	// Get SQLite placeholder format
	qc := NewSQLiteQueryContext()
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

func (p *SQLiteProvider) GetQueriesBySerieName(ctx context.Context, params QueriesBySerieNameParams) (*PagedResult, error) {
	ValidatePagination(&params.Page, &params.PageSize, 10)

	validSortFields := map[string]bool{
		"queryParam":      true,
		"avgDuration":     true,
		"avgPeakySamples": true,
		"maxPeakSamples":  true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "avgDuration")
	SetDefaultTimeRange(&params.TimeRange)

	startTime, endTime := PrepareTimeRange(params.TimeRange, "sqlite")

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
			json_extract(labelMatchers, '$[0].__name__') = ?
			AND ts BETWEEN ? AND ?
			AND CASE 
				WHEN ? != '' THEN 
					queryParam LIKE '%' || ? || '%'
				ELSE 
					1=1
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
		CASE WHEN ? = 'asc' THEN
			CASE ?
				WHEN 'queryParam' THEN queryParam
				WHEN 'avgDuration' THEN avgDuration
				WHEN 'avgPeakySamples' THEN avgPeakySamples
				WHEN 'maxPeakSamples' THEN maxPeakSamples
			END
		END ASC,
		CASE WHEN ? = 'desc' THEN
			CASE ?
				WHEN 'queryParam' THEN queryParam
				WHEN 'avgDuration' THEN avgDuration
				WHEN 'avgPeakySamples' THEN avgPeakySamples
				WHEN 'maxPeakSamples' THEN maxPeakSamples
			END
		END DESC
	LIMIT ? OFFSET ?;
	`

	args := []interface{}{
		params.SerieName, startTime, endTime,
		params.Filter, params.Filter,
		params.SortOrder, params.SortBy,
		params.SortOrder, params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	stmt, err := p.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

	results := []QueriesBySerieNameResult{}
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

func (p *SQLiteProvider) InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error {
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
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

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

func (p *SQLiteProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error) {
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
		params.TimeRange.From = time.Now().UTC().Add(-30 * 24 * time.Hour) // Default to 30 days ago
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

	startTime, endTime := params.TimeRange.Format(SQLiteTimeFormat)

	countQuery := `
		SELECT COUNT(DISTINCT name || group_name)
		FROM RulesUsage
		WHERE serie = ? 
		AND kind = ?
		AND created_at BETWEEN ? AND ?
		AND CASE 
			WHEN ? != '' THEN 
				(name LIKE '%' || ? || '%' OR expression LIKE '%' || ? || '%')
			ELSE 
				1=1
			END;
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, params.Serie, params.Kind, startTime, endTime,
		params.Filter, params.Filter, params.Filter).Scan(&totalCount)
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
			WHERE serie = ? AND kind = ? 
			AND created_at BETWEEN ? AND ?
			AND CASE 
				WHEN ? != '' THEN 
					(name LIKE '%' || ? || '%' OR expression LIKE '%' || ? || '%')
				ELSE 
					1=1
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
			CASE WHEN ? = 'asc' THEN
				CASE ?
					WHEN 'name' THEN name
					WHEN 'group_name' THEN group_name
					WHEN 'expression' THEN expression
					WHEN 'created_at' THEN created_at
				END
			END ASC,
			CASE WHEN ? = 'desc' THEN
				CASE ?
					WHEN 'name' THEN name
					WHEN 'group_name' THEN group_name
					WHEN 'expression' THEN expression
					WHEN 'created_at' THEN created_at
				END
			END DESC
		LIMIT ? OFFSET ?;
	`

	args := []interface{}{
		params.Serie, params.Kind, startTime, endTime,
		params.Filter, params.Filter, params.Filter,
		params.SortOrder, params.SortBy,
		params.SortOrder, params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query rules usage: %w", err)
	}
	defer CloseResource(rows)

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

func (p *SQLiteProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	createdAt := time.Now().UTC()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO DashboardUsage (
			id, serie, name, url, created_at
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

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

func (p *SQLiteProvider) GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (*PagedResult, error) {
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
		params.TimeRange.From = time.Now().UTC().Add(-30 * 24 * time.Hour) // Default to 30 days ago
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

	startTime, endTime := params.TimeRange.Format(SQLiteTimeFormat)

	countQuery := `
		SELECT COUNT(DISTINCT name)
		FROM DashboardUsage
		WHERE serie = ? 
		AND created_at BETWEEN ? AND ?
		AND CASE 
			WHEN ? != '' THEN 
				(name LIKE '%' || ? || '%' OR url LIKE '%' || ? || '%')
			ELSE 
				1=1
			END;
	`
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery,
		params.Serie, startTime, endTime,
		params.Filter, params.Filter, params.Filter).Scan(&totalCount)
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
			WHERE serie = ? 
			AND created_at BETWEEN ? AND ?
			AND CASE 
				WHEN ? != '' THEN 
					(name LIKE '%' || ? || '%' OR url LIKE '%' || ? || '%')
				ELSE 
					1=1
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
			CASE WHEN ? = 'asc' THEN
				CASE ?
					WHEN 'name' THEN name
					WHEN 'url' THEN url
					WHEN 'created_at' THEN created_at
				END
			END ASC,
			CASE WHEN ? = 'desc' THEN
				CASE ?
					WHEN 'name' THEN name
					WHEN 'url' THEN url
					WHEN 'created_at' THEN created_at
				END
			END DESC
		LIMIT ? OFFSET ?;
	`

	args := []interface{}{
		params.Serie, startTime, endTime,
		params.Filter, params.Filter, params.Filter,
		params.SortOrder, params.SortBy,
		params.SortOrder, params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query dashboard usage: %w", err)
	}
	defer CloseResource(rows)

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

// GetQueryTypes returns the total number of queries, the percentage of instant queries, and the percentage of range queries.
func (p *SQLiteProvider) GetQueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	SetDefaultTimeRange(&tr)
	startTime, endTime := PrepareTimeRange(tr, "sqlite")

	query := `
		WITH total AS (
			SELECT COUNT(*) AS count
			FROM queries
			WHERE ts BETWEEN ? AND ?
		),
		types AS (
			SELECT 
				COUNT(CASE WHEN type = 'instant' THEN 1 END) AS instant_count,
				COUNT(CASE WHEN type = 'range' THEN 1 END) AS range_count
			FROM queries
			WHERE ts BETWEEN ? AND ?
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
	defer CloseResource(rows)

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

func (p *SQLiteProvider) GetAverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
	query := `
		WITH current AS (
			SELECT AVG(duration) AS avg_current
			FROM queries
			WHERE ts BETWEEN datetime(?) AND datetime(?)
			ORDER BY ts
		),
		previous AS (
			SELECT AVG(duration) AS avg_previous
			FROM queries
			WHERE ts BETWEEN datetime(?) AND datetime(?)
			ORDER BY ts
		)
		SELECT
			ROUND(avg_current, 2),
			CASE 
				WHEN avg_previous IS NULL OR avg_previous = 0 THEN 0
				ELSE ROUND(((avg_current - avg_previous) * 100.0) / avg_previous, 2)
			END AS delta_percent
		FROM current, previous;
	`

	prevRange := tr.Previous()
	prevFrom, prevTo := prevRange.Format(ISOTimeFormatNano)
	from, to := tr.Format(ISOTimeFormatNano)

	rows, err := p.db.QueryContext(ctx, query, from, to, prevFrom, prevTo)
	if err != nil {
		return nil, fmt.Errorf("failed to query average duration: %w", err)
	}
	defer CloseResource(rows)

	result := &AverageDurationResult{}

	if !rows.Next() {
		return nil, ErrNoResults
	}

	if err := rows.Scan(&result.AvgDuration, &result.DeltaPercent); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *SQLiteProvider) GetQueryRate(ctx context.Context, tr TimeRange, metricName string) (*QueryRateResult, error) {
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
		WHERE ts BETWEEN datetime(?) AND datetime(?)
		AND CASE 
			WHEN ? != '' THEN 
				json_extract(labelMatchers, '$[0].__name__') = ?
			ELSE 
				1=1
			END;
	`

	from, to := tr.Format(ISOTimeFormatNano)
	rows, err := p.db.QueryContext(ctx, query, from, to, metricName, metricName)
	if err != nil {
		return nil, fmt.Errorf("failed to query query rate: %w", err)
	}
	defer CloseResource(rows)

	result := &QueryRateResult{}
	if !rows.Next() {
		return nil, ErrNoResults
	}

	if err := rows.Scan(
		&result.SuccessTotal,
		&result.SuccessRatePercent,
		&result.ErrorTotal,
		&result.ErrorRatePercent,
	); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return result, nil
}

func (p *SQLiteProvider) GetQueryStatusDistribution(ctx context.Context, tr TimeRange) ([]QueryStatusDistributionResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "sqlite")
	from, to := PrepareTimeRange(tr, "sqlite")

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			strftime('%Y-%m-%d %H:%M:00', ?) as bucket_start,
			strftime('%Y-%m-%d %H:%M:00', datetime(?, ?)) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			strftime('%Y-%m-%d %H:%M:00', datetime(bucket_end, ?))
		FROM time_buckets 
		WHERE bucket_start < strftime('%Y-%m-%d %H:%M:00', ?)
	)
	SELECT 
		bucket_start as time,
		SUM(CASE WHEN statusCode >= 200 AND statusCode < 300 THEN 1 ELSE 0 END) as status2xx,
		SUM(CASE WHEN statusCode >= 400 AND statusCode < 500 THEN 1 ELSE 0 END) as status4xx,
		SUM(CASE WHEN statusCode >= 500 AND statusCode < 600 THEN 1 ELSE 0 END) as status5xx
	FROM time_buckets
	LEFT JOIN queries ON 
		ts >= bucket_start AND 
		ts < bucket_end
	GROUP BY bucket_start
	ORDER BY bucket_start;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, from, interval, interval, to)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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

func (p *SQLiteProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string) ([]QueryLatencyTrendsResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "sqlite")
	from, to := PrepareTimeRange(tr, "sqlite")

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			strftime('%Y-%m-%d %H:%M:00', ?) as bucket_start,
			strftime('%Y-%m-%d %H:%M:00', datetime(?, ?)) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			strftime('%Y-%m-%d %H:%M:00', datetime(bucket_end, ?))
		FROM time_buckets 
		WHERE bucket_start < strftime('%Y-%m-%d %H:%M:00', ?)
	)
	SELECT 
		b.bucket_start as time,
		COALESCE(ROUND(AVG(t.duration), 2), 0) as value,
		COALESCE(MAX(CASE 
			WHEN row_num > CAST((total_rows * 0.95) AS INTEGER) THEN t.duration 
			ELSE 0 
		END), 0) as p95
	FROM time_buckets b
	LEFT JOIN (
		SELECT 
			ts,
			duration,
			ROW_NUMBER() OVER (PARTITION BY strftime('%Y-%m-%d %H:%M:00', ts) ORDER BY duration) as row_num,
			COUNT(*) OVER (PARTITION BY strftime('%Y-%m-%d %H:%M:00', ts)) as total_rows
		FROM queries
		WHERE CASE 
			WHEN ? != '' THEN 
				json_extract(labelMatchers, '$[0].__name__') = ?
			ELSE 
				1=1
			END
	) t ON 
		t.ts >= b.bucket_start AND 
		t.ts < b.bucket_end
	GROUP BY b.bucket_start
	ORDER BY b.bucket_start;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, from, interval, interval, to, metricName, metricName)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

	var results []QueryLatencyTrendsResult
	for rows.Next() {
		var result QueryLatencyTrendsResult
		if err := rows.Scan(&result.Time, &result.Value, &result.P95); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}

	return results, nil
}

func (p *SQLiteProvider) GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "sqlite")
	from, to := PrepareTimeRange(tr, "sqlite")

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			strftime('%Y-%m-%d %H:%M:00', ?) as bucket_start,
			strftime('%Y-%m-%d %H:%M:00', datetime(?, ?)) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			strftime('%Y-%m-%d %H:%M:00', datetime(bucket_end, ?))
		FROM time_buckets 
		WHERE bucket_start < strftime('%Y-%m-%d %H:%M:00', ?)
	)
	SELECT 
		b.bucket_start as time,
		COALESCE(COUNT(t.ts), 0) as value
	FROM time_buckets b
	LEFT JOIN queries t ON 
		t.ts >= b.bucket_start AND 
		t.ts < b.bucket_end
	GROUP BY b.bucket_start
	ORDER BY b.bucket_start;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, from, interval, interval, to)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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

func (p *SQLiteProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "sqlite")
	from, to := PrepareTimeRange(tr, "sqlite")

	query := `
	WITH RECURSIVE time_buckets AS (
		SELECT 
			strftime('%Y-%m-%d %H:%M:00', ?) as bucket_start,
			strftime('%Y-%m-%d %H:%M:00', datetime(?, ?)) as bucket_end
		UNION ALL
		SELECT 
			bucket_end,
			strftime('%Y-%m-%d %H:%M:00', datetime(bucket_end, ?))
		FROM time_buckets 
		WHERE bucket_start < strftime('%Y-%m-%d %H:%M:00', ?)
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

	rows, err := ExecuteQuery(ctx, p.db, query, from, from, interval, interval, to)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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

func (p *SQLiteProvider) GetRecentQueries(ctx context.Context, params RecentQueriesParams) (PagedResult, error) {
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

	from, to := params.TimeRange.Format(SQLiteTimeFormat)

	query := `
	WITH filtered_queries AS (
		SELECT 
			queryParam,
			MAX(duration) as duration,
			MAX(peakSamples) as peakSamples,
			statusCode as statusCode,
			ts as ts
		FROM queries
		WHERE 
			ts BETWEEN ? AND ?
			AND CASE 
				WHEN ? != '' THEN 
					queryParam LIKE '%' || ? || '%'
				ELSE 
					1=1
				END
		GROUP BY queryParam
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
		CASE WHEN ? = 'asc' THEN
			CASE ?
				WHEN 'queryParam' THEN queryParam
				WHEN 'duration' THEN duration
				WHEN 'samples' THEN peakSamples
				WHEN 'status' THEN statusCode
				WHEN 'timestamp' THEN ts
			END
		END ASC,
		CASE WHEN ? = 'desc' THEN
			CASE ?
				WHEN 'queryParam' THEN queryParam
				WHEN 'duration' THEN duration
				WHEN 'samples' THEN peakSamples
				WHEN 'status' THEN statusCode
				WHEN 'timestamp' THEN ts
			END
		END DESC
	LIMIT ? OFFSET ?;
	`

	args := []interface{}{
		from, to,
		params.Filter, params.Filter,
		params.SortOrder, params.SortBy,
		params.SortOrder, params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return PagedResult{}, fmt.Errorf("failed to execute query: %w", err)
	}
	defer CloseResource(rows)

	var results []RecentQueriesResult
	var totalCount int

	for rows.Next() {
		var result RecentQueriesResult
		if err := rows.Scan(
			&result.QueryParam,
			&result.Duration,
			&result.Samples,
			&result.Status,
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

func (p *SQLiteProvider) GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error) {
	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN r.Kind = 'alert' THEN 1 ELSE 0 END), 0) as alert_count,
			COALESCE(SUM(CASE WHEN r.Kind = 'record' THEN 1 ELSE 0 END), 0) as record_count,
			COALESCE(COUNT(DISTINCT CASE WHEN d.created_at BETWEEN datetime(?) AND datetime(?) THEN d.Name END), 0) as dashboard_count,
			(SELECT COALESCE(SUM(CASE WHEN Kind = 'alert' THEN 1 ELSE 0 END), 0) FROM RulesUsage WHERE created_at BETWEEN datetime(?) AND datetime(?)) as total_alerts,
			(SELECT COALESCE(SUM(CASE WHEN Kind = 'record' THEN 1 ELSE 0 END), 0) FROM RulesUsage WHERE created_at BETWEEN datetime(?) AND datetime(?)) as total_records,
			(SELECT COALESCE(COUNT(DISTINCT Name), 0) FROM DashboardUsage WHERE created_at BETWEEN datetime(?) AND datetime(?)) as total_dashboards
		FROM RulesUsage r
		LEFT JOIN DashboardUsage d ON r.Serie = d.Serie
		WHERE r.Serie = ? 
		AND r.created_at BETWEEN datetime(?) AND datetime(?);
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query,
		from, to, // For dashboard_count
		from, to, // For total_alerts
		from, to, // For total_records
		from, to, // For total_dashboards
		metricName, from, to) // For the main query
	if err != nil {
		return MetricUsageStatics{}, fmt.Errorf("failed to query metric statistics: %w", err)
	}
	defer CloseResource(rows)

	result := MetricUsageStatics{}
	if !rows.Next() {
		return MetricUsageStatics{}, nil
	}

	if err := rows.Scan(&result.AlertCount, &result.RecordCount, &result.DashboardCount, &result.TotalAlerts, &result.TotalRecords, &result.TotalDashboards); err != nil {
		return MetricUsageStatics{}, fmt.Errorf("failed to scan row: %w", err)
	}

	if err := rows.Err(); err != nil {
		return MetricUsageStatics{}, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

func (p *SQLiteProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error) {
	query := `
		SELECT 
			COUNT(*) as total_queries,
			ROUND(AVG(peakSamples), 2) as average_samples,
			MAX(peakSamples) as peak_samples,
			ROUND(AVG(duration), 2) as average_duration
		FROM queries 
		WHERE json_extract(labelMatchers, '$[0].__name__') = ?
		AND ts BETWEEN datetime(?) AND datetime(?);
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, metricName, from, to)
	if err != nil {
		return MetricQueryPerformanceStatistics{}, fmt.Errorf("failed to query metric query performance statistics: %w", err)
	}
	defer CloseResource(rows)

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
