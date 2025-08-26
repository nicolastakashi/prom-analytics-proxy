package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	pq "github.com/lib/pq"
	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
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
	// In-memory de-dup in case payload contains duplicates
	type ruleKey struct {
		Serie      string
		Kind       string
		Group      string
		Name       string
		Expression string
		Labels     string
	}

	dedup := make(map[ruleKey]struct{})
	normalized := make([]RulesUsage, 0, len(rulesUsage))
	for _, r := range rulesUsage {
		// Normalize labels order for stable JSON equality
		labels := make([]string, len(r.Labels))
		copy(labels, r.Labels)
		sort.Strings(labels)
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels to JSON: %w", err)
		}
		k := ruleKey{Serie: r.Serie, Kind: r.Kind, Group: r.GroupName, Name: r.Name, Expression: r.Expression, Labels: string(labelsJSON)}
		if _, ok := dedup[k]; ok {
			continue
		}
		dedup[k] = struct{}{}
		r.Labels = labels
		normalized = append(normalized, r)
	}

	if len(normalized) == 0 {
		return nil
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Upsert to avoid duplicates and track presence window
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO RulesUsage (
            serie, group_name, name, expression, kind, labels, created_at, first_seen_at, last_seen_at
        ) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $7, $7)
        ON CONFLICT (serie, kind, group_name, name, expression, labels)
        DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at
    `)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

	now := time.Now().UTC()
	for _, rule := range normalized {
		labelsJSON, err := json.Marshal(rule.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels to JSON: %w", err)
		}
		if _, err = stmt.ExecContext(ctx,
			rule.Serie,
			rule.GroupName,
			rule.Name,
			rule.Expression,
			rule.Kind,
			string(labelsJSON),
			now,
		); err != nil {
			return fmt.Errorf("failed to execute upsert: %w", err)
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
	} else if params.PageSize > MaxPageSize {
		params.PageSize = MaxPageSize
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
        SELECT COUNT(DISTINCT kind || '|' || group_name || '|' || name)
        FROM RulesUsage
        WHERE serie = $1 
        AND kind = $2
        AND first_seen_at <= $4 AND last_seen_at >= $3
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
        WITH overlapped AS (
            SELECT 
                serie,
                group_name,
                name,
                expression,
                kind,
                labels,
                created_at,
                last_seen_at,
                ROW_NUMBER() OVER (
                    PARTITION BY serie, kind, group_name, name 
                    ORDER BY last_seen_at DESC
                ) AS rank
            FROM RulesUsage
            WHERE serie = $1 AND kind = $2 
            AND first_seen_at <= $4 AND last_seen_at >= $3
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
        FROM overlapped
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
	// In-memory dedup by (id, serie) keeping last name/url
	type dashKey struct{ Id, Serie string }
	dedup := make(map[dashKey]DashboardUsage)
	for _, d := range dashboardUsage {
		k := dashKey{Id: d.Id, Serie: d.Serie}
		dedup[k] = d
	}
	if len(dedup) == 0 {
		return nil
	}

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
            id, serie, name, url, created_at, first_seen_at, last_seen_at
        ) VALUES ($1, $2, $3, $4, $5, $5, $5)
        ON CONFLICT (id, serie)
        DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, name = EXCLUDED.name, url = EXCLUDED.url
    `)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

	now := time.Now().UTC()
	for _, d := range dedup {
		if _, err := stmt.ExecContext(ctx, d.Id, d.Serie, d.Name, d.URL, now); err != nil {
			return fmt.Errorf("failed to execute upsert: %w", err)
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
        SELECT COUNT(DISTINCT id)
        FROM DashboardUsage
        WHERE serie = $1
        AND first_seen_at <= $3 AND last_seen_at >= $2
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
        WITH overlapped AS (
            SELECT 
                id,
                serie,
                name,
                url,
                created_at,
                last_seen_at,
                ROW_NUMBER() OVER (
                    PARTITION BY serie, id ORDER BY last_seen_at DESC
                ) AS rank
            FROM DashboardUsage
            WHERE serie = $1 
            AND first_seen_at <= $3 AND last_seen_at >= $2
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
        FROM overlapped
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

func (p *PostGreSQLProvider) GetSeriesMetadata(ctx context.Context, params SeriesMetadataParams) (*PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "name"
	}
	if params.SortOrder == "" {
		params.SortOrder = "asc"
	}

	// Count
	countSQL := `
        SELECT COUNT(*)
        FROM metrics_catalog c
        LEFT JOIN metrics_usage_summary s ON s.name = c.name
        WHERE ($1 = '' OR c.name ILIKE '%' || $1 || '%' OR c.help ILIKE '%' || $1 || '%')
          AND ($2 = 'all' OR c.type = $2)
          AND (CASE WHEN $3 THEN COALESCE(s.alert_count,0)=0 AND COALESCE(s.record_count,0)=0 AND COALESCE(s.dashboard_count,0)=0 AND COALESCE(s.query_count,0)=0 ELSE TRUE END)
          AND ($4 = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = $4
          ))
    `
	var total int
	if err := p.db.QueryRowContext(ctx, countSQL, params.Filter, params.Type, params.Unused, params.Job).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	if total == 0 {
		return &PagedResult{Total: 0, TotalPages: 0, Data: []models.MetricMetadata{}}, nil
	}

	baseQuery := `
        SELECT c.name, c.type, c.help, c.unit,
               COALESCE(s.alert_count,0), COALESCE(s.record_count,0), COALESCE(s.dashboard_count,0), COALESCE(s.query_count,0), s.last_queried_at
        FROM metrics_catalog c
        LEFT JOIN metrics_usage_summary s ON s.name = c.name
        WHERE ($1 = '' OR c.name ILIKE '%%' || $1 || '%%' OR c.help ILIKE '%%' || $1 || '%%')
          AND ($2 = 'all' OR c.type = $2)
          AND (CASE WHEN $3 THEN COALESCE(s.alert_count,0)=0 AND COALESCE(s.record_count,0)=0 AND COALESCE(s.dashboard_count,0)=0 AND COALESCE(s.query_count,0)=0 ELSE TRUE END)
          AND ($4 = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = $4
          ))
    `
	// Build complete query with safe ORDER BY clause to prevent SQL injection
	query := BuildSafeQueryWithOrderBy(baseQuery, "c", " LIMIT $5 OFFSET $6", params.SortBy, params.SortOrder, ValidSeriesMetadataSortFields, "name")

	rows, err := p.db.QueryContext(ctx, query, params.Filter, params.Type, params.Unused, params.Job, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer CloseResource(rows)

	type row struct {
		name, mtype, help, unit     string
		alert, record, dash, qcount int
		last                        sql.NullTime
	}
	out := make([]models.MetricMetadata, 0, params.PageSize)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.mtype, &r.help, &r.unit, &r.alert, &r.record, &r.dash, &r.qcount, &r.last); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		mm := models.MetricMetadata{Name: r.name, Type: r.mtype, Help: r.help, Unit: r.unit, AlertCount: r.alert, RecordCount: r.record, DashboardCount: r.dash, QueryCount: r.qcount}
		if r.last.Valid {
			mm.LastQueriedAt = r.last.Time.UTC().Format(time.RFC3339)
		}

		out = append(out, mm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter: %w", err)
	}

	pages := (total + params.PageSize - 1) / params.PageSize
	return &PagedResult{Total: total, TotalPages: pages, Data: out}, nil
}

func (p *PostGreSQLProvider) UpsertMetricsCatalog(ctx context.Context, items []MetricCatalogItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO metrics_catalog(name, type, help, unit, last_synced_at)
        VALUES ($1, $2, $3, $4, NOW())
        ON CONFLICT(name) DO UPDATE SET
          type=EXCLUDED.type,
          help=EXCLUDED.help,
          unit=EXCLUDED.unit,
          last_synced_at=EXCLUDED.last_synced_at
    `)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare: %w", err)
	}
	defer CloseResource(stmt)
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, it.Name, it.Type, it.Help, it.Unit); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (p *PostGreSQLProvider) RefreshMetricsUsageSummary(ctx context.Context, tr TimeRange) error {
	from, to := PrepareTimeRange(tr, "postgresql")
	query := `
    INSERT INTO metrics_usage_summary(name, alert_count, record_count, dashboard_count, query_count, last_queried_at, updated_at)
    SELECT c.name,
           COALESCE(ra.alert_count, 0),
           COALESCE(ra.record_count, 0),
           COALESCE(da.dashboard_count, 0),
           COALESCE(qa.query_count, 0),
           qa.last_queried_at,
           NOW()
    FROM metrics_catalog c
    LEFT JOIN (
        SELECT serie AS name,
               COUNT(*) FILTER (WHERE kind='alert') AS alert_count,
               COUNT(*) FILTER (WHERE kind='record') AS record_count
        FROM RulesUsage
        WHERE first_seen_at <= $2 AND last_seen_at >= $1
        GROUP BY serie
    ) ra USING(name)
    LEFT JOIN (
        SELECT serie AS name,
               COUNT(DISTINCT id) AS dashboard_count
        FROM DashboardUsage
        WHERE first_seen_at <= $2 AND last_seen_at >= $1
        GROUP BY serie
    ) da USING(name)
    LEFT JOIN (
        SELECT (labelMatchers->0->>'__name__') AS name,
               COUNT(*) AS query_count,
               MAX(ts)  AS last_queried_at
        FROM queries
        WHERE ts BETWEEN $1 AND $2
        GROUP BY 1
    ) qa USING(name)
    ON CONFLICT(name) DO UPDATE SET
        alert_count=EXCLUDED.alert_count,
        record_count=EXCLUDED.record_count,
        dashboard_count=EXCLUDED.dashboard_count,
        query_count=EXCLUDED.query_count,
        last_queried_at=EXCLUDED.last_queried_at,
        updated_at=EXCLUDED.updated_at;
    `
	_, err := p.db.ExecContext(ctx, query, from, to)
	if err != nil {
		return fmt.Errorf("refresh summary: %w", err)
	}
	return nil
}

func (p *PostGreSQLProvider) UpsertMetricsJobIndex(ctx context.Context, items []MetricJobIndexItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metrics_job_index(name, job, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT(name, job) DO UPDATE SET
		  updated_at = EXCLUDED.updated_at
	`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare: %w", err)
	}
	defer CloseResource(stmt)
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, it.Name, it.Job); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListJobs returns the distinct list of jobs known in metrics_job_index
func (p *PostGreSQLProvider) ListJobs(ctx context.Context) ([]string, error) {
	rows, err := ExecuteQuery(ctx, p.db, `SELECT DISTINCT job FROM metrics_job_index ORDER BY job`)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

	var jobs []string
	for rows.Next() {
		var job string
		if err := rows.Scan(&job); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter jobs: %w", err)
	}
	return jobs, nil
}

func (p *PostGreSQLProvider) GetQueryTypes(ctx context.Context, tr TimeRange, fingerprint string) (*QueryTypesResult, error) {
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
			AND   ($3 = '' OR fingerprint = $3)
		)
		SELECT
			total,
			ROUND(instant_cnt * 100.0 / NULLIF(total,0), 2) AS instant_pct,
			ROUND(range_cnt  * 100.0 / NULLIF(total,0), 2)  AS range_pct
		FROM   stats;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, startTime, endTime, fingerprint)
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

