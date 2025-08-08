package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	pq "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"

	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type PostGreSQLProvider struct {
	db *sql.DB
}

// Non-breaking alias for future rename migration
type PostgreSQLProvider = PostGreSQLProvider

func (p *PostGreSQLProvider) WithDB(f func(db *sql.DB)) {
	f(p.db)
}

// metricMatcherJSON builds the JSONB matcher used in labelMatchers @> predicates
func metricMatcherJSON(metric string) string {
	return fmt.Sprintf(`[{"__name__": "%s"}]`, metric)
}

// DDL creation moved to embedded Goose migrations.

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

	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d application_name=prom-analytics-proxy",
		postgresConfig.Addr,
		postgresConfig.Port,
		postgresConfig.User,
		postgresConfig.Password,
		postgresConfig.Database,
		postgresConfig.SSLMode,
		int(postgresConfig.DialTimeout.Seconds()),
	)

	db, err := otelsql.Open("postgres", psqlInfo, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
	if err != nil {
		return nil, ConnectionError(err, "PostgreSQL", "failed to open connection")
	}

	// Apply pool settings from config when provided; keep safe defaults otherwise
	if postgresConfig.MaxOpenConns > 0 {
		db.SetMaxOpenConns(postgresConfig.MaxOpenConns)
	} else {
		db.SetMaxOpenConns(20)
	}
	if postgresConfig.MaxIdleConns > 0 {
		db.SetMaxIdleConns(postgresConfig.MaxIdleConns)
	} else {
		db.SetMaxIdleConns(10)
	}
	if postgresConfig.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(postgresConfig.ConnMaxLifetime)
	} else {
		db.SetConnMaxLifetime(30 * time.Minute)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, ConnectionError(err, "PostgreSQL", "failed to ping database")
	}

	// Run embedded migrations (PostgreSQL dialect)
	if err := runMigrations(ctx, db, "postgres"); err != nil {
		return nil, SchemaError(err, "migration", "postgres")
	}

	return &PostGreSQLProvider{
		db: db,
	}, nil
}

func (p *PostGreSQLProvider) Close() error {
	return p.db.Close()
}

