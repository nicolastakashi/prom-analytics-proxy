package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
)

// Common time formats
const (
	SQLiteTimeFormat  = "2006-01-02 15:04:05" // For SQLite native format
	ISOTimeFormat     = time.RFC3339          // For standard ISO format
	ISOTimeFormatNano = time.RFC3339Nano      // For high precision timestamps
	DisplayTimeFormat = "2006-01-02 15:04"    // For user display
)

// Time constants
const (
	FifteenMinutes = 15 * time.Minute
	ThirtyMinutes  = 30 * time.Minute
	OneHour        = time.Hour
	OneDay         = 24 * time.Hour
	ThirtyDays     = 30 * 24 * time.Hour
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, queries []Query) error
	InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error
	InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error
	GetSeriesMetadata(ctx context.Context, params SeriesMetadataParams) (*PagedResult, error)
	UpsertMetricsCatalog(ctx context.Context, items []MetricCatalogItem) error
	RefreshMetricsUsageSummary(ctx context.Context, tr TimeRange) error
	UpsertMetricsJobIndex(ctx context.Context, items []MetricJobIndexItem) error
	ListJobs(ctx context.Context) ([]string, error)

	GetQueryTypes(ctx context.Context, tr TimeRange, fingerprint string) (*QueryTypesResult, error)
	GetAverageDuration(ctx context.Context, tr TimeRange, fingerprint string) (*AverageDurationResult, error)
	GetQueryRate(ctx context.Context, tr TimeRange, metricName string, fingerprint string) (*QueryRateResult, error)
	GetQueriesBySerieName(ctx context.Context, params QueriesBySerieNameParams) (*PagedResult, error)
	GetQueryStatusDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryStatusDistributionResult, error)
	GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string, fingerprint string) ([]QueryLatencyTrendsResult, error)
	GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error)
	GetQueryErrorAnalysis(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryErrorAnalysisResult, error)
	GetQueryTimeRangeDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryTimeRangeDistributionResult, error)
	GetQueryExpressions(ctx context.Context, params QueryExpressionsParams) (PagedResult, error)
	// GetQueryExecutions returns paginated raw query executions filtered by fingerprint/time range
	GetQueryExecutions(ctx context.Context, params QueryExecutionsParams) (PagedResult, error)
	GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error)
	GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error)
	GetRulesUsage(ctx context.Context, params RulesUsageParams) (*PagedResult, error)
	GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (*PagedResult, error)
	GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error)

	Close() error
}

// MaxPageSize limits page sizes to avoid unbounded memory allocations
const MaxPageSize = 100

// ValidSeriesMetadataSortFields centralizes sortable fields for series metadata
// Note: These should match the actual column names used in the SQL query
var ValidSeriesMetadataSortFields = map[string]bool{
	"name": true,
	"type": true,
	"help": true,
	"unit": true,
}

// ValidSortDirections centralizes valid sort directions
var ValidSortDirections = map[string]bool{
	"asc":  true,
	"desc": true,
}

func GetDbProvider(ctx context.Context, dbProvider DatabaseProvider) (Provider, error) {
	switch dbProvider {
	case PostGreSQL:
		return newPostGreSQLProvider(ctx)
	case SQLite:
		return newSqliteProvider(ctx)
	default:
		return nil, ValidationError("database type", fmt.Sprintf("invalid type %q, only 'postgresql' and 'sqlite' are supported", dbProvider))
	}
}

var deniedKeywords = []string{"DROP", "DELETE", "UPDATE", "INSERT", "ALTER", "TRUNCATE", "EXEC", "--", ";"}

func containsDeniedKeyword(query string) bool {
	upperQuery := strings.ToUpper(query)
	for _, keyword := range deniedKeywords {
		if strings.Contains(upperQuery, keyword) {
			return true
		}
	}
	return false
}

var deniedPatterns = []string{"--", ";"}

func containsDeniedPattern(query string) bool {
	for _, pattern := range deniedPatterns {
		if strings.Contains(query, pattern) {
			return true
		}
	}
	return false
}

func ValidateSQLQuery(query string) error {
	if containsDeniedKeyword(query) {
		return ValidationError("SQL", "query contains disallowed keyword")
	}

	if containsDeniedPattern(query) {
		return ValidationError("SQL", "query contains dangerous pattern")
	}

	return nil
}

func SetDefaultTimeRange(tr *TimeRange) {
	if tr.From.IsZero() {
		tr.From = time.Now().UTC().Add(-ThirtyDays)
	}
	if tr.To.IsZero() {
		tr.To = time.Now().UTC()
	}
}

func ValidateSortField(sortBy *string, sortOrder *string, validSortFields map[string]bool, defaultSort string) {
	// Set default values if empty
	if *sortBy == "" {
		*sortBy = defaultSort
	}
	if *sortOrder == "" {
		*sortOrder = "desc"
	}

	// Validate sort order against whitelist to prevent SQL injection
	normalizedOrder := strings.ToLower(strings.TrimSpace(*sortOrder))
	if !ValidSortDirections[normalizedOrder] {
		*sortOrder = "desc" // Safe default
	} else {
		*sortOrder = normalizedOrder
	}

	// Validate sort field against whitelist to prevent SQL injection
	normalizedSortBy := strings.TrimSpace(*sortBy)
	if !validSortFields[normalizedSortBy] {
		*sortBy = defaultSort // Safe default
	} else {
		*sortBy = normalizedSortBy
	}
}

