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

// SeriesMetadata SQL literals for the PostgreSQL backend.
//
// Each pair is the static SQL backing one branch of GetSeriesMetadata:
//   - pgSeriesMetadataCountSQL / pgSeriesMetadataBaseSQL drive the used/all
//     branch via the catalog-driven LEFT JOIN shape.
//   - pgSeriesMetadataUnusedCountSQL / pgSeriesMetadataUnusedBaseSQL drive
//     the ?usage=unused branch from metrics_usage_summary via the
//     is_unused partial index, INNER JOIN metrics_catalog.
//   - pgSeriesMetadataUnusedJobCountSQL / pgSeriesMetadataUnusedJobBaseSQL
//     drive ?usage=unused&job=<X> from metrics_job_index so per-request
//     work scales with the size of the requested job's metric set rather
//     than the entire unused universe.
//
// These are exposed as named consts (rather than inlined) so the Go SQL
// scanner sees QueryContext receiving a string that does not depend on
// user input, and so the queries are addressable from unit tests that
// assert their shape and that they prepare cleanly against PostgreSQL.
// The `%%` sequences in the Base SQL strings survive through
// BuildSafeQueryWithOrderBy's fmt.Sprintf as `%%` (Sprintf does not
// re-interpret `%` in the argument); PostgreSQL collapses consecutive
// `%` in a LIKE/ILIKE pattern so the matched range is unchanged.
const pgSeriesMetadataCountSQL = `
        SELECT COUNT(*)
        FROM metrics_catalog c
        LEFT JOIN metrics_usage_summary s ON s.name = c.name
        WHERE ($1 = '' OR c.name ILIKE '%' || $1 || '%' OR c.help ILIKE '%' || $1 || '%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
          AND (
                $3 = 'all'
                OR ($3 = 'used' AND (
                     COALESCE(s.alert_count,0) > 0
                     OR COALESCE(s.record_count,0) > 0
                     OR COALESCE(s.dashboard_count,0) > 0
                     OR COALESCE(s.query_count,0) > 0
                ))
          )
          AND ($4 = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = $4
          ))
    `

const pgSeriesMetadataBaseSQL = `
        SELECT c.name, c.type, c.help, c.unit,
               COALESCE(s.alert_count,0), COALESCE(s.record_count,0), COALESCE(s.dashboard_count,0), COALESCE(s.query_count,0), s.last_queried_at
        FROM metrics_catalog c
        LEFT JOIN metrics_usage_summary s ON s.name = c.name
        WHERE ($1 = '' OR c.name ILIKE '%%' || $1 || '%%' OR c.help ILIKE '%%' || $1 || '%%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
          AND (
                $3 = 'all'
                OR ($3 = 'used' AND (
                     COALESCE(s.alert_count,0) > 0
                     OR COALESCE(s.record_count,0) > 0
                     OR COALESCE(s.dashboard_count,0) > 0
                     OR COALESCE(s.query_count,0) > 0
                ))
          )
          AND ($4 = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = $4
          ))
    `

const pgSeriesMetadataUnusedCountSQL = `
        SELECT COUNT(*)
        FROM metrics_usage_summary s
        JOIN metrics_catalog c ON c.name = s.name
        WHERE s.is_unused = TRUE
          AND ($1 = '' OR c.name ILIKE '%' || $1 || '%' OR c.help ILIKE '%' || $1 || '%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
    `

const pgSeriesMetadataUnusedBaseSQL = `
        SELECT c.name, c.type, c.help, c.unit,
               s.alert_count, s.record_count, s.dashboard_count, s.query_count, s.last_queried_at
        FROM metrics_usage_summary s
        JOIN metrics_catalog c ON c.name = s.name
        WHERE s.is_unused = TRUE
          AND ($1 = '' OR c.name ILIKE '%%' || $1 || '%%' OR c.help ILIKE '%%' || $1 || '%%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
    `

const pgSeriesMetadataUnusedJobCountSQL = `
        SELECT COUNT(*)
        FROM metrics_job_index j
        JOIN metrics_usage_summary s ON s.name = j.name
        JOIN metrics_catalog c ON c.name = j.name
        WHERE j.job = $3
          AND s.is_unused = TRUE
          AND ($1 = '' OR c.name ILIKE '%' || $1 || '%' OR c.help ILIKE '%' || $1 || '%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
    `