func (p *PostGreSQLProvider) GetAverageDuration(ctx context.Context, tr TimeRange, fingerprint string) (*AverageDurationResult, error) {
	query := `
		WITH win AS (
			SELECT
				AVG(duration) FILTER (WHERE ts BETWEEN $1 AND $2) AS avg_current,
				AVG(duration) FILTER (WHERE ts BETWEEN $3 AND $4) AS avg_previous
			FROM queries
			WHERE ts BETWEEN LEAST($1,$3) AND GREATEST($2,$4)
			AND   ($5 = '' OR fingerprint = $5)
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

	rows, err := ExecuteQuery(ctx, p.db, query, from, to, previousFrom, previousTo, fingerprint)
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

func (p *PostGreSQLProvider) GetQueryRate(ctx context.Context, tr TimeRange, metricName string, fingerprint string) (*QueryRateResult, error) {
	query := `
		WITH s AS (
			SELECT
				COUNT(*)                                                     AS total_rows,
				COUNT(*) FILTER (WHERE statusCode BETWEEN 200 AND 299)       AS success_rows,
				COUNT(*) FILTER (WHERE statusCode >= 400)                    AS fail_rows
			FROM   queries
			WHERE  ts BETWEEN $1 AND $2
			AND  ( $3 = '' OR labelMatchers @> $4::jsonb )
			AND  ( $5 = '' OR fingerprint = $5 )
		)
		SELECT
			success_rows                                                          AS successful_queries,
			ROUND(success_rows * 100.0 / NULLIF(total_rows,0), 2)                 AS success_rate_percent,
			fail_rows                                                             AS failed_queries,
			ROUND(fail_rows * 100.0 / NULLIF(total_rows,0), 2)                    AS error_rate_percent
		FROM s;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := ExecuteQuery(ctx, p.db, query, from, to, metricName, metricMatcherJSON(metricName), fingerprint)
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

func (p *PostGreSQLProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string, fingerprint string) ([]QueryLatencyTrendsResult, error) {
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
		AND  ( $6 = '' OR fingerprint = $6 )
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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to, metricName, metricMatcherJSON(metricName), fingerprint)
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

// GetQueryTimeRangeDistribution returns counts and percentages of range queries bucketed by window size.
func (p *PostGreSQLProvider) GetQueryTimeRangeDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryTimeRangeDistributionResult, error) {
	SetDefaultTimeRange(&tr)
	from, to := PrepareTimeRange(tr, "postgresql")

	query := `
    WITH filtered AS (
        SELECT EXTRACT(EPOCH FROM ("end" - start))::bigint AS seconds
        FROM   queries
        WHERE  ts BETWEEN $1 AND $2
        AND    ($3 = '' OR fingerprint = $3)
        AND    type = 'range'
        AND    start IS NOT NULL AND "end" IS NOT NULL AND "end" > start
    ), total AS (
        SELECT COUNT(*) AS total FROM filtered
    ), buckets AS (
        SELECT '<24h' AS label, COUNT(*) AS cnt FROM filtered WHERE seconds < 86400
        UNION ALL
        SELECT '24h'  AS label, COUNT(*) AS cnt FROM filtered WHERE seconds >= 86400 AND seconds < 7*86400
        UNION ALL
        SELECT '7d'   AS label, COUNT(*) AS cnt FROM filtered WHERE seconds >= 7*86400 AND seconds < 30*86400
        UNION ALL
        SELECT '30d'  AS label, COUNT(*) AS cnt FROM filtered WHERE seconds >= 30*86400 AND seconds < 60*86400
        UNION ALL
        SELECT '60d'  AS label, COUNT(*) AS cnt FROM filtered WHERE seconds >= 60*86400 AND seconds < 90*86400
        UNION ALL
        SELECT '90d+' AS label, COUNT(*) AS cnt FROM filtered WHERE seconds >= 90*86400
    )
    SELECT label,
           cnt AS count,
           CASE WHEN t.total > 0 THEN ROUND((cnt * 100.0 / t.total)::numeric, 2) ELSE 0 END AS percent
    FROM buckets, total t;`

	rows, err := ExecuteQuery(ctx, p.db, query, from, to, fingerprint)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

	var results []QueryTimeRangeDistributionResult
	for rows.Next() {
		var r QueryTimeRangeDistributionResult
		if err := rows.Scan(&r.Label, &r.Count, &r.Percent); err != nil {
			return nil, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrorWithOperation(err, "row iteration")
	}
	return results, nil
}

// GetRecentQueries removed (endpoint deprecated)

// GetQueryExpressions aggregates queries by fingerprint returning executions, avgDuration, errorRatePercent, peakSamples and latest query text
func (p *PostGreSQLProvider) GetQueryExpressions(ctx context.Context, params QueryExpressionsParams) (PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}

	validSortFields := map[string]bool{
		"query":            true,
		"executions":       true,
		"avgDuration":      true,
		"errorRatePercent": true,
		"peakSamples":      true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "executions")

	from, to := params.TimeRange.Format(ISOTimeFormat)

	baseQuery := `
        WITH filtered AS (
            SELECT *
            FROM queries
            WHERE ts BETWEEN $1 AND $2
              AND CASE WHEN $3 <> '' THEN queryParam ILIKE '%%' || $3 || '%%' ELSE TRUE END
        ), grouped AS (
            SELECT
                fingerprint,
                COUNT(*) AS executions,
                ROUND(AVG(duration)::numeric, 2) AS avgDuration,
                ROUND((SUM(CASE WHEN statusCode >= 400 THEN 1 ELSE 0 END) * 100.0 / NULLIF(COUNT(*),0))::numeric, 2) AS errorRatePercent,
                MAX(peakSamples) AS peakSamples,
                (ARRAY_AGG(queryParam ORDER BY ts DESC))[1] AS query
            FROM filtered
            GROUP BY fingerprint
        ), counted AS (
            SELECT COUNT(*) AS total_count FROM grouped
        )
        SELECT fingerprint, query, executions, avgDuration, errorRatePercent, peakSamples, total_count
        FROM grouped, counted
    `

	// Safe ORDER BY
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $4 OFFSET $5;"

	rows, err := ExecuteQuery(ctx, p.db, query, from, to, params.Filter, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return PagedResult{}, err
	}
	defer CloseResource(rows)

	var (
		results    []QueryExpression
		totalCount int
	)
	for rows.Next() {
		var r QueryExpression
		if err := rows.Scan(&r.Fingerprint, &r.Query, &r.Executions, &r.AvgDuration, &r.ErrorRatePercent, &r.PeakSamples, &totalCount); err != nil {
			return PagedResult{}, fmt.Errorf("scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return PagedResult{}, fmt.Errorf("row iter: %w", err)
	}

	totalPages := CalculateTotalPages(totalCount, params.PageSize)
	return PagedResult{Total: totalCount, TotalPages: totalPages, Data: results}, nil
}

// GetQueryExecutions returns raw executions filtered by fingerprint/time range with steps computed in DB
func (p *PostGreSQLProvider) GetQueryExecutions(ctx context.Context, params QueryExecutionsParams) (PagedResult, error) {
	ValidatePagination(&params.Page, &params.PageSize, 10)
	validSort := map[string]bool{
		"ts":         true,
		"duration":   true,
		"samples":    true,
		"statusCode": true,
		"type":       true,
		"steps":      true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSort, "ts")

	from, to := params.TimeRange.Format(ISOTimeFormat)

	base := `
        WITH filtered AS (
            SELECT ts, statusCode, duration, totalQueryableSamples AS samples, type, start, "end", step
            FROM queries
            WHERE ts BETWEEN $1 AND $2
              AND fingerprint = $3
              AND ($4 = '' OR type = $4)
        ), counted AS (
            SELECT COUNT(*) AS total_count FROM filtered
        )
        SELECT ts, statusCode, duration, samples, type,
               COALESCE(step, 0) AS steps,
               total_count
        FROM filtered, counted
    `

	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := base + orderClause + " LIMIT $5 OFFSET $6;"

	offset := (params.Page - 1) * params.PageSize
	rows, err := ExecuteQuery(ctx, p.db, query, from, to, params.Fingerprint, params.Type, params.PageSize, offset)
	if err != nil {
		return PagedResult{}, err
	}
	defer CloseResource(rows)

	type row struct {
		ts         time.Time
		status     int
		duration   int64
		samples    int
		typ        string
		steps      float64
		totalCount int
	}
	var (
		results []QueryExecutionRow
		total   int
	)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ts, &r.status, &r.duration, &r.samples, &r.typ, &r.steps, &r.totalCount); err != nil {
			return PagedResult{}, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, QueryExecutionRow{Timestamp: r.ts, Status: r.status, Duration: r.duration, Samples: r.samples, Type: r.typ, Steps: r.steps})
		total = r.totalCount
	}
	if err := rows.Err(); err != nil {
		return PagedResult{}, ErrorWithOperation(err, "row iteration")
	}
	return PagedResult{Total: total, TotalPages: CalculateTotalPages(total, params.PageSize), Data: results}, nil
}

func (p *PostGreSQLProvider) GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error) {
	query := `
	WITH rule_stats AS (
		SELECT
            COUNT(*) FILTER (WHERE serie = $3 AND kind = 'alert')  AS alert_count,
            COUNT(*) FILTER (WHERE serie = $3 AND kind = 'record') AS record_count,
            COUNT(*) FILTER (WHERE kind = 'alert')                 AS total_alerts,
            COUNT(*) FILTER (WHERE kind = 'record')                AS total_records
		FROM   rulesusage
        WHERE  first_seen_at <= $2 AND last_seen_at >= $1
	),
	dash_stats AS (
		SELECT
			COUNT(DISTINCT name) FILTER (WHERE serie = $3) AS dashboard_count,
			COUNT(DISTINCT name)                           AS total_dashboards
		FROM   dashboardusage
		WHERE  first_seen_at <= $2 AND last_seen_at >= $1
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

	from, to := PrepareTimeRange(tr, "postgresql")
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