func ValidatePagination(page *int, pageSize *int, defaultPageSize int) {
	if *page <= 0 {
		*page = 1
	}
	if *pageSize <= 0 {
		*pageSize = defaultPageSize
	}
	if *pageSize > MaxPageSize {
		*pageSize = MaxPageSize
	}
}

// BuildSafeOrderByClause constructs a safe ORDER BY clause using validated parameters
// tableAlias should be the table alias (e.g., "c") or empty string if not needed
func BuildSafeOrderByClause(sortBy, sortOrder, tableAlias string, validSortFields map[string]bool, defaultSort string) string {
	// Validate parameters using existing validation function
	ValidateSortField(&sortBy, &sortOrder, validSortFields, defaultSort)

	// Build the ORDER BY clause safely
	var orderByClause string
	if tableAlias != "" {
		orderByClause = fmt.Sprintf(" ORDER BY %s.%s %s NULLS LAST", tableAlias, sortBy, strings.ToUpper(sortOrder))
	} else {
		orderByClause = fmt.Sprintf(" ORDER BY %s %s NULLS LAST", sortBy, strings.ToUpper(sortOrder))
	}

	return orderByClause
}

// BuildSafeQueryWithOrderBy constructs a complete query with validated ORDER BY clause
// This function minimizes string concatenation for better static analysis compatibility
func BuildSafeQueryWithOrderBy(baseQuery, tableAlias, limitClause string, sortBy, sortOrder string, validSortFields map[string]bool, defaultSort string) string {
	// Validate parameters first
	ValidateSortField(&sortBy, &sortOrder, validSortFields, defaultSort)

	// Build complete query with validated components
	var completeQuery string
	if tableAlias != "" {
		completeQuery = fmt.Sprintf("%s ORDER BY %s.%s %s NULLS LAST%s",
			baseQuery, tableAlias, sortBy, strings.ToUpper(sortOrder), limitClause)
	} else {
		completeQuery = fmt.Sprintf("%s ORDER BY %s %s NULLS LAST%s",
			baseQuery, sortBy, strings.ToUpper(sortOrder), limitClause)
	}

	return completeQuery
}

func CalculateTotalPages(totalCount, pageSize int) int {
	return int(math.Ceil(float64(totalCount) / float64(pageSize)))
}

func ProcessRows(rows *sql.Rows, scanFunc func(*sql.Rows) error) error {
	defer CloseResource(rows)

	for rows.Next() {
		if err := scanFunc(rows); err != nil {
			return ErrorWithOperation(err, "scanning row")
		}
	}

	if err := rows.Err(); err != nil {
		return ErrorWithOperation(err, "row iteration")
	}

	return nil
}

// QueryExpressionsParams defines parameters for aggregated query expressions grouped by fingerprint
type QueryExpressionsParams struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	Filter    string
	TimeRange TimeRange
}

type QueriesBySerieNameParams struct {
	SerieName string
	TimeRange TimeRange
	Page      int
	PageSize  int
	Filter    string
	SortBy    string
	SortOrder string
}

// QueryExecutionsParams defines parameters to list raw query executions for a fingerprint
type QueryExecutionsParams struct {
	Fingerprint string
	TimeRange   TimeRange
	Page        int
	PageSize    int
	SortBy      string
	SortOrder   string
	Type        string // all|instant|range
}

// QueryExecutionRow represents a single query execution row returned from the DB
type QueryExecutionRow struct {
	Timestamp time.Time         `json:"timestamp"`
	Status    int               `json:"status"`
	Duration  int64             `json:"duration"`
	Samples   int               `json:"samples"`
	Type      string            `json:"type"`
	Steps     float64           `json:"steps"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type SeriesMetadataParams struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	Filter    string
	Type      string
	Unused    bool
	Job       string
}

type MetricCatalogItem struct {
	Name string
	Type string
	Help string
	Unit string
}

type MetricJobIndexItem struct {
	Name string
	Job  string
}

type TimeRange struct {
	From time.Time
	To   time.Time
}

func (tr TimeRange) Format(layout string) (string, string) {
	return tr.From.UTC().Format(layout), tr.To.UTC().Format(layout)
}

func (tr TimeRange) Previous() TimeRange {
	duration := tr.To.Sub(tr.From)
	return TimeRange{
		From: tr.From.Add(-duration),
		To:   tr.To.Add(-duration),
	}
}

type ListQueriesResult struct {
	Fingerprint string    `json:"fingerprint"`
	Duration    int64     `json:"duration"`
	Samples     int64     `json:"samples"`
	Status      int       `json:"status"`
	Timestamp   time.Time `json:"timestamp"`
}

type RecentQueriesResult struct {
	QueryParam string    `json:"queryParam"`
	Duration   int64     `json:"duration"`
	Samples    int64     `json:"samples"`
	Status     int       `json:"status"`
	Timestamp  time.Time `json:"timestamp"`
}