func (p *PostGreSQLProvider) Insert(ctx context.Context, queries []Query) error {
	if len(queries) == 0 {
		return nil
	}

	// Always use COPY for throughput and fewer round-trips
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return QueryError(err, "begin copy tx", "")
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(
		"queries",
		// Use lower-case identifiers because pq.CopyIn will quote them
		"ts", "queryparam", "timeparam", "duration", "statuscode", "bodysize",
		"fingerprint", "labelmatchers", "type", "step", "start", "end",
		"totalqueryablesamples", "peaksamples",
	))
	if err != nil {
		_ = tx.Rollback()
		return QueryError(err, "prepare copyin", "")
	}

	for _, q := range queries {
		labelMatchersJSON, err := json.Marshal(q.LabelMatchers)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return QueryError(err, "marshal label matchers", "")
		}
		if _, err := stmt.ExecContext(
			ctx,
			q.TS,
			q.QueryParam,
			q.TimeParam,
			q.Duration.Milliseconds(),
			q.StatusCode,
			q.BodySize,
			q.Fingerprint,
			string(labelMatchersJSON),
			q.Type,
			q.Step,
			q.Start,
			q.End,
			q.TotalQueryableSamples,
			q.PeakSamples,
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return QueryError(err, "copyin exec", "")
		}
	}

	if _, err := stmt.ExecContext(ctx); err != nil { // flush
		_ = stmt.Close()
		_ = tx.Rollback()
		return QueryError(err, "copyin flush", "")
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return QueryError(err, "copyin close", "")
	}
	if err := tx.Commit(); err != nil {
		return QueryError(err, "copyin commit", "")
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

	baseQuery := `
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
					queryParam ILIKE '%' || $4 || '%'
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
	`
	// Build ORDER BY clause dynamically to avoid mixed-type CASE expressions
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $5 OFFSET $6;"

	args := []interface{}{
		metricMatcherJSON(params.SerieName),
		params.TimeRange.From,
		params.TimeRange.To,
		params.Filter,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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

func (p *PostGreSQLProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
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
				(name ILIKE '%' || $5 || '%' OR expression ILIKE '%' || $5 || '%')
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

	baseQuery := `
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
					(name ILIKE '%' || $5 || '%' OR expression ILIKE '%' || $5 || '%')
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
	`
	// Build ORDER BY clause dynamically to avoid mixed-type CASE expressions
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $6 OFFSET $7;"

	args := []interface{}{
		params.Serie, params.Kind, startTime, endTime,
		params.Filter,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return nil, err
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
			labelsJSON json.RawMessage
			createdAt  time.Time
		)

		if err := rows.Scan(&serie, &groupName, &name, &expression, &kind, &labelsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var labels []string
		if len(labelsJSON) > 0 {
			if err := json.Unmarshal(labelsJSON, &labels); err != nil {
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
	defer CloseResource(stmt)

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

	baseQuery := `
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
	`
	// Build ORDER BY clause dynamically to avoid mixed-type CASE expressions
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $5 OFFSET $6;"

	offset := (params.Page - 1) * params.PageSize
	rows, err := ExecuteQuery(ctx, p.db, query,
		params.Serie, from, to, params.Filter,
		params.PageSize, offset)
	if err != nil {
		return nil, err
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

func (p *PostGreSQLProvider) GetQueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	SetDefaultTimeRange(&tr)
	startTime, endTime := PrepareTimeRange(tr, "postgresql")

	query := `
		WITH stats AS (
			SELECT
				COUNT(*)                                   AS total,
				COUNT(*) FILTER (WHERE type = 'instant')   AS instant_cnt,
				COUNT(*) FILTER (WHERE type = 'range')     AS range_cnt
			FROM   queries
			WHERE  ts BETWEEN $1 AND $2
		)
		SELECT
			total,
			ROUND(instant_cnt * 100.0 / NULLIF(total,0), 2) AS instant_pct,
			ROUND(range_cnt  * 100.0 / NULLIF(total,0), 2)  AS range_pct
		FROM   stats;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, startTime, endTime)
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

func (p *PostGreSQLProvider) GetAverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
	query := `
		WITH win AS (
			SELECT
				AVG(duration) FILTER (WHERE ts BETWEEN $1 AND $2) AS avg_current,
				AVG(duration) FILTER (WHERE ts BETWEEN $3 AND $4) AS avg_previous
			FROM queries
			WHERE ts BETWEEN LEAST($1,$3) AND GREATEST($2,$4)
		)
		SELECT
			ROUND(COALESCE(avg_current, 0)::numeric, 2)                            AS avg_current,
			CASE
				WHEN avg_previous IS NULL OR avg_previous = 0 THEN 0
				ELSE ROUND(((avg_current - avg_previous) * 100.0 / avg_previous)::numeric, 2)
			END                                                                   AS delta_percent
		FROM win;

	`

	from, to := tr.Format(ISOTimeFormat)
	previousFrom, previousTo := tr.Previous().Format(ISOTimeFormat)

	rows, err := ExecuteQuery(ctx, p.db, query, from, to, previousFrom, previousTo)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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
		WITH s AS (
			SELECT
				COUNT(*)                                                     AS total_rows,
				COUNT(*) FILTER (WHERE statusCode BETWEEN 200 AND 299)       AS success_rows,
				COUNT(*) FILTER (WHERE statusCode >= 400)                    AS fail_rows
			FROM   queries
			WHERE  ts BETWEEN $1 AND $2
			AND  ( $3 = '' OR labelMatchers @> $4::jsonb )
		)
		SELECT
			success_rows                                                          AS successful_queries,
			ROUND(success_rows * 100.0 / NULLIF(total_rows,0), 2)                 AS success_rate_percent,
			fail_rows                                                             AS failed_queries,
			ROUND(fail_rows * 100.0 / NULLIF(total_rows,0), 2)                    AS error_rate_percent
		FROM s;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := ExecuteQuery(ctx, p.db, query, from, to, metricName, metricMatcherJSON(metricName))
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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
	WITH
	buckets AS (
		SELECT generate_series(
				date_trunc('minute', $1::timestamp),
				date_trunc('minute', $3::timestamp),
				$2::interval
			) AS bucket
	),
	agg AS (
		SELECT
			date_trunc('minute', ts)                                AS bucket,
			COUNT(*) FILTER (WHERE statusCode BETWEEN 200 AND 299)  AS status2xx,
			COUNT(*) FILTER (WHERE statusCode BETWEEN 400 AND 499)  AS status4xx,
			COUNT(*) FILTER (WHERE statusCode BETWEEN 500 AND 599)  AS status5xx
		FROM   queries
		WHERE  ts >= $1
		AND  ts <  $3
		GROUP  BY bucket
	)
	SELECT
		b.bucket                                            AS time,
		COALESCE(a.status2xx, 0)                            AS status2xx,
		COALESCE(a.status4xx, 0)                            AS status4xx,
		COALESCE(a.status5xx, 0)                            AS status5xx
	FROM   buckets b
	LEFT   JOIN agg a USING (bucket)
	ORDER  BY b.bucket;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
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

func (p *PostGreSQLProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string) ([]QueryLatencyTrendsResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

	query := `
	WITH
	buckets AS (
		SELECT generate_series(
				date_trunc('minute', $1::timestamp),
				date_trunc('minute', $3::timestamp),
				$2::interval
			) AS bucket
	),
	agg AS (
		SELECT
			date_trunc('minute', ts)                         AS bucket,
			ROUND(AVG(duration)::numeric, 2)                 AS avg_duration,
			ROUND(
				percentile_cont(0.95) WITHIN GROUP (ORDER BY duration)
				::numeric, 2
			)                                                AS p95
		FROM   queries
		WHERE  ts >= $1
		AND  ts <  $3
		AND  ( $4 = '' OR labelMatchers @> $5::jsonb )
		GROUP  BY bucket
	)
	SELECT
		b.bucket                               AS time,
		COALESCE(a.avg_duration, 0)            AS value,
		COALESCE(a.p95,         0)            AS p95
	FROM   buckets b
	LEFT   JOIN agg a USING (bucket)
	ORDER  BY b.bucket;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to, metricName, metricMatcherJSON(metricName))
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

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
	WITH
	buckets AS (
		SELECT generate_series(
				date_trunc('minute', $1::timestamp),
				date_trunc('minute', $3::timestamp),
				$2::interval
			) AS bucket
	),
	agg AS (
		SELECT
			date_trunc('minute', ts) AS bucket,
			COUNT(*)                 AS value
		FROM   queries
		WHERE  ts >= $1
		AND  ts <  $3
		GROUP  BY bucket
	)
	SELECT
		b.bucket         AS time,
		COALESCE(a.value, 0) AS value
	FROM   buckets b
	LEFT   JOIN agg a USING (bucket)
	ORDER  BY b.bucket;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
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

func (p *PostGreSQLProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error) {
	SetDefaultTimeRange(&tr)
	interval := GetInterval(tr.From, tr.To, "postgresql")
	from, to := PrepareTimeRange(tr, "postgresql")

	query := `
	WITH
	buckets AS (
		SELECT generate_series(
				date_trunc('minute', $1::timestamp),
				date_trunc('minute', $3::timestamp),
				$2::interval
			) AS bucket
	),
	agg AS (
		SELECT
			date_trunc('minute', ts)                  AS bucket,
			COUNT(*) FILTER (WHERE statusCode >= 400) AS value
		FROM   queries
		WHERE  ts >= $1
		AND  ts <  $3
		GROUP  BY bucket
	)

	SELECT
		b.bucket                 AS time,
		COALESCE(a.value, 0)     AS value
	FROM   buckets b
	LEFT   JOIN agg a USING (bucket)
	ORDER  BY b.bucket;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to)
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

func (p *PostGreSQLProvider) GetRecentQueries(ctx context.Context, params RecentQueriesParams) (PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}

	switch params.SortBy {
	case "queryParam", "duration", "samples", "status", "timestamp":
	default:
		params.SortBy = "timestamp"
	}

	dir := "DESC"
	if strings.ToLower(params.SortOrder) == "asc" {
		dir = "ASC"
	}

	orderCol := map[string]string{
		"queryParam": "queryParam",
		"duration":   "duration",
		"samples":    "peak_samples",
		"status":     "statusCode",
		"timestamp":  "ts",
	}[params.SortBy]

	querySQL := fmt.Sprintf(`
	WITH aggregated AS (
		SELECT
			queryParam,
			statusCode,
			MAX(duration)    AS duration,
			MAX(peakSamples) AS peak_samples,
			MAX(ts)          AS ts
		FROM   queries
		WHERE  ts BETWEEN $1 AND $2
		AND  ($3 = '' OR queryParam ILIKE '%%' || $3 || '%%')
		GROUP  BY queryParam, statusCode
	)
	SELECT
		queryParam,
		statusCode,
		duration,
		peak_samples,
		ts,
		COUNT(*) OVER () AS total_count
	FROM   aggregated
	ORDER  BY %s %s NULLS LAST
	LIMIT  $4 OFFSET $5;`, orderCol, dir)

	from, to := params.TimeRange.Format(ISOTimeFormat)

	rows, err := p.db.QueryContext(
		ctx,
		querySQL,
		from,
		to,
		params.Filter,
		params.PageSize,
		(params.Page-1)*params.PageSize,
	)
	if err != nil {
		return PagedResult{}, fmt.Errorf("query exec: %w", err)
	}
	defer CloseResource(rows)

	var (
		out        []RecentQueriesResult
		totalCount int
	)

	for rows.Next() {
		var r RecentQueriesResult
		if err := rows.Scan(
			&r.QueryParam,
			&r.Status,
			&r.Duration,
			&r.Samples,
			&r.Timestamp,
			&totalCount,
		); err != nil {
			return PagedResult{}, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return PagedResult{}, fmt.Errorf("row iter: %w", err)
	}

	pages := (totalCount + params.PageSize - 1) / params.PageSize

	return PagedResult{
		Total:      totalCount,
		TotalPages: pages,
		Data:       out,
	}, nil
}

func (p *PostGreSQLProvider) GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error) {
	query := `
	WITH rule_stats AS (
		SELECT
			COUNT(DISTINCT name) FILTER (WHERE serie = $3 AND kind = 'alert')  AS alert_count,
			COUNT(DISTINCT name) FILTER (WHERE serie = $3 AND kind = 'record') AS record_count,
			COUNT(DISTINCT name) FILTER (WHERE kind = 'alert')                AS total_alerts,
			COUNT(DISTINCT name) FILTER (WHERE kind = 'record')               AS total_records
		FROM   rulesusage
		WHERE  created_at BETWEEN $1 AND $2
	),
	dash_stats AS (
		SELECT
			COUNT(DISTINCT name) FILTER (WHERE serie = $3) AS dashboard_count,
			COUNT(DISTINCT name)                           AS total_dashboards
		FROM   dashboardusage
		WHERE  created_at BETWEEN $1 AND $2
	)
	SELECT
		rs.alert_count,
		rs.record_count,
		ds.dashboard_count,
		rs.total_alerts,
		rs.total_records,
		ds.total_dashboards
	FROM   rule_stats rs
	CROSS  JOIN dash_stats ds;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := ExecuteQuery(ctx, p.db, query,
		from, to,
		metricName,
	)
	if err != nil {
		return MetricUsageStatics{}, err
	}
	defer CloseResource(rows)

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
	rows, err := ExecuteQuery(ctx, p.db, query, metricMatcherJSON(metricName), from, to)
	if err != nil {
		return MetricQueryPerformanceStatistics{}, err
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
