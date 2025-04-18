package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
)

type ClickHouseProvider struct {
	mu sync.RWMutex
	db *sql.DB
}

const (
	createClickHouseTableStmt = `
		CREATE TABLE IF NOT EXISTS queries (
			TS DateTime,
			QueryParam String,
			TimeParam DateTime,
			Duration UInt64,
			StatusCode Int32,
			BodySize Int32,
			Fingerprint String,
			LabelMatchers Nested (
				key String,
				value String
			),
			Type String,
			Step Float64,
			Start DateTime,
			End DateTime,
			TotalQueryableSamples Int32,
			PeakSamples Int32
		) 
		ENGINE = MergeTree()
		ORDER BY TS;
	`

	createClickHouseRulesUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS RulesUsage (
			serie String,               -- TEXT equivalent in ClickHouse
			group_name String,          -- TEXT equivalent
			name String,                -- TEXT equivalent
			expression String,          -- TEXT equivalent
			kind String,                -- TEXT equivalent
			labels String,              -- Storing as a plain string or JSON (ClickHouse supports JSON functions)
			created_at DateTime         -- DATETIME equivalent
		) 
		ENGINE = MergeTree
		ORDER BY (serie, group_name, name);
	`

	createClickHouseDashboardUsageTableStmt = `
		CREATE TABLE IF NOT EXISTS DashboardUsage (
			id String,                  
			serie String,               
			name String,                
			url String,          
			created_at DateTime         
		) 
		ENGINE = MergeTree
		ORDER BY (id, serie, name);
	`
)

func RegisterClickHouseFlags(flagSet *flag.FlagSet) {
	flagSet.DurationVar(&config.DefaultConfig.Database.ClickHouse.DialTimeout, "clickhouse-dial-timeout", 5*time.Second, "Timeout to dial clickhouse.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Addr, "clickhouse-addr", "localhost:9000", "Address of the clickhouse server, comma separated for multiple servers.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Database, "clickhouse-database", "default", "Database for the clickhouse server, can also be set via CLICKHOUSE_DATABASE env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Username, "clickhouse-username", os.Getenv("CLICKHOUSE_USER"), "Username for the clickhouse server, can also be set via CLICKHOUSE_USER env var.")
	flagSet.StringVar(&config.DefaultConfig.Database.ClickHouse.Auth.Password, "clickhouse-password", os.Getenv("CLICKHOUSE_PASSWORD"), "Password for the clickhouse server, can also be set via CLICKHOUSE_PASSWORD env var.")
}

func newClickHouseProvider(ctx context.Context) (Provider, error) {
	config := config.DefaultConfig.Database.ClickHouse
	opts := &clickhouse.Options{
		Addr:        strings.Split(config.Addr, ","),
		DialTimeout: config.DialTimeout,
	}

	if config.Auth.Username != "" {
		opts.Auth = config.Auth
	}

	if config.Auth.Password != "" {
		opts.Auth.Password = config.Auth.Password
	}

	if config.Auth.Database != "" {
		opts.Auth.Database = config.Auth.Database
	}

	db := clickhouse.OpenDB(opts)
	if _, err := db.ExecContext(ctx, createClickHouseTableStmt); err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, createClickHouseRulesUsageTableStmt); err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, createClickHouseDashboardUsageTableStmt); err != nil {
		return nil, err
	}

	return &ClickHouseProvider{
		db: db,
	}, nil
}

func (c *ClickHouseProvider) WithDB(f func(db *sql.DB)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f(c.db)
}

func (c *ClickHouseProvider) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db.Close()
}

func (c *ClickHouseProvider) Insert(ctx context.Context, queries []Query) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	args := make([]interface{}, 0, len(queries)*15)

	for _, query := range queries {
		keys := make([]string, 0, len(query.LabelMatchers))
		values := make([]string, 0, len(query.LabelMatchers))
		for _, matcher := range query.LabelMatchers {
			for key, value := range matcher {
				keys = append(keys, key)
				values = append(values, value)
			}
		}

		args = append(args,
			query.TS,
			query.QueryParam,
			query.TimeParam,
			query.Duration.Milliseconds(), // Store Duration as milliseconds
			query.StatusCode,
			query.BodySize,
			query.Fingerprint,
			keys,
			values,
			query.Type,
			query.Step,
			query.Start,
			query.End,
			query.TotalQueryableSamples,
			query.PeakSamples,
		)
	}

	stmt := fmt.Sprintf("INSERT INTO queries VALUES %s", strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", len(queries)-1)+"(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	_, err := c.db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("unable to execute batch insert: %w", err)
	}

	return nil
}

func (c *ClickHouseProvider) Query(ctx context.Context, query string) (*QueryResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if err := ValidateSQLQuery(query); err != nil {
		return nil, fmt.Errorf("query not allowed: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("unable to query clickhouse: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("unable to get columns: %w", err)
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return &QueryResult{
		Columns: columns,
		Data:    data,
	}, nil
}

func (p *ClickHouseProvider) QueryShortCuts() []QueryShortCut {
	return commonQueryShortCuts
}

func (p *ClickHouseProvider) GetQueriesBySerieName(
	ctx context.Context,
	params QueriesBySerieNameParams) (*PagedResult, error) {

	// Set default values if not provided
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 10
	}
	if params.SortBy == "" {
		params.SortBy = "avgDuration"
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	if params.TimeRange.From.IsZero() {
		params.TimeRange.From = time.Now().Add(-30 * 24 * time.Hour) // Default to 30 days ago
	}
	if params.TimeRange.To.IsZero() {
		params.TimeRange.To = time.Now()
	}

	validSortFields := map[string]bool{
		"queryParam":      true,
		"avgDuration":     true,
		"avgPeakySamples": true,
		"maxPeakSamples":  true,
	}
	if !validSortFields[params.SortBy] {
		params.SortBy = "avgDuration"
	}

	// Build the query with filtering and sorting
	query := `
	WITH filtered_queries AS (
		SELECT
			QueryParam,
			AVG(Duration) AS avgDuration,
			AVG(PeakSamples) AS avgPeakySamples,
			MAX(PeakSamples) AS maxPeakSamples
		FROM queries
		WHERE 
			LabelMatchers.value[indexOf(LabelMatchers.key, '__name__')] = ?
			AND TS BETWEEN ? AND ?
			AND CASE 
				WHEN ? != '' THEN 
					position(QueryParam, ?) > 0
				ELSE 
					1=1
				END
		GROUP BY
			QueryParam
	),
	counted_queries AS (
		SELECT COUNT(*) as total_count 
		FROM filtered_queries
	)
	SELECT 
		q.*,
		cq.total_count
	FROM 
		filtered_queries q
	CROSS JOIN counted_queries cq
	ORDER BY
		CASE WHEN ? = 'asc' THEN
			CASE ?
				WHEN 'queryParam' THEN q.QueryParam
				WHEN 'avgDuration' THEN q.avgDuration
				WHEN 'avgPeakySamples' THEN q.avgPeakySamples
				WHEN 'maxPeakSamples' THEN q.maxPeakSamples
			END
		END ASC,
		CASE WHEN ? = 'desc' THEN
			CASE ?
				WHEN 'queryParam' THEN q.QueryParam
				WHEN 'avgDuration' THEN q.avgDuration
				WHEN 'avgPeakySamples' THEN q.avgPeakySamples
				WHEN 'maxPeakSamples' THEN q.maxPeakSamples
			END
		END DESC
	LIMIT ? OFFSET ?;
	`

	args := []interface{}{
		params.SerieName,
		params.TimeRange.From,
		params.TimeRange.To,
		params.Filter,
		params.Filter,
		params.SortOrder,
		params.SortBy,
		params.SortOrder,
		params.SortBy,
		params.PageSize,
		(params.Page - 1) * params.PageSize,
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
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
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	totalPages := int(math.Ceil(float64(totalCount) / float64(params.PageSize)))

	return &PagedResult{
		Total:      totalCount,
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

func (p *ClickHouseProvider) InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error {
	// If there are no rows, just return
	if len(rulesUsage) == 0 {
		return nil
	}

	// Each row has 7 columns: serie, group_name, name, expression, kind, labels, created_at.
	// We need 7 placeholders per row: (?,?,?, ?,?,?, ?)
	placeholdersPerRow := "(?, ?, ?, ?, ?, ?, ?)"

	// 1. Build the string of placeholders for all rows: (?,?,?, ?,?,?, ?), (?,?,?, ?,?,?, ?), ...
	var sb strings.Builder
	for i := 0; i < len(rulesUsage); i++ {
		sb.WriteString(placeholdersPerRow)
		if i < len(rulesUsage)-1 {
			sb.WriteString(",")
		}
	}
	// Example if len(rulesUsage) == 2:
	//   sb = "(?, ?, ?, ?, ?, ?, ?),(?, ?, ?, ?, ?, ?, ?)"

	// 2. Construct the full INSERT statement
	query := fmt.Sprintf(`
        INSERT INTO RulesUsage (
            serie, group_name, name, expression, kind, labels, created_at
        ) VALUES %s
    `, sb.String())

	// 3. Prepare a slice for all rows' parameters
	// 7 columns per row -> capacity = 7 * len(rulesUsage)
	args := make([]interface{}, 0, 7*len(rulesUsage))

	createdAt := time.Now()

	for _, rule := range rulesUsage {
		// Convert the labels map to JSON
		labelsJSON, err := json.Marshal(rule.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels: %w", err)
		}

		// Append each column's value in the same order as the placeholders
		args = append(args,
			rule.Serie,
			rule.GroupName,
			rule.Name,
			rule.Expression,
			rule.Kind,
			string(labelsJSON),
			createdAt,
		)
	}

	// 4. Execute the multi-row insert
	if _, err := p.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("unable to execute batch insert into RulesUsage: %w", err)
	}

	return nil
}

func (p *ClickHouseProvider) GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error) {
	// Set default values if not provided
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
		params.TimeRange.From = time.Now().Add(-30 * 24 * time.Hour) // Default to 30 days ago
	}
	if params.TimeRange.To.IsZero() {
		params.TimeRange.To = time.Now()
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

	// Query for total count
	countQuery := `
		SELECT COUNT(DISTINCT CONCAT(name, group_name))
		FROM RulesUsage
		WHERE serie = ? 
		AND kind = ?
		AND created_at BETWEEN ? AND ?
		AND CASE 
			WHEN ? != '' THEN 
				(name LIKE concat('%', ?, '%') OR expression LIKE concat('%', ?, '%'))
			ELSE 
				1=1
			END;
	`
	var totalCount int
	startTime := params.TimeRange.From
	endTime := params.TimeRange.To
	err := p.db.QueryRowContext(ctx, countQuery, params.Serie, params.Kind, startTime, endTime,
		params.Filter, params.Filter, params.Filter).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	// Calculate total pages
	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

	// Build the ORDER BY clause based on sort parameters
	var orderBy string
	if params.SortOrder == "asc" {
		switch params.SortBy {
		case "name":
			orderBy = "ORDER BY name ASC"
		case "group_name":
			orderBy = "ORDER BY group_name ASC"
		case "expression":
			orderBy = "ORDER BY expression ASC"
		case "created_at":
			orderBy = "ORDER BY created_at ASC"
		default:
			orderBy = "ORDER BY created_at ASC"
		}
	} else {
		switch params.SortBy {
		case "name":
			orderBy = "ORDER BY name DESC"
		case "group_name":
			orderBy = "ORDER BY group_name DESC"
		case "expression":
			orderBy = "ORDER BY expression DESC"
		case "created_at":
			orderBy = "ORDER BY created_at DESC"
		default:
			orderBy = "ORDER BY created_at DESC"
		}
	}

	// Query for paginated results
	query := fmt.Sprintf(`
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
					(name LIKE concat('%%', ?, '%%') OR expression LIKE concat('%%', ?, '%%'))
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
		%s
		LIMIT ? OFFSET ?;
	`, orderBy)

	// Calculate offset for pagination
	offset := (params.Page - 1) * params.PageSize

	args := []interface{}{
		params.Serie, params.Kind, startTime, endTime,
		params.Filter, params.Filter, params.Filter,
		params.PageSize, offset,
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

func (p *ClickHouseProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error {
	// Each row has 5 columns: (id, serie, name, url, created_at).
	// So we need 5 placeholders per row.

	// 1) Build the placeholder chunk for each row, i.e. "(?, ?, ?, ?, ?)"
	placeholdersPerRow := "(?, ?, ?, ?, ?)"

	// 2) Build a string with one "(?, ?, ?, ?, ?)" for each row, separated by commas
	var placeholdersBuilder strings.Builder
	for i := 0; i < len(dashboardUsage); i++ {
		placeholdersBuilder.WriteString(placeholdersPerRow)
		if i < len(dashboardUsage)-1 {
			placeholdersBuilder.WriteString(",")
		}
	}
	// e.g., if len(dashboardUsage) = 3,
	// placeholdersBuilder = "(?, ?, ?, ?, ?),(?, ?, ?, ?, ?),(?, ?, ?, ?, ?)"

	// 3) Build the entire INSERT statement using the placeholders string
	stmt := fmt.Sprintf(`
        INSERT INTO DashboardUsage (id, serie, name, url, created_at)
        VALUES %s
    `, placeholdersBuilder.String())

	// 4) Build a single slice of arguments for all rows
	// For each DashboardUsage: 5 columns -> append them in order
	args := make([]interface{}, 0, len(dashboardUsage)*5)

	createdAt := time.Now()
	for _, dash := range dashboardUsage {
		args = append(args,
			dash.Id,
			dash.Serie,
			dash.Name,
			dash.URL,
			createdAt, // or dash.CreatedAt if each row has a distinct timestamp
		)
	}

	// 5) Execute the statement once, passing in the arguments
	if _, err := p.db.ExecContext(ctx, stmt, args...); err != nil {
		return fmt.Errorf("unable to execute batch insert: %w", err)
	}

	return nil
}
func (p *ClickHouseProvider) GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (*PagedResult, error) {
	// Set default values if not provided
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
		params.TimeRange.From = time.Now().Add(-30 * 24 * time.Hour) // Default to 30 days ago
	}
	if params.TimeRange.To.IsZero() {
		params.TimeRange.To = time.Now()
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

	// Query for total count of distinct dashboards
	countQuery := `
		SELECT COUNT(DISTINCT CONCAT(id))
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
		params.Serie, from, to,
		params.Filter, params.Filter, params.Filter).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query total count: %w", err)
	}

	// Calculate total pages
	totalPages := (totalCount + params.PageSize - 1) / params.PageSize

	// Query for paginated results
	query := `
		WITH latest_dashboards AS (
			SELECT 
				id,
				serie,
				name,
				url,
				created_at,
				ROW_NUMBER() OVER (PARTITION BY serie, id ORDER BY created_at DESC) AS rank
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
		params.Serie, from, to,
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

func (p *ClickHouseProvider) QueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	query := `
		SELECT
			COUNT(*) AS total_queries,
			SUM(CASE WHEN type = 'instant' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS instant_percent,
			SUM(CASE WHEN type = 'range' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS range_percent
		FROM queries
		WHERE ts BETWEEN ? AND ?;
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

func (p *ClickHouseProvider) AverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
	query := `
		WITH current AS (
			SELECT avg(toFloat64(duration_ms)) AS avg_current
			FROM queries
			WHERE ts BETWEEN ? AND ?
		),
		previous AS (
			SELECT avg(toFloat64(duration_ms)) AS avg_previous 
			FROM queries
			WHERE ts BETWEEN ? AND ?
		)
		SELECT
			round(coalesce(avg_current, 0), 2),
			CASE 
				WHEN avg_previous IS NULL OR avg_previous = 0 THEN 0
				ELSE round(((avg_current - avg_previous) * 100.0 / avg_previous), 2)
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

func (p *ClickHouseProvider) GetQueryRate(ctx context.Context, tr TimeRange, metricName string) (*QueryRateResult, error) {
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
		WHERE ts BETWEEN ? AND ?;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, to)
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

func (p *ClickHouseProvider) GetQueryStatusDistribution(ctx context.Context, tr TimeRange) ([]QueryStatusDistributionResult, error) {
	return nil, nil
}

func (p *ClickHouseProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string) ([]QueryLatencyTrendsResult, error) {
	return nil, nil
}

func (p *ClickHouseProvider) GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error) {
	return nil, nil
}

func (p *ClickHouseProvider) GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error) {
	return nil, nil
}

func (p *ClickHouseProvider) GetRecentQueries(ctx context.Context, params RecentQueriesParams) (PagedResult, error) {
	return PagedResult{}, nil
}

func (p *ClickHouseProvider) GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error) {
	return MetricUsageStatics{}, nil
}

func (p *ClickHouseProvider) GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error) {
	return MetricQueryPerformanceStatistics{}, nil
}
