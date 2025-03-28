package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"sort"
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
	FifteenMinutes = 15 * time.Minute
	ThirtyMinutes  = 30 * time.Minute
	OneHour        = time.Hour
	OneDay         = 24 * time.Hour
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

	if _, err := db.ExecContext(ctx, createSqliteDashboardUsageTableStmt); err != nil {
		return nil, fmt.Errorf("failed to create dashboard usage table: %w", err)
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

func (p *SQLiteProvider) QueryShortCuts() []QueryShortCut {
	return commonQueryShortCuts
}

func (p *SQLiteProvider) GetQueriesBySerieName(ctx context.Context, serieName string, page int, pageSize int) (*PagedResult, error) {
	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // 30 days ago

	tr := TimeRange{
		From: startTime,
		To:   endTime,
	}
	startTimeFormatted, endTimeFormatted := tr.Format(SQLiteTimeFormat)

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

func (p *SQLiteProvider) InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error {
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

	createdAt := time.Now()

	// Prepare the SQL statement for insertion
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO DashboardUsage (
			id, serie, name, url, created_at
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

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

func (p *SQLiteProvider) GetDashboardUsage(ctx context.Context, serie string, page, pageSize int) (*PagedResult, error) {
	offset := (page - 1) * pageSize

	// Query for total count
	countQuery := `
		SELECT COUNT(DISTINCT name)
		FROM DashboardUsage
		WHERE serie = ? 
		AND created_at >= datetime('now', '-30 days');
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
			FROM DashboardUsage
			WHERE serie = ? AND created_at >= datetime('now', '-30 days')
		)
		SELECT 
			id,
			serie,
			name,
			url,
			created_at
		FROM latest_rules
		WHERE rank = 1
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?;
	`

	rows, err := p.db.QueryContext(ctx, query, serie, pageSize, offset)
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
		Total:      len(results),
		TotalPages: totalPages,
		Data:       results,
	}, nil
}

// QueryTypes returns the total number of queries, the percentage of instant queries, and the percentage of range queries.
func (p *SQLiteProvider) QueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error) {
	query := `
	SELECT
		COUNT(*) AS total_queries,
		SUM(CASE WHEN type = 'instant' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS instant_percent,
		SUM(CASE WHEN type = 'range' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS range_percent
	FROM queries
	WHERE ts BETWEEN datetime(?) AND datetime(?)
	ORDER BY ts;
	`

	fromStr, toStr := tr.Format(ISOTimeFormatNano)
	rows, err := p.db.QueryContext(ctx, query, fromStr, toStr)
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

func (p *SQLiteProvider) AverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error) {
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
	defer rows.Close()

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

func (p *SQLiteProvider) QueryRate(ctx context.Context, tr TimeRange) (*QueryRateResult, error) {
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
		WHERE ts BETWEEN datetime(?) AND datetime(?);
	`

	from, to := tr.Format(ISOTimeFormatNano)
	rows, err := p.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query query rate: %w", err)
	}
	defer rows.Close()

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
	query := `
		SELECT 
			ts,
			statusCode
		FROM queries
		WHERE ts BETWEEN datetime(?) AND datetime(?)
		ORDER BY ts;
	`

	from, to := tr.Format(ISOTimeFormat)
	rows, err := p.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query status distribution: %w", err)
	}
	defer rows.Close()

	var rawData []TimeSeriesData
	for rows.Next() {
		var tsStr string
		var statusCode int
		if err := rows.Scan(&tsStr, &statusCode); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp: %w", err)
		}
		rawData = append(rawData, TimeSeriesData{
			Time:  ts,
			Value: statusCode,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	aggregator := func(data []TimeSeriesData) interface{} {
		counts := struct {
			status2xx int
			status4xx int
			status5xx int
		}{}

		for _, d := range data {
			statusCode := d.Value.(int)
			switch {
			case statusCode >= 200 && statusCode < 300:
				counts.status2xx++
			case statusCode >= 400 && statusCode < 500:
				counts.status4xx++
			case statusCode >= 500 && statusCode < 600:
				counts.status5xx++
			}
		}
		return counts
	}

	aggregated := AggregateTimeSeries(rawData, tr.From, tr.To, GetBucketDuration(tr.From, tr.To), aggregator)

	var results []QueryStatusDistributionResult
	for _, point := range aggregated {
		counts := point.Value.(struct {
			status2xx int
			status4xx int
			status5xx int
		})
		results = append(results, QueryStatusDistributionResult{
			Time:      point.Time.Format(DisplayTimeFormat),
			Status2xx: counts.status2xx,
			Status4xx: counts.status4xx,
			Status5xx: counts.status5xx,
		})
	}

	return results, nil
}

func (p *SQLiteProvider) GetQueryLatencyTrends(ctx context.Context, tr TimeRange) ([]QueryLatencyTrendsResult, error) {
	query := `
		SELECT ts, duration
		FROM queries
		WHERE ts BETWEEN datetime(?) AND datetime(?)
		ORDER BY ts;
	`
	from, to := tr.Format(ISOTimeFormat)

	rows, err := p.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query query latency trends: %w", err)
	}
	defer rows.Close()

	var rawData []TimeSeriesData
	for rows.Next() {
		var tsStr string
		var duration int
		if err := rows.Scan(&tsStr, &duration); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp: %w", err)
		}
		rawData = append(rawData, TimeSeriesData{Time: ts, Value: duration})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	aggregator := func(data []TimeSeriesData) interface{} {
		if len(data) == 0 {
			return struct {
				avg float64
				p95 int
			}{0, 0}
		}

		var sum int
		durations := make([]int, len(data))
		for i, d := range data {
			duration := d.Value.(int)
			sum += duration
			durations[i] = duration
		}
		avg := float64(sum) / float64(len(data))

		sort.Ints(durations)
		p95Index := int(0.95 * float64(len(durations)))
		if p95Index >= len(durations) {
			p95Index = len(durations) - 1
		}

		return struct {
			avg float64
			p95 int
		}{avg, durations[p95Index]}
	}

	aggregated := AggregateTimeSeries(rawData, tr.From, tr.To, GetBucketDuration(tr.From, tr.To), aggregator)

	var results []QueryLatencyTrendsResult
	for _, point := range aggregated {
		stats := point.Value.(struct {
			avg float64
			p95 int
		})
		results = append(results, QueryLatencyTrendsResult{
			Time:  point.Time.Format(DisplayTimeFormat),
			Value: stats.avg,
			P95:   stats.p95,
		})
	}

	return results, nil
}

func (p *SQLiteProvider) GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error) {
	query := `
		SELECT ts, COUNT(*) as count
		FROM queries
		WHERE ts BETWEEN datetime(?) AND datetime(?)
		GROUP BY ts
		ORDER BY ts;
	`
	from, to := tr.Format(ISOTimeFormat)

	rows, err := p.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query query throughput analysis: %w", err)
	}
	defer rows.Close()

	var rawData []TimeSeriesData
	for rows.Next() {
		var tsStr string
		var count int
		if err := rows.Scan(&tsStr, &count); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp: %w", err)
		}
		rawData = append(rawData, TimeSeriesData{Time: ts, Value: count})
	}

	minutes := GetBucketDuration(tr.From, tr.To).Minutes()
	aggregator := func(data []TimeSeriesData) interface{} {
		var total int
		for _, d := range data {
			total += d.Value.(int)
		}
		return float64(total) / minutes
	}

	aggregated := AggregateTimeSeries(rawData, tr.From, tr.To, GetBucketDuration(tr.From, tr.To), aggregator)

	// Format results
	var results []QueryThroughputAnalysisResult
	for _, point := range aggregated {
		results = append(results, QueryThroughputAnalysisResult{
			Time:  point.Time.Format(DisplayTimeFormat),
			Value: point.Value.(float64),
		})
	}

	return results, nil
}
