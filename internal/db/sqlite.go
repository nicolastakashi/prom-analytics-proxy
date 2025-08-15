package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	_ "modernc.org/sqlite"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type SQLiteProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

// DDL creation moved to embedded Goose migrations.

func RegisterSqliteFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&config.DefaultConfig.Database.SQLite.DatabasePath, "sqlite-database-path", "prom-analytics-proxy.db", "Path to the sqlite database.")
}

func newSqliteProvider(ctx context.Context) (Provider, error) {
	db, err := otelsql.Open("sqlite", config.DefaultConfig.Database.SQLite.DatabasePath, otelsql.WithAttributes(semconv.DBSystemSqlite))
	if err != nil {
		return nil, ConnectionError(err, "SQLite", "failed to open connection")
	}

	// SQLite benefits from a single writer connection
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		return nil, ConnectionError(err, "SQLite", "failed to ping database")
	}

	// Run embedded migrations (SQLite dialect)
	if err := runMigrations(ctx, db, "sqlite"); err != nil {
		return nil, SchemaError(err, "migration", "sqlite")
	}

	// Increase busy timeout to reduce SQLITE_BUSY during concurrent reads
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return nil, SchemaError(err, "pragma", "busy_timeout")
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
	// Serialize writes to avoid SQLITE_BUSY
	p.mu.Lock()
	defer p.mu.Unlock()
	// In-memory de-duplication and labels normalization
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

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO RulesUsage (
            serie, group_name, name, expression, kind, labels, created_at, first_seen_at, last_seen_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(serie, kind, group_name, name, expression, labels)
        DO UPDATE SET last_seen_at = excluded.last_seen_at
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
			now,
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

func (p *SQLiteProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	} else if params.PageSize > MaxPageSize {
		params.PageSize = MaxPageSize
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
        SELECT COUNT(DISTINCT kind || '|' || group_name || '|' || name)
        FROM RulesUsage
        WHERE serie = ? 
        AND kind = ?
        AND first_seen_at <= ? AND last_seen_at >= ?
        AND CASE 
            WHEN ? != '' THEN 
                (name LIKE '%' || ? || '%' OR expression LIKE '%' || ? || '%')
            ELSE 
                1=1
            END;
    `
	var totalCount int
	err := p.db.QueryRowContext(ctx, countQuery, params.Serie, params.Kind, endTime, startTime,
		params.Filter, params.Filter, params.Filter).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

	query := `
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
            WHERE serie = ? AND kind = ? 
            AND first_seen_at <= ? AND last_seen_at >= ?
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
        FROM overlapped
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
		params.Serie, params.Kind, endTime, startTime,
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
	// Serialize writes to avoid SQLITE_BUSY
	p.mu.Lock()
	defer p.mu.Unlock()
	// Dedup by (id, serie)
	type dashKey struct{ Id, Serie string }
	dedup := make(map[dashKey]DashboardUsage)
	for _, d := range dashboardUsage {
		dedup[dashKey{Id: d.Id, Serie: d.Serie}] = d
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
        ) VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id, serie) DO UPDATE SET last_seen_at = excluded.last_seen_at, name = excluded.name, url = excluded.url
    `)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer CloseResource(stmt)

	now := time.Now().UTC()
	for _, d := range dedup {
		if _, err := stmt.ExecContext(ctx, d.Id, d.Serie, d.Name, d.URL, now, now, now); err != nil {
			return fmt.Errorf("failed to execute upsert: %w", err)
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
        SELECT COUNT(DISTINCT id)
        FROM DashboardUsage
        WHERE serie = ? 
        AND first_seen_at <= ? AND last_seen_at >= ?
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
            WHERE serie = ? 
            AND first_seen_at <= ? AND last_seen_at >= ?
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
        FROM overlapped
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

func (p *SQLiteProvider) GetSeriesMetadata(ctx context.Context, params SeriesMetadataParams) (*PagedResult, error) {
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
	countQuery := `
        SELECT COUNT(*)
        FROM metrics_catalog c
        LEFT JOIN metrics_usage_summary s ON s.name = c.name
        WHERE (? = '' OR c.name LIKE '%' || ? || '%' OR c.help LIKE '%' || ? || '%')
          AND (? = 'all' OR c.type = ?)
          AND (CASE WHEN ? = 1 THEN COALESCE(s.alert_count,0)=0 AND COALESCE(s.record_count,0)=0 AND COALESCE(s.dashboard_count,0)=0 AND COALESCE(s.query_count,0)=0 ELSE 1 END)
          AND (? = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = ?
          ))
    `
	var total int
	if err := p.db.QueryRowContext(ctx, countQuery, params.Filter, params.Filter, params.Filter, params.Type, params.Type, boolToInt(params.Unused), params.Job, params.Job).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count catalog: %w", err)
	}

	if total == 0 {
		return &PagedResult{Total: 0, TotalPages: 0, Data: []models.MetricMetadata{}}, nil
	}

	// Query page with left join to usage summary
	baseQuery := `
        SELECT c.name, c.type, c.help, c.unit,
               COALESCE(s.alert_count, 0), COALESCE(s.record_count, 0), COALESCE(s.dashboard_count, 0), COALESCE(s.query_count, 0), s.last_queried_at
        FROM metrics_catalog AS c
        LEFT JOIN metrics_usage_summary AS s ON s.name = c.name
        WHERE (? = '' OR c.name LIKE '%%' || ? || '%%' OR c.help LIKE '%%' || ? || '%%')
          AND (? = 'all' OR c.type = ?)
          AND (CASE WHEN ? = 1 THEN COALESCE(s.alert_count,0)=0 AND COALESCE(s.record_count,0)=0 AND COALESCE(s.dashboard_count,0)=0 AND COALESCE(s.query_count,0)=0 ELSE 1 END)
          AND (? = '' OR EXISTS (
                SELECT 1 FROM metrics_job_index j
                WHERE j.name = c.name AND j.job = ?
          ))
    `
	// Build complete query with safe ORDER BY clause to prevent SQL injection
	query := BuildSafeQueryWithOrderBy(baseQuery, "c", " LIMIT ? OFFSET ?", params.SortBy, params.SortOrder, ValidSeriesMetadataSortFields, "name")

	rows, err := p.db.QueryContext(ctx, query,
		params.Filter, params.Filter, params.Filter,
		params.Type, params.Type,
		boolToInt(params.Unused),
		params.Job, params.Job,
		params.PageSize, (params.Page-1)*params.PageSize,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query series metadata: %w", err)
	}
	defer CloseResource(rows)

	type row struct {
		name, mtype, help, unit     string
		alert, record, dash, qcount int
		last                        sql.NullTime
	}
	results := make([]models.MetricMetadata, 0, params.PageSize)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.mtype, &r.help, &r.unit, &r.alert, &r.record, &r.dash, &r.qcount, &r.last); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		mm := models.MetricMetadata{
			Name:           r.name,
			Type:           r.mtype,
			Help:           r.help,
			Unit:           r.unit,
			AlertCount:     r.alert,
			RecordCount:    r.record,
			DashboardCount: r.dash,
			QueryCount:     r.qcount,
		}
		if r.last.Valid {
			mm.LastQueriedAt = r.last.Time.UTC().Format(time.RFC3339)
		}

		results = append(results, mm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	pages := (total + params.PageSize - 1) / params.PageSize
	return &PagedResult{Total: total, TotalPages: pages, Data: results}, nil
}

func (p *SQLiteProvider) UpsertMetricsCatalog(ctx context.Context, items []MetricCatalogItem) error {
	if len(items) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO metrics_catalog(name, type, help, unit, last_synced_at)
        VALUES(?, ?, ?, ?, datetime('now'))
        ON CONFLICT(name) DO UPDATE SET
            type=excluded.type,
            help=excluded.help,
            unit=excluded.unit,
            last_synced_at=excluded.last_synced_at
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

func (p *SQLiteProvider) UpsertMetricsJobIndex(ctx context.Context, items []MetricJobIndexItem) error {
	if len(items) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metrics_job_index(name, job, updated_at)
		VALUES(?, ?, datetime('now'))
		ON CONFLICT(name, job) DO UPDATE SET
			updated_at = excluded.updated_at
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

// ListJobs returns the distinct list of jobs from metrics_job_index
func (p *SQLiteProvider) ListJobs(ctx context.Context) ([]string, error) {
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

func (p *SQLiteProvider) RefreshMetricsUsageSummary(ctx context.Context, tr TimeRange) error {
	from, to := PrepareTimeRange(tr, "sqlite")
	query := `
    INSERT INTO metrics_usage_summary(name, alert_count, record_count, dashboard_count, query_count, last_queried_at, updated_at)
    SELECT c.name,
           COALESCE(ra.alert_count, 0),
           COALESCE(ra.record_count, 0),
           COALESCE(da.dashboard_count, 0),
           COALESCE(qa.query_count, 0),
           qa.last_queried_at,
           datetime('now')
    FROM metrics_catalog c
    LEFT JOIN (
        SELECT serie AS name,
               SUM(CASE WHEN kind = 'alert' THEN 1 ELSE 0 END) AS alert_count,
               SUM(CASE WHEN kind = 'record' THEN 1 ELSE 0 END) AS record_count
        FROM RulesUsage
        WHERE first_seen_at <= ? AND last_seen_at >= ?
        GROUP BY serie
    ) ra USING(name)
    LEFT JOIN (
        SELECT serie AS name,
               COUNT(DISTINCT id) AS dashboard_count
        FROM DashboardUsage
        WHERE first_seen_at <= ? AND last_seen_at >= ?
        GROUP BY serie
    ) da USING(name)
    LEFT JOIN (
        SELECT json_extract(labelMatchers, '$[0].__name__') AS name,
               COUNT(*) AS query_count,
               MAX(ts) AS last_queried_at
        FROM queries
        WHERE ts BETWEEN ? AND ?
        GROUP BY name
    ) qa USING(name)
    ON CONFLICT(name) DO UPDATE SET
        alert_count=excluded.alert_count,
        record_count=excluded.record_count,
        dashboard_count=excluded.dashboard_count,
        query_count=excluded.query_count,
        last_queried_at=excluded.last_queried_at,
        updated_at=excluded.updated_at;
    `
	_, err := p.db.ExecContext(ctx, query, to, from, to, from, from, to)
	if err != nil {
		return fmt.Errorf("refresh summary: %w", err)
	}
	return nil
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
            (SELECT COALESCE(SUM(CASE WHEN Kind = 'alert' THEN 1 ELSE 0 END), 0) FROM RulesUsage WHERE first_seen_at <= datetime(?) AND last_seen_at >= datetime(?)) as total_alerts,
            (SELECT COALESCE(SUM(CASE WHEN Kind = 'record' THEN 1 ELSE 0 END), 0) FROM RulesUsage WHERE first_seen_at <= datetime(?) AND last_seen_at >= datetime(?)) as total_records,
			(SELECT COALESCE(COUNT(DISTINCT Name), 0) FROM DashboardUsage WHERE created_at BETWEEN datetime(?) AND datetime(?)) as total_dashboards
		FROM RulesUsage r
		LEFT JOIN DashboardUsage d ON r.Serie = d.Serie
		WHERE r.Serie = ? 
        AND r.first_seen_at <= datetime(?) AND r.last_seen_at >= datetime(?);
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query,
		from, to, // For dashboard_count (created_at BETWEEN from AND to)
		to, from, // For total_alerts (first_seen_at <= to AND last_seen_at >= from)
		to, from, // For total_records (first_seen_at <= to AND last_seen_at >= from)
		from, to, // For total_dashboards (created_at BETWEEN from AND to)
		metricName, to, from) // Main WHERE (first_seen_at <= to AND last_seen_at >= from)
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