const pgSeriesMetadataUnusedJobBaseSQL = `
        SELECT c.name, c.type, c.help, c.unit,
               s.alert_count, s.record_count, s.dashboard_count, s.query_count, s.last_queried_at
        FROM metrics_job_index j
        JOIN metrics_usage_summary s ON s.name = j.name
        JOIN metrics_catalog c ON c.name = j.name
        WHERE j.job = $3
          AND s.is_unused = TRUE
          AND ($1 = '' OR c.name ILIKE '%%' || $1 || '%%' OR c.help ILIKE '%%' || $1 || '%%')
          AND ($2 = 'all' OR
               CASE
                  WHEN $2 = 'histogram' THEN c.type IN ('histogram_bucket', 'histogram_count', 'histogram_sum')
                  WHEN $2 = 'summary' THEN c.type IN ('summary', 'summary_count', 'summary_sum')
                  ELSE c.type = $2
                END)
    `

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
	flagSet.IntVar(&config.DefaultConfig.Database.PostgreSQL.MaxOpenConns, "postgresql-max-open-conns", 0, "Maximum number of open connections to the database (0 = use default 20).")
	flagSet.IntVar(&config.DefaultConfig.Database.PostgreSQL.MaxIdleConns, "postgresql-max-idle-conns", 0, "Maximum number of idle connections in the pool (0 = use default 10).")
	flagSet.DurationVar(&config.DefaultConfig.Database.PostgreSQL.ConnMaxLifetime, "postgresql-conn-max-lifetime", 0, "Maximum amount of time a connection may be reused (0 = use default 30m).")
	flagSet.DurationVar(&config.DefaultConfig.Database.PostgreSQL.ConnMaxIdleTime, "postgresql-conn-max-idle-time", 0, "Maximum amount of time a connection may be idle before being closed (0 = use default 5m).")
	flagSet.DurationVar(&config.DefaultConfig.Database.PostgreSQL.StatementTimeout, "postgresql-statement-timeout", 0,
		"Server-side per-statement timeout enforced by PostgreSQL on every pooled connection. "+
			"When a single query exceeds this, PostgreSQL aborts it (SQLSTATE 57014) and frees the connection slot "+
			"before the HTTP client can cancel it - useful as a load-shedder under saturation. "+
			"Set just under the expected client per-call deadline (e.g. 4500ms when clients use 5s). 0 disables.")
}

