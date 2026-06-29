package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
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

// SQL ORDER BY direction literals. resolveSafeSortOrder narrows the
// user-supplied sortOrder to one of these two constants so the ORDER BY
// direction in generated SQL is never user-controlled.
const (
	sortOrderASC  = "ASC"
	sortOrderDESC = "DESC"
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, queries []Query) error
	InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error
	InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error
	GetSeriesMetadata(ctx context.Context, params SeriesMetadataParams) (PagedResult, error)
	UpsertMetricsCatalog(ctx context.Context, items []MetricCatalogItem) error
	RefreshMetricsUsageSummary(ctx context.Context, tr TimeRange) error
	UpsertMetricsJobIndex(ctx context.Context, items []MetricJobIndexItem) error
	ListJobs(ctx context.Context) ([]string, error)

	GetQueryTypes(ctx context.Context, tr TimeRange, fingerprint string) (*QueryTypesResult, error)
	GetAverageDuration(ctx context.Context, tr TimeRange, fingerprint string) (*AverageDurationResult, error)
	GetQueryRate(ctx context.Context, tr TimeRange, metricName string, fingerprint string) (*QueryRateResult, error)
	GetQueriesBySerieName(ctx context.Context, params QueriesBySerieNameParams) (PagedResult, error)
	GetQueryStatusDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryStatusDistributionResult, error)
	GetQueryLatencyTrends(ctx context.Context, tr TimeRange, metricName string, fingerprint string) ([]QueryLatencyTrendsResult, error)
	GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error)
	GetQueryErrorAnalysis(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryErrorAnalysisResult, error)
	GetQueryTimeRangeDistribution(ctx context.Context, tr TimeRange, fingerprint string) ([]QueryTimeRangeDistributionResult, error)
	GetQueryExpressions(ctx context.Context, params QueryExpressionsParams) (PagedResult, error)
	GetQueryExecutions(ctx context.Context, params QueryExecutionsParams) (PagedResult, error)
	GetMetricStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricUsageStatics, error)
	GetMetricQueryPerformanceStatistics(ctx context.Context, metricName string, tr TimeRange) (MetricQueryPerformanceStatistics, error)
	GetRulesUsage(ctx context.Context, params RulesUsageParams) (PagedResult, error)
	GetDashboardUsage(ctx context.Context, params DashboardUsageParams) (PagedResult, error)
	GetSeriesMetadataByNames(ctx context.Context, names []string, job string) ([]models.MetricMetadata, error)
	DeleteQueriesBefore(ctx context.Context, cutoff time.Time) (int64, error)

	GetSeriesMetadataCount(ctx context.Context, params SeriesMetadataParams) (int, error)
	GetQueryExpressionsCount(ctx context.Context, params QueryExpressionsParams) (int, error)
	GetQueryExecutionsCount(ctx context.Context, params QueryExecutionsParams) (int, error)
	GetQueriesBySerieNameCount(ctx context.Context, params QueriesBySerieNameParams) (int, error)
	GetRulesUsageCount(ctx context.Context, params RulesUsageParams) (int, error)
	GetDashboardUsageCount(ctx context.Context, params DashboardUsageParams) (int, error)

	Close() error
}

// MaxPageSize limits page sizes to avoid unbounded memory allocations
const MaxPageSize = 100

// MaxSeriesMetadataPageSize is the upper bound for the programmatic seriesMetadata
// sweep endpoint; sweet spot is 1k–5k per the bench in routes_test.go.
const MaxSeriesMetadataPageSize = 10000

// ValidSeriesMetadataSortFields centralizes sortable fields for series metadata
// Note: These should match the actual column names used in the SQL query
var ValidSeriesMetadataSortFields = map[string]bool{
	"name":           true,
	"type":           true,
	"help":           true,
	"unit":           true,
	"alertCount":     true,
	"recordCount":    true,
	"dashboardCount": true,
	"queryCount":     true,
}

// SeriesMetadataSortAliases maps sort fields to their SQL expression with the correct table alias.
// Fields from metrics_catalog use alias "c", fields from metrics_usage_summary use alias "s" with COALESCE.
var SeriesMetadataSortAliases = map[string]string{
	"name":           "c.name",
	"type":           "c.type",
	"help":           "c.help",
	"unit":           "c.unit",
	"alertCount":     "COALESCE(s.alert_count, 0)",
	"recordCount":    "COALESCE(s.record_count, 0)",
	"dashboardCount": "COALESCE(s.dashboard_count, 0)",
	"queryCount":     "COALESCE(s.query_count, 0)",
}

const (
	SeriesMetadataUsageAll    = "all"
	SeriesMetadataUsageUsed   = "used"
	SeriesMetadataUsageUnused = "unused"
)

