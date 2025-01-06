package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Provider interface {
	WithDB(func(db *sql.DB))
	Insert(ctx context.Context, queries []Query) error
	Query(ctx context.Context, query string) (*QueryResult, error)
	QueryShortCuts() []QueryShortCut
	GetQueriesBySerieName(ctx context.Context, serieName string, page int, pageSize int) (*PagedResult, error)
	InsertRulesUsage(ctx context.Context, rulesUsage []RulesUsage) error
	GetRulesUsage(ctx context.Context, serie string, kind string, page int, pageSize int) (*PagedResult, error)
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