func NewPostgreSQLProvider(ctx context.Context, postgresConfig config.PostgreSQLConfig) (Provider, error) {
	// statement_timeout is set via the connection-string 'options' parameter
	// so it takes effect on every connection the pool opens, with no
	// per-checkout overhead. lib/pq preserves the space inside the single-
	// quoted value, and postgres then parses '-c' and 'statement_timeout=...'
	// as two separate command-line arguments at connection start.
	// Round up to the nearest millisecond so a sub-ms configured duration
	// doesn't silently truncate to 0, which PostgreSQL interprets as
	// "disabled" - the opposite of the operator's intent.
	statementTimeoutOpt := ""
	if postgresConfig.StatementTimeout > 0 {
		ms := (postgresConfig.StatementTimeout + time.Millisecond - 1) / time.Millisecond
		statementTimeoutOpt = fmt.Sprintf(
			" options='-c statement_timeout=%d'",
			ms,
		)
	}
	psqlInfo := fmt.Sprintf(
		"host='%s' port=%d user='%s' password='%s' dbname='%s' sslmode='%s' connect_timeout=%d application_name=prom-analytics-proxy%s",
		postgresConfig.Addr,
		postgresConfig.Port,
		postgresConfig.User,
		postgresConfig.Password,
		postgresConfig.Database,
		postgresConfig.SSLMode,
		int(postgresConfig.DialTimeout.Seconds()),
		statementTimeoutOpt,
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
	// Set MaxIdleTime to prevent stale connections
	// Idle connections older than the configured time will be closed
	if postgresConfig.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(postgresConfig.ConnMaxIdleTime)
	} else {
		db.SetConnMaxIdleTime(5 * time.Minute)
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

	// Use a prepared INSERT to batch rows within a single transaction
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return QueryError(err, "begin insert tx", "")
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO queries (
		ts, queryparam, timeparam, duration, statuscode, bodysize,
		fingerprint, labelmatchers, type, step, start, "end",
		totalqueryablesamples, peaksamples, httpheaders
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`)
	if err != nil {
		_ = tx.Rollback()
		return QueryError(err, "prepare insert", "")
	}

	for _, q := range queries {
		labelMatchersJSON, err := json.Marshal(q.LabelMatchers)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return QueryError(err, "marshal label matchers", "")
		}
		httpHeadersJSON, err := json.Marshal(q.HTTPHeaders)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return QueryError(err, "marshal httpHeaders", "")
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
			string(httpHeadersJSON),
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return QueryError(err, "insert exec", "")
		}
	}

	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return QueryError(err, "insert close", "")
	}
	if err := tx.Commit(); err != nil {
		return QueryError(err, "insert commit", "")
	}
	return nil
}

func (p *PostGreSQLProvider) GetQueriesBySerieName(
	ctx context.Context,
	params QueriesBySerieNameParams) (PagedResult, error) {

	ValidatePagination(&params.Page, &params.PageSize, 10)

	validSortFields := map[string]bool{
		"queryParam":      true,
		"avgDuration":     true,
		"avgPeakySamples": true,
		"maxPeakSamples":  true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "avgDuration")
	SetDefaultTimeRange(&params.TimeRange)

	// filterClause is shared with the count query below (see countMatching)
	// so both see the identical row set.
	filterClause := `
		labelMatchers @> $1::jsonb
		AND ts BETWEEN $2 AND $3
		AND CASE
			WHEN $4 != '' THEN
				queryParam ILIKE '%' || $4 || '%'
			ELSE
				TRUE
			END
	`
	filterArgs := []interface{}{
		metricMatcherJSON(params.SerieName),
		params.TimeRange.From,
		params.TimeRange.To,
		params.Filter,
	}

	countQuery := `SELECT COUNT(DISTINCT queryParam) FROM queries WHERE ` + filterClause
	totalCount, err := countMatching(ctx, p.db, countQuery, filterArgs...)
	if err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if totalCount == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []QueriesBySerieNameResult{}}, nil
	}

	baseQuery := `
	SELECT
		queryParam,
		AVG(duration) AS avgDuration,
		AVG(peakSamples) AS avgPeakySamples,
		MAX(peakSamples) AS maxPeakSamples
	FROM
		queries
	WHERE ` + filterClause + `
	GROUP BY
		queryParam
	`
	// Build ORDER BY clause dynamically to avoid mixed-type CASE expressions
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $5 OFFSET $6;"

	args := append(append([]interface{}{}, filterArgs...), params.PageSize, (params.Page-1)*params.PageSize)

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return PagedResult{}, err
	}
	defer CloseResource(rows)

	var results []QueriesBySerieNameResult

	for rows.Next() {
		var result QueriesBySerieNameResult
		if err := rows.Scan(
			&result.Query,
			&result.AvgDuration,
			&result.AvgPeakySamples,
			&result.MaxPeakSamples,
		); err != nil {
			return PagedResult{}, ErrorWithOperation(err, "scanning row")
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return PagedResult{}, ErrorWithOperation(err, "row iteration")
	}

	totalPages := CalculateTotalPages(totalCount, params.PageSize)

	return PagedResult{
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

	// Sort by the same composite key as the ON CONFLICT target below before
	// upserting, so this function stays safe under concurrent calls with
	// overlapping rows in any order - the same deadlock precondition as
	// #592.
	sort.Slice(normalized, func(i, j int) bool {
		a, b := normalized[i], normalized[j]
		if a.Serie != b.Serie {
			return a.Serie < b.Serie
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.GroupName != b.GroupName {
			return a.GroupName < b.GroupName
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Expression != b.Expression {
			return a.Expression < b.Expression
		}
		// labels is part of the ON CONFLICT target too - two rows can
		// share every other field and still be distinct conflict targets
		// differing only in labels. Each item's Labels is already sorted
		// (a few lines above), so joining is a stable, comparable form.
		return strings.Join(a.Labels, ",") < strings.Join(b.Labels, ",")
	})

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

func (p *PostGreSQLProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (PagedResult, error) {
	ValidatePagination(&params.Page, &params.PageSize, 10)
	validSortFields := map[string]bool{
		"name":       true,
		"group_name": true,
		"expression": true,
		"created_at": true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "created_at")
	SetDefaultTimeRange(&params.TimeRange)

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
	totalCount, err := countMatching(ctx, p.db, countQuery, params.Serie, params.Kind, startTime, endTime, params.Filter)
	if err != nil {
		return PagedResult{}, fmt.Errorf("failed to query total count: %w", err)
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
		return PagedResult{}, err
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
			return PagedResult{}, fmt.Errorf("failed to scan row: %w", err)
		}

		var labels []string
		if len(labelsJSON) > 0 {
			if err := json.Unmarshal(labelsJSON, &labels); err != nil {
				return PagedResult{}, fmt.Errorf("failed to unmarshal labels: %w", err)
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
		return PagedResult{}, fmt.Errorf("row iteration error: %w", err)
	}

	return PagedResult{
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

	// Collect into a slice and sort by (id, serie) - the same ON CONFLICT
	// target below - instead of iterating the dedup map directly, so this
	// function stays safe under concurrent calls with overlapping rows in
	// any order - the same deadlock precondition as #592.
	normalized := make([]DashboardUsage, 0, len(dedup))
	for _, d := range dedup {
		normalized = append(normalized, d)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Id != normalized[j].Id {
			return normalized[i].Id < normalized[j].Id
		}
		return normalized[i].Serie < normalized[j].Serie
	})

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
	for _, d := range normalized {
		if _, err := stmt.ExecContext(ctx, d.Id, d.Serie, d.Name, d.URL, now); err != nil {
			return fmt.Errorf("failed to execute upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (p *PostGreSQLProvider) GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (PagedResult, error) {
	ValidatePagination(&params.Page, &params.PageSize, 10)
	validSortFields := map[string]bool{
		"name":       true,
		"url":        true,
		"created_at": true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "created_at")
	SetDefaultTimeRange(&params.TimeRange)

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
	totalCount, err := countMatching(ctx, p.db, countQuery, params.Serie, from, to, params.Filter)
	if err != nil {
		return PagedResult{}, fmt.Errorf("failed to query total count: %w", err)
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
		return PagedResult{}, err
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
			return PagedResult{}, fmt.Errorf("failed to scan row: %w", err)
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
		return PagedResult{}, fmt.Errorf("row iteration error: %w", err)
	}

	return PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *PostGreSQLProvider) GetSeriesMetadata(ctx context.Context, params SeriesMetadataParams) (PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "queryCount"
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	params.Usage = NormalizeSeriesMetadataUsage(params.Usage)

	// usage=unused is handled by a dedicated method with a distinct query
	// shape (driven from metrics_usage_summary via the is_unused partial
	// index, INNER JOIN metrics_catalog). Splitting it out keeps each path's
	// SQL as a single static literal - this matters for code scanning,
	// which otherwise flags QueryContext as having a query string that
	// control-depends on the user-controlled Usage value.
	if params.Usage == SeriesMetadataUsageUnused {
		return p.getSeriesMetadataUnused(ctx, params)
	}

	// Used / all: catalog-driven LEFT JOIN shape.
	var total int
	if err := p.db.QueryRowContext(ctx, pgSeriesMetadataCountSQL, params.Filter, params.Type, params.Usage, params.Job).Scan(&total); err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if total == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []models.MetricMetadata{}}, nil
	}

	query := BuildSafeQueryWithOrderBy(pgSeriesMetadataBaseSQL, "c", " LIMIT $5 OFFSET $6", params.SortBy, params.SortOrder, ValidSeriesMetadataSortFields, "queryCount", SeriesMetadataSortAliases)
	rows, err := p.db.QueryContext(ctx, query, params.Filter, params.Type, params.Usage, params.Job, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return PagedResult{}, fmt.Errorf("select: %w", err)
	}
	defer CloseResource(rows)

	out, err := scanSeriesMetadataRows(rows, params.PageSize)
	if err != nil {
		return PagedResult{}, err
	}
	pages := (total + params.PageSize - 1) / params.PageSize
	return PagedResult{Total: total, TotalPages: pages, Data: out}, nil
}

// getSeriesMetadataUnused drives the ?usage=unused query from
// metrics_usage_summary via the is_unused partial index, INNER JOIN
// metrics_catalog. This lets the planner satisfy the unused subset with an
// index scan instead of walking the full catalog and filtering. The two
// SQL strings here are static literals; the conditional that picks this
// method lives in GetSeriesMetadata.
//
// When a job filter is provided the optimal access pattern flips - driving
// from is_unused=TRUE and EXISTS-probing metrics_job_index per row scales
// with the entire unused universe rather than with the requested job, which
// catastrophically misses for jobs that match a small slice of unused
// metrics. The job case is therefore handled by getSeriesMetadataUnusedJobScoped,
// which drives from metrics_job_index instead.
func (p *PostGreSQLProvider) getSeriesMetadataUnused(ctx context.Context, params SeriesMetadataParams) (PagedResult, error) {
	if params.Job != "" {
		return p.getSeriesMetadataUnusedJobScoped(ctx, params)
	}

	var total int
	if err := p.db.QueryRowContext(ctx, pgSeriesMetadataUnusedCountSQL, params.Filter, params.Type).Scan(&total); err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if total == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []models.MetricMetadata{}}, nil
	}

	// Force ORDER BY c.name for the unused branch regardless of the
	// client-supplied sortBy. Every unused row has alert_count =
	// record_count = dashboard_count = query_count = 0, so sorting by any
	// of those count columns is meaningless (all values tie at zero) and
	// forces the planner to materialise the full unused subset before
	// LIMIT can apply - which on cx10 (139k unused) cost seconds even for
	// a 10-row page. Sorting by c.name lines up with the
	// idx_metrics_usage_summary_is_unused partial index's name ordering,
	// so the planner can index-scan + LIMIT in O(LIMIT) work. sortOrder is
	// still honoured so ?sortOrder=desc inverts the alphabetical listing.
	query := BuildSafeQueryWithOrderBy(pgSeriesMetadataUnusedBaseSQL, "c", " LIMIT $3 OFFSET $4", "name", params.SortOrder, ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases)
	rows, err := p.db.QueryContext(ctx, query, params.Filter, params.Type, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return PagedResult{}, fmt.Errorf("select: %w", err)
	}
	defer CloseResource(rows)

	out, err := scanSeriesMetadataRows(rows, params.PageSize)
	if err != nil {
		return PagedResult{}, err
	}
	pages := (total + params.PageSize - 1) / params.PageSize
	return PagedResult{Total: total, TotalPages: pages, Data: out}, nil
}

// getSeriesMetadataUnusedJobScoped drives ?usage=unused&job=<X> from
// metrics_job_index (filtered to the requested job via the composite
// (job, name) index) and INNER JOINs to metrics_usage_summary (filtered by
// is_unused=TRUE) and metrics_catalog. This makes per-request work scale
// with the size of the requested job's metric set, not with the entire
// unused universe - the latter is what cratered cx10 when the operator's
// ?usage=unused&job=kube-state-metrics request hit 139k unused metrics
// looking for a sparse 57-row match.
func (p *PostGreSQLProvider) getSeriesMetadataUnusedJobScoped(ctx context.Context, params SeriesMetadataParams) (PagedResult, error) {
	var total int
	if err := p.db.QueryRowContext(ctx, pgSeriesMetadataUnusedJobCountSQL, params.Filter, params.Type, params.Job).Scan(&total); err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if total == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []models.MetricMetadata{}}, nil
	}

	// Force ORDER BY c.name; see getSeriesMetadataUnused for rationale.
	// The same all-zero-counts pattern applies here, and consistency
	// matters because clients page through both shapes against the same
	// API contract.
	query := BuildSafeQueryWithOrderBy(pgSeriesMetadataUnusedJobBaseSQL, "c", " LIMIT $4 OFFSET $5", "name", params.SortOrder, ValidSeriesMetadataSortFields, "name", SeriesMetadataSortAliases)
	rows, err := p.db.QueryContext(ctx, query, params.Filter, params.Type, params.Job, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return PagedResult{}, fmt.Errorf("select: %w", err)
	}
	defer CloseResource(rows)

	out, err := scanSeriesMetadataRows(rows, params.PageSize)
	if err != nil {
		return PagedResult{}, err
	}
	pages := (total + params.PageSize - 1) / params.PageSize
	return PagedResult{Total: total, TotalPages: pages, Data: out}, nil
}

// scanSeriesMetadataRows scans rows returned by either GetSeriesMetadata
// branch into a slice of models.MetricMetadata. Both branches return the
// same column layout (the unused branch drops the COALESCE wrappers since
// the INNER JOIN guarantees non-NULL summary counts, but the scanned
// Go types are identical).
func scanSeriesMetadataRows(rows *sql.Rows, pageSize int) ([]models.MetricMetadata, error) {
	type row struct {
		name, mtype, help, unit     string
		alert, record, dash, qcount int
		last                        sql.NullTime
	}
	out := make([]models.MetricMetadata, 0, pageSize)
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
	return out, nil
}

func (p *PostGreSQLProvider) GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error) {
	if len(names) == 0 {
		return []models.MetricMetadata{}, nil
	}

	var (
		query string
		args  []any
	)

	// COALESCE(s.is_unused, FALSE) is the single source of truth for
	// "confirmed unused" - it must not be recomputed from the four counts
	// below, since a metric that was never evaluated by
	// RefreshMetricsUsageSummary also has all-zero counts (see the
	// placeholder insert in UpsertMetricsCatalog) and a missing summary row
	// (FALSE from COALESCE) must fail toward "keep" the same way. See
	// https://github.com/nicolastakashi/prom-analytics-proxy/issues/571.
	if job != "" {
		query = `
            SELECT c.name, c.type, c.help, c.unit,
                   COALESCE(s.alert_count,0), COALESCE(s.record_count,0), COALESCE(s.dashboard_count,0), COALESCE(s.query_count,0), s.last_queried_at,
                   COALESCE(s.is_unused, FALSE)
            FROM metrics_catalog c
            LEFT JOIN metrics_usage_summary s ON s.name = c.name
            WHERE c.name = ANY($1)
              AND EXISTS (SELECT 1 FROM metrics_job_index j WHERE j.name = c.name AND j.job = $2)
        `
		args = []any{pq.Array(names), job}
	} else {
		query = `
            SELECT c.name, c.type, c.help, c.unit,
                   COALESCE(s.alert_count,0), COALESCE(s.record_count,0), COALESCE(s.dashboard_count,0), COALESCE(s.query_count,0), s.last_queried_at,
                   COALESCE(s.is_unused, FALSE)
            FROM metrics_catalog c
            LEFT JOIN metrics_usage_summary s ON s.name = c.name
            WHERE c.name = ANY($1)
        `
		args = []any{pq.Array(names)}
	}

	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer CloseResource(rows)

	type row struct {
		name, mtype, help, unit     string
		alert, record, dash, qcount int
		last                        sql.NullTime
		isUnused                    bool
	}
	out := make([]models.MetricMetadata, 0, len(names))
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.mtype, &r.help, &r.unit, &r.alert, &r.record, &r.dash, &r.qcount, &r.last, &r.isUnused); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		mm := models.MetricMetadata{Name: r.name, Type: r.mtype, Help: r.help, Unit: r.unit, AlertCount: r.alert, RecordCount: r.record, DashboardCount: r.dash, QueryCount: r.qcount, IsUnused: r.isUnused}
		if r.last.Valid {
			mm.LastQueriedAt = r.last.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, mm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter: %w", err)
	}
	return out, nil
}

// upsertMetricsCatalogItems de-duplicates items by name (last occurrence
// wins) and sorts the result by name. Both are required for
// UpsertMetricsCatalog to stay safe under concurrent calls with
// overlapping items, in any relative order or interleaving:
//
//   - Sorting guarantees every call, however many run concurrently,
//     always acquires metrics_catalog row locks in the same order. Two
//     concurrent calls upserting overlapping rows in different orders is
//     Postgres's own documented deadlock precondition (#592).
//   - De-duplication is required independently: a single bulk
//     INSERT ... ON CONFLICT DO UPDATE statement errors ("ON CONFLICT DO
//     UPDATE command cannot affect row a second time") if its own input
//     contains the same conflict target twice.
func upsertMetricsCatalogItems(items []MetricCatalogItem) []MetricCatalogItem {
	deduped := make(map[string]MetricCatalogItem, len(items))
	for _, it := range items {
		deduped[it.Name] = it
	}
	sorted := make([]MetricCatalogItem, 0, len(deduped))
	for _, it := range deduped {
		sorted = append(sorted, it)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}

func (p *PostGreSQLProvider) UpsertMetricsCatalog(ctx context.Context, items []MetricCatalogItem) error {
	if len(items) == 0 {
		return nil
	}
	items = upsertMetricsCatalogItems(items)

	names := make([]string, len(items))
	types := make([]string, len(items))
	helps := make([]string, len(items))
	units := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
		types[i] = it.Type
		helps[i] = it.Help
		units[i] = it.Unit
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Both statements below run as one bulk INSERT via unnest() rather than
	// one round-trip per item - the syncer calls this with the full catalog
	// inventory (~tens of thousands of rows on a real Prometheus).
	//
	// last_synced_at is stored as TIMESTAMP WITHOUT TIME ZONE, so a bare NOW()
	// lands in the server session's time zone while RefreshMetricsUsageSummary
	// compares it against tr.From.UTC() (see PrepareTimeRange). AT TIME ZONE
	// 'UTC' pins the written value to UTC so the two stay in the same clock
	// domain regardless of session TimeZone.
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO metrics_catalog(name, type, help, unit, last_synced_at)
        SELECT n, t, h, u, NOW() AT TIME ZONE 'UTC'
        FROM unnest($1::text[], $2::text[], $3::text[], $4::text[]) AS x(n, t, h, u)
        ON CONFLICT(name) DO UPDATE SET
          type=EXCLUDED.type,
          help=EXCLUDED.help,
          unit=EXCLUDED.unit,
          last_synced_at=EXCLUDED.last_synced_at
    `, pq.Array(names), pq.Array(types), pq.Array(helps), pq.Array(units)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec: %w", err)
	}

	// Co-write a placeholder summary row per catalog item so the
	// metrics_catalog <-> metrics_usage_summary invariant holds before
	// the next RefreshMetricsUsageSummary. ON CONFLICT DO NOTHING means
	// existing rows with real counts are not clobbered.
	//
	// is_unused=FALSE here, not TRUE: a metric that has never been
	// evaluated is not the same thing as one confirmed to have zero usage,
	// and defaulting to TRUE previously caused every newly-catalogued
	// metric to be treated as unused (and dropped/hidden) until the next
	// successful RefreshMetricsUsageSummary run - which, if that job is
	// failing, could be never. See
	// https://github.com/nicolastakashi/prom-analytics-proxy/issues/570.
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO metrics_usage_summary(name, alert_count, record_count, dashboard_count, query_count, updated_at, is_unused)
        SELECT n, 0, 0, 0, 0, NOW(), FALSE FROM unnest($1::text[]) AS n
        ON CONFLICT (name) DO NOTHING
    `, pq.Array(names)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec summary: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// refreshMetricsUsageSummaryQueryPostgreSQL is kept as a package-level const
// so a test can assert on the exact statement RefreshMetricsUsageSummary
// runs without risking drift between a duplicated copy and the real query.
const refreshMetricsUsageSummaryQueryPostgreSQL = `
    INSERT INTO metrics_usage_summary(name, alert_count, record_count, dashboard_count, query_count, last_queried_at, updated_at, is_unused)
    SELECT c.name,
           COALESCE(ra.alert_count, 0),
           COALESCE(ra.record_count, 0),
           COALESCE(da.dashboard_count, 0),
           COALESCE(qa.query_count, 0),
           qa.last_queried_at,
           NOW(),
           (COALESCE(ra.alert_count, 0) = 0
            AND COALESCE(ra.record_count, 0) = 0
            AND COALESCE(da.dashboard_count, 0) = 0
            AND COALESCE(qa.query_count, 0) = 0)
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
    -- Only recompute catalog rows Prometheus has actually reconfirmed
    -- within the window: this bounds the scan to actively-synced metrics
    -- instead of every name ever seen, however long ago. See
    -- https://github.com/nicolastakashi/prom-analytics-proxy/issues/579.
    -- Rows that age out simply keep whatever summary they last had. Reuses
    -- $1 rather than adding a new bind param, since it's the same lower
    -- bound already used above.
    WHERE c.last_synced_at >= $1
    ON CONFLICT(name) DO UPDATE SET
        alert_count=EXCLUDED.alert_count,
        record_count=EXCLUDED.record_count,
        dashboard_count=EXCLUDED.dashboard_count,
        query_count=EXCLUDED.query_count,
        last_queried_at=EXCLUDED.last_queried_at,
        updated_at=EXCLUDED.updated_at,
        is_unused=EXCLUDED.is_unused;
    `

func (p *PostGreSQLProvider) RefreshMetricsUsageSummary(ctx context.Context, tr TimeRange) error {
	from, to := PrepareTimeRange(tr, "postgresql")
	res, err := p.db.ExecContext(ctx, refreshMetricsUsageSummaryQueryPostgreSQL, from, to)
	if err != nil {
		return fmt.Errorf("refresh summary: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		warnIfSummaryRefreshWasNoOp(ctx, p.db, n, "postgresql")
	}
	return nil
}

func (p *PostGreSQLProvider) UpsertMetricsJobIndex(ctx context.Context, items []MetricJobIndexItem) error {
	if len(items) == 0 {
		return nil
	}
	// Sort by (name, job) before upserting, so this function stays safe
	// under concurrent calls with overlapping rows in any order - the same
	// deadlock precondition as #592. Unlike UpsertMetricsCatalog, no
	// de-duplication is needed: the per-row loop stays as-is, so a
	// repeated (name, job) pair within one call is just two separate
	// statements, not the single-statement "ON CONFLICT DO UPDATE command
	// cannot affect row a second time" case that requires it there.
	sorted := make([]MetricJobIndexItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Job < sorted[j].Job
	})

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
	for _, it := range sorted {
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

func (p *PostGreSQLProvider) GetQueryStatusDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryStatusDistributionResult, error) {
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
		AND  ($4 = '' OR fingerprint = $4)
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

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to, fingerprint)
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

func (p *PostGreSQLProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryErrorAnalysisResult, error) {
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
		AND  ($4 = '' OR fingerprint = $4)
		GROUP  BY bucket
	)

	SELECT
		b.bucket                 AS time,
		COALESCE(a.value, 0)     AS value
	FROM   buckets b
	LEFT   JOIN agg a USING (bucket)
	ORDER  BY b.bucket;
	`

	rows, err := ExecuteQuery(ctx, p.db, query, from, interval, to, fingerprint)
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
	ValidatePagination(&params.Page, &params.PageSize, 10)

	validSortFields := map[string]bool{
		"query":            true,
		"executions":       true,
		"avgDuration":      true,
		"errorRatePercent": true,
		"peakSamples":      true,
	}
	ValidateSortField(&params.SortBy, &params.SortOrder, validSortFields, "executions")

	from, to := params.TimeRange.Format(ISOTimeFormat)

	// filterClause is shared with the count query below (see countMatching)
	// so both see the identical row set.
	filterClause := `ts BETWEEN $1 AND $2 AND CASE WHEN $3 <> '' THEN queryParam ILIKE '%%' || $3 || '%%' ELSE TRUE END`
	filterArgs := []interface{}{from, to, params.Filter}

	countQuery := `SELECT COUNT(DISTINCT fingerprint) FROM queries WHERE ` + filterClause
	totalCount, err := countMatching(ctx, p.db, countQuery, filterArgs...)
	if err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if totalCount == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []QueryExpression{}}, nil
	}

	baseQuery := `
        WITH filtered AS (
            SELECT *
            FROM queries
            WHERE ` + filterClause + `
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
        )
        SELECT fingerprint, query, executions, avgDuration, errorRatePercent, peakSamples
        FROM grouped
    `

	// Safe ORDER BY
	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := baseQuery + orderClause + " LIMIT $4 OFFSET $5;"

	args := append(append([]interface{}{}, filterArgs...), params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return PagedResult{}, err
	}
	defer CloseResource(rows)

	var results []QueryExpression
	for rows.Next() {
		var r QueryExpression
		if err := rows.Scan(&r.Fingerprint, &r.Query, &r.Executions, &r.AvgDuration, &r.ErrorRatePercent, &r.PeakSamples); err != nil {
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

	// filterClause is shared with the count query below (see countMatching)
	// so both see the identical row set.
	filterClause := `ts BETWEEN $1 AND $2 AND fingerprint = $3 AND ($4 = '' OR type = $4)`
	filterArgs := []interface{}{from, to, params.Fingerprint, params.Type}

	countQuery := `SELECT COUNT(*) FROM queries WHERE ` + filterClause
	total, err := countMatching(ctx, p.db, countQuery, filterArgs...)
	if err != nil {
		return PagedResult{}, fmt.Errorf("count: %w", err)
	}
	if total == 0 {
		return PagedResult{Total: 0, TotalPages: 0, Data: []QueryExecutionRow{}}, nil
	}

	base := `
        SELECT ts, statusCode, duration, totalQueryableSamples AS samples, type,
               COALESCE(step, 0) AS steps,
               httpHeaders, start, "end"
        FROM queries
        WHERE ` + filterClause + `
    `

	orderClause := fmt.Sprintf(" ORDER BY %s %s NULLS LAST", params.SortBy, strings.ToUpper(params.SortOrder))
	query := base + orderClause + " LIMIT $5 OFFSET $6;"

	offset := (params.Page - 1) * params.PageSize
	args := append(append([]interface{}{}, filterArgs...), params.PageSize, offset)
	rows, err := ExecuteQuery(ctx, p.db, query, args...)
	if err != nil {
		return PagedResult{}, err
	}
	defer CloseResource(rows)

	type row struct {
		ts          time.Time
		status      int
		duration    int64
		samples     int
		typ         string
		steps       float64
		httpHeaders []byte
		start       time.Time
		end         time.Time
	}
	var results []QueryExecutionRow
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ts, &r.status, &r.duration, &r.samples, &r.typ, &r.steps, &r.httpHeaders, &r.start, &r.end); err != nil {
			return PagedResult{}, ErrorWithOperation(err, "scanning row")
		}
		res := QueryExecutionRow{Timestamp: r.ts, Status: r.status, Duration: r.duration, Samples: r.samples, Type: r.typ, Steps: r.steps, Start: r.start, End: r.end}
		if len(r.httpHeaders) > 0 {
			if err := json.Unmarshal(r.httpHeaders, &res.HTTPHeaders); err != nil {
				return PagedResult{}, fmt.Errorf("unmarshal httpHeaders: %w", err)
			}
		}
		results = append(results, res)
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

func (p *PostGreSQLProvider) DeleteQueriesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := p.db.ExecContext(ctx, "DELETE FROM queries WHERE ts < $1", cutoff)
	if err != nil {
		return 0, ErrorWithOperation(err, "delete old queries")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, ErrorWithOperation(err, "get rows affected")
	}
	return rowsAffected, nil
}