var ValidSeriesMetadataUsageFilters = map[string]bool{
	SeriesMetadataUsageAll:    true,
	SeriesMetadataUsageUsed:   true,
	SeriesMetadataUsageUnused: true,
}

// ValidSortDirections centralizes valid sort directions
var ValidSortDirections = map[string]bool{
	"asc":  true,
	"desc": true,
}

func GetDbProvider(ctx context.Context, dbProvider DatabaseProvider) (Provider, error) {
	switch dbProvider {
	case PostGreSQL:
		return NewPostgreSQLProvider(ctx, config.DefaultConfig.Database.PostgreSQL)
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

// resolveSafeSortExpr returns a SQL fragment for the ORDER BY column. The
// returned string is always either a value from the static sortAliases map
// or a hardcoded literal — it never embeds the caller's sortBy string.
// ValidateSortField has already constrained sortBy to a key from
// validSortFields; this layer rebuilds the SQL fragment from that key as a
// map lookup so static analysers can see that user input never reaches the
// SQL text by string interpolation. The defaultSort fallback assumes
// callers keep validSortFields, sortAliases, and defaultSort consistent
// (i.e. defaultSort is a key in sortAliases when sortAliases is provided);
// the final "1" guarantees we always emit a valid ORDER BY expression
// even if those invariants are violated.
func resolveSafeSortExpr(sortBy, tableAlias, defaultSort string, sortAliases ...map[string]string) string {
	if len(sortAliases) > 0 && sortAliases[0] != nil {
		if expr, ok := sortAliases[0][sortBy]; ok {
			return expr
		}
		if expr, ok := sortAliases[0][defaultSort]; ok {
			return expr
		}
	}
	if tableAlias != "" && defaultSort != "" {
		return tableAlias + "." + defaultSort
	}
	if defaultSort != "" {
		return defaultSort
	}
	return "1"
}

// resolveSafeSortOrder collapses sortOrder to one of two literal constants
// ("ASC" or "DESC"). ValidateSortField has already narrowed sortOrder to
// "asc" or "desc"; this function rewrites it into a literal so static
// analysers can see that the ORDER BY direction is not user-controlled
// even though the input came from an HTTP query parameter.
func resolveSafeSortOrder(sortOrder string) string {
	if strings.EqualFold(strings.TrimSpace(sortOrder), "asc") {
		return sortOrderASC
	}
	return sortOrderDESC
}

// BuildSafeOrderByClause constructs a safe ORDER BY clause using validated parameters
// tableAlias should be the table alias (e.g., "c") or empty string if not needed
// sortAliases optionally maps field names to their full SQL expression (e.g., "alertCount" -> "COALESCE(s.alert_count, 0)")
func BuildSafeOrderByClause(sortBy, sortOrder, tableAlias string, validSortFields map[string]bool, defaultSort string, sortAliases ...map[string]string) string {
	ValidateSortField(&sortBy, &sortOrder, validSortFields, defaultSort)
	return fmt.Sprintf(" ORDER BY %s %s NULLS LAST",
		resolveSafeSortExpr(sortBy, tableAlias, defaultSort, sortAliases...),
		resolveSafeSortOrder(sortOrder))
}

// BuildSafeQueryWithOrderBy constructs a complete query with validated ORDER BY clause
// This function minimizes string concatenation for better static analysis compatibility
// sortAliases optionally maps field names to their full SQL expression for mixed-table sorting
func BuildSafeQueryWithOrderBy(baseQuery, tableAlias, limitClause string, sortBy, sortOrder string, validSortFields map[string]bool, defaultSort string, sortAliases ...map[string]string) string {
	ValidateSortField(&sortBy, &sortOrder, validSortFields, defaultSort)
	return fmt.Sprintf("%s ORDER BY %s %s NULLS LAST%s",
		baseQuery,
		resolveSafeSortExpr(sortBy, tableAlias, defaultSort, sortAliases...),
		resolveSafeSortOrder(sortOrder),
		limitClause)
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
	Timestamp   time.Time         `json:"timestamp"`
	Status      int               `json:"status"`
	Duration    int64             `json:"duration"`
	Samples     int               `json:"samples"`
	Type        string            `json:"type"`
	Steps       float64           `json:"steps"`
	HTTPHeaders map[string]string `json:"httpHeaders,omitempty"`
	Start       time.Time         `json:"start"`
	End         time.Time         `json:"end,omitempty"`
}

type SeriesMetadataParams struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	Filter    string
	Type      string
	Usage     string
	Job       string
}

func NormalizeSeriesMetadataUsage(usage string) string {
	normalized := strings.ToLower(strings.TrimSpace(usage))
	if ValidSeriesMetadataUsageFilters[normalized] {
		return normalized
	}
	return SeriesMetadataUsageAll
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
