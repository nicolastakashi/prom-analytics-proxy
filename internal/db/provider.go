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

	// Add more checks if needed (e.g., length, specific sub-queries)
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

// TimeSeriesData represents a generic time series data point
type TimeSeriesData struct {
	Time  time.Time
	Value interface{}
}

// BucketAggregator defines how to aggregate data within a bucket
type BucketAggregator func([]TimeSeriesData) interface{}

// AggregateTimeSeries handles generic time series data aggregation
func AggregateTimeSeries(
	data []TimeSeriesData,
	from, to time.Time,
	bucketDuration time.Duration,
	aggregator BucketAggregator,
) []TimeSeriesData {
	// Create a map to store buckets
	buckets := make(map[time.Time][]TimeSeriesData)

	// Group data points into buckets
	for _, point := range data {
		bucketKey := point.Time.Truncate(bucketDuration)
		buckets[bucketKey] = append(buckets[bucketKey], point)
	}

	// Create result slice with all buckets in range
	var result []TimeSeriesData
	fromAligned := from.Truncate(bucketDuration)

	// Fill in all time buckets in range, including empty ones
	for t := fromAligned; !t.After(to); t = t.Add(bucketDuration) {
		bucketData := buckets[t]
		aggregatedValue := aggregator(bucketData)

		result = append(result, TimeSeriesData{
			Time:  t,
			Value: aggregatedValue,
		})
	}

	return result
}

// GetBucketDuration returns appropriate bucket duration based on time range
func GetBucketDuration(from, to time.Time) time.Duration {
	timeRange := to.Sub(from)

	switch {
	case timeRange <= 6*time.Hour:
		return 15 * time.Minute // 15m buckets for ranges up to 6 hours
	case timeRange <= 24*time.Hour:
		return 30 * time.Minute // 30m buckets for ranges up to 24 hours
	case timeRange <= 7*24*time.Hour:
		return time.Hour // 1h buckets for ranges up to 7 days
	default:
		return 24 * time.Hour // 1d buckets for ranges over 7 days
	}
}

// TimeRange represents a time period for queries
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
