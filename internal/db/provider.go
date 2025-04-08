package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Common time formats
const (
	SQLiteTimeFormat  = "2006-01-02 15:04:05" // For SQLite native format
	ISOTimeFormat     = time.RFC3339          // For standard ISO format
	ISOTimeFormatNano = time.RFC3339Nano      // For high precision timestamps
	DisplayTimeFormat = "2006-01-02 15:04"    // For user display
)

// Common errors
var (
	ErrNoResults   = errors.New("no results found")
	ErrInvalidScan = errors.New("invalid row scan")
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, queries []Query) error
	Query(ctx context.Context, query string) (*QueryResult, error)
	QueryShortCuts() []QueryShortCut
	QueryTypes(ctx context.Context, tr TimeRange) (*QueryTypesResult, error)
	AverageDuration(ctx context.Context, tr TimeRange) (*AverageDurationResult, error)
	QueryRate(ctx context.Context, tr TimeRange) (*QueryRateResult, error)
	GetQueriesBySerieName(ctx context.Context, serieName string, page int, pageSize int) (*PagedResult, error)
	GetQueryStatusDistribution(ctx context.Context, tr TimeRange) ([]QueryStatusDistributionResult, error)
	GetQueryLatencyTrends(ctx context.Context, tr TimeRange) ([]QueryLatencyTrendsResult, error)
	GetQueryThroughputAnalysis(ctx context.Context, tr TimeRange) ([]QueryThroughputAnalysisResult, error)
	GetQueryErrorAnalysis(ctx context.Context, tr TimeRange) ([]QueryErrorAnalysisResult, error)
	GetRecentQueries(ctx context.Context, params RecentQueriesParams) (PagedResult, error)
	InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error
	GetRulesUsage(ctx context.Context, serie string, kind string, page int, pageSize int) (*PagedResult, error)
	InsertDashboardUsage(ctx context.Context, dashboardUsage []DashboardUsage) error
	GetDashboardUsage(ctx context.Context, serieName string, page int, pageSize int) (*PagedResult, error)
	Close() error
}

func GetDbProvider(ctx context.Context, dbProvider DatabaseProvider) (Provider, error) {
	switch dbProvider {
	case ClickHouse:
		return newClickHouseProvider(ctx)
	case PostGreSQL:
		return newPostGreSQLProvider(ctx)
	case SQLite:
		return newSqliteProvider(ctx)
	default:
		return nil, fmt.Errorf("invalid database type %q, only 'clickhouse' and 'postgresql' are supported", dbProvider)
	}
}

var deniedKeywords = []string{"DROP", "DELETE", "UPDATE", "INSERT", "ALTER", "TRUNCATE", "EXEC", "--", ";"}

func containsDeniedKeyword(query string) bool {
	upperQuery := strings.ToUpper(query) // Normalize to upper case for comparison
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
		return fmt.Errorf("query contains disallowed keyword")
	}

	if containsDeniedPattern(query) {
		return fmt.Errorf("query contains dangerous pattern")
	}

	return nil
}

var commonQueryShortCuts = []QueryShortCut{
	{
		Title: "Top 10 Longest Queries by Duration",
		Query: `SELECT fingerprint, queryParam, duration, ts FROM queries GROUP BY fingerprint ORDER BY duration DESC LIMIT 10`,
	},
	{
		Title: "Top 10 Queries with Highest Peak Samples",
		Query: `SELECT fingerprint, queryParam, peakSamples, ts FROM queries GROUP BY fingerprint ORDER BY peakSamples DESC LIMIT 10`,
	},
	{
		Title: "Top 10 Queries with Highest Queryable Samples",
		Query: `SELECT fingerprint, queryParam, totalQueryableSamples, ts FROM queries GROUP BY fingerprint ORDER BY totalQueryableSamples DESC LIMIT 10`,
	},
	{
		Title: "Top 10 Average and Maximum Duration per Query",
		Query: `SELECT fingerprint, queryParam, AVG(duration) AS avgDuration, MAX(duration) AS maxDuration FROM queries GROUP BY fingerprint, queryParam ORDER BY avgDuration DESC`,
	},
	{
		Title: "Top 10 Most Frequent Queries",
		Query: `SELECT fingerprint, queryParam, COUNT(fingerprint) AS count FROM queries GROUP BY fingerprint, queryParam ORDER BY count DESC LIMIT 10`,
	},
}

type RecentQueriesParams struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	Filter    string
	TimeRange TimeRange
}

type SeriesMetadataParams struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
	Filter    string
	Type      string
}

type TimeRange struct {
	From time.Time
	To   time.Time
}

// Format formats the time range according to the given format
func (tr TimeRange) Format(layout string) (string, string) {
	return tr.From.UTC().Format(layout), tr.To.UTC().Format(layout)
}

// Previous returns the previous time range of the same duration
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

// Add RecentQueriesResult type
type RecentQueriesResult struct {
	QueryParam string    `json:"queryParam"`
	Duration   int64     `json:"duration"`
	Samples    int64     `json:"samples"`
	Status     int       `json:"status"`
	Timestamp  time.Time `json:"timestamp"`
}
