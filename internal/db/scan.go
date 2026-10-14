package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
)

// Row scanners shared by both providers. Each maps one result row, with its
// columns in the order both providers' queries select them.

// scanQueriesBySerieName scans a GetQueriesBySerieName row, storing the
// listing's total row count in total.
func scanQueriesBySerieName(total *int) func(*sql.Rows) (QueriesBySerieNameResult, error) {
	return func(row *sql.Rows) (QueriesBySerieNameResult, error) {
		var r QueriesBySerieNameResult
		err := row.Scan(&r.Query, &r.AvgDuration, &r.AvgPeakySamples, &r.MaxPeakSamples, total)
		return r, err
	}
}

// scanQueryExpression scans a GetQueryExpressions row, storing the listing's
// total row count in total.
func scanQueryExpression(total *int) func(*sql.Rows) (QueryExpression, error) {
	return func(row *sql.Rows) (QueryExpression, error) {
		var r QueryExpression
		err := row.Scan(&r.Fingerprint, &r.Query, &r.Executions, &r.AvgDuration, &r.ErrorRatePercent, &r.PeakSamples, total)
		return r, err
	}
}

// scanQueryExecution scans a GetQueryExecutions row, storing the listing's
// total row count in total. Headers stored as NULL read back as none.
func scanQueryExecution(total *int) func(*sql.Rows) (QueryExecutionRow, error) {
	return func(row *sql.Rows) (QueryExecutionRow, error) {
		var r QueryExecutionRow
		var headers []byte
		if err := row.Scan(&r.Timestamp, &r.Status, &r.Duration, &r.Samples, &r.Type, &r.Steps, &headers, &r.Start, &r.End, total); err != nil {
			return r, err
		}
		if len(headers) > 0 {
			if err := json.Unmarshal(headers, &r.HTTPHeaders); err != nil {
				return r, fmt.Errorf("unmarshal httpHeaders: %w", err)
			}
		}
		return r, nil
	}
}

func scanRulesUsage(row *sql.Rows) (RulesUsage, error) {
	var r RulesUsage
	var labels []byte
	if err := row.Scan(&r.Serie, &r.GroupName, &r.Name, &r.Expression, &r.Kind, &labels, &r.CreatedAt); err != nil {
		return r, err
	}
	if len(labels) > 0 {
		if err := json.Unmarshal(labels, &r.Labels); err != nil {
			return r, fmt.Errorf("unmarshal labels: %w", err)
		}
	}
	return r, nil
}

func scanDashboardUsage(row *sql.Rows) (DashboardUsage, error) {
	var d DashboardUsage
	err := row.Scan(&d.Id, &d.Serie, &d.Name, &d.URL, &d.CreatedAt)
	return d, err
}

func scanJob(row *sql.Rows) (string, error) {
	var job string
	err := row.Scan(&job)
	return job, err
}

func scanQueryStatusDistribution(row *sql.Rows) (QueryStatusDistributionResult, error) {
	var r QueryStatusDistributionResult
	err := row.Scan((*bucketTime)(&r.Time), &r.Status2xx, &r.Status4xx, &r.Status5xx)
	return r, err
}

func scanQueryThroughputAnalysis(row *sql.Rows) (QueryThroughputAnalysisResult, error) {
	var r QueryThroughputAnalysisResult
	err := row.Scan((*bucketTime)(&r.Time), &r.Value)
	return r, err
}

func scanQueryErrorAnalysis(row *sql.Rows) (QueryErrorAnalysisResult, error) {
	var r QueryErrorAnalysisResult
	err := row.Scan((*bucketTime)(&r.Time), &r.Value)
	return r, err
}

func scanQueryTimeRangeDistribution(row *sql.Rows) (QueryTimeRangeDistributionResult, error) {
	var r QueryTimeRangeDistributionResult
	err := row.Scan(&r.Label, &r.Count, &r.Percent)
	return r, err
}

// bucketTime scans a time.Time, or text in SQLiteTimeFormat, as a UTC
// time.Time.
type bucketTime time.Time

func (b *bucketTime) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*b = bucketTime(v.UTC())
		return nil
	case string:
		t, err := time.ParseInLocation(SQLiteTimeFormat, v, time.UTC)
		if err != nil {
			return fmt.Errorf("bucket time: %w", err)
		}
		*b = bucketTime(t)
		return nil
	default:
		return fmt.Errorf("bucket time: unsupported type %T", src)
	}
}

// metricMetadataRow holds a series metadata row's columns while it is
// scanned: name, type, help, unit, alert, record, dashboard and query
// counts, and last queried time.
type metricMetadataRow struct {
	mm   models.MetricMetadata
	last sql.NullTime
}

func (r *metricMetadataRow) dest() []any {
	return []any{&r.mm.Name, &r.mm.Type, &r.mm.Help, &r.mm.Unit, &r.mm.AlertCount, &r.mm.RecordCount, &r.mm.DashboardCount, &r.mm.QueryCount, &r.last}
}

func (r *metricMetadataRow) result() models.MetricMetadata {
	if r.last.Valid {
		r.mm.LastQueriedAt = r.last.Time.UTC().Format(time.RFC3339)
	}
	return r.mm
}

func scanMetricMetadata(row *sql.Rows) (models.MetricMetadata, error) {
	var r metricMetadataRow
	err := row.Scan(r.dest()...)
	return r.result(), err
}

// scanMetricMetadataWithUnused is scanMetricMetadata for rows that end with
// the is_unused flag.
func scanMetricMetadataWithUnused(row *sql.Rows) (models.MetricMetadata, error) {
	var r metricMetadataRow
	err := row.Scan(append(r.dest(), &r.mm.IsUnused)...)
	return r.result(), err
}
