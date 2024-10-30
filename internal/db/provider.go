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
		Title:       "Top 10 Longest Queries by Duration",
		Description: "Helps to identy the longest-running queries in terms of execution time, helping pinpoint potential performance bottlenecks in the system.",
		Query:       `SELECT queryParam, duration, ts FROM queries ORDER BY duration DESC LIMIT 10`,
	},
	{
		Title:       "Queries with Highest Peak Samples",
		Description: `Helps to identify queries that have the highest peak samples, which can be used to identify queries that are causing high memory usage in the system.`,
		Query:       `SELECT queryParam, peakSamples, ts FROM queries ORDER BY peakSamples DESC LIMIT 10`,
	},
	{
		Title:       "Queries with Highest Total Queryable Samples",
		Description: `Helps to identify queries that have the highest total queryable samples, which can be used to identify queries that are causing high memory usage in the system.`,
		Query:       `SELECT queryParam, totalQueryableSamples, ts FROM queries ORDER BY totalQueryableSamples DESC LIMIT 10`,
	},
}
