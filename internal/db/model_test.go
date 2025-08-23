package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueryType_Constants(t *testing.T) {
	assert.Equal(t, QueryType("range"), QueryTypeRange)
	assert.Equal(t, QueryType("instant"), QueryTypeInstant)
}

func TestDatabaseProvider_Constants(t *testing.T) {
	assert.Equal(t, DatabaseProvider("postgresql"), PostGreSQL)
	assert.Equal(t, DatabaseProvider("sqlite"), SQLite)
}

func TestRuleUsageKind_Constants(t *testing.T) {
	assert.Equal(t, RuleUsageKind("alert"), RuleUsageKindAlert)
	assert.Equal(t, RuleUsageKind("record"), RuleUsageKindRecord)
}

func TestQuery_Struct(t *testing.T) {
	now := time.Now().UTC()
	query := Query{
		TS:                    now,
		QueryParam:            "up",
		TimeParam:             now,
		Duration:              100 * time.Millisecond,
		StatusCode:            200,
		BodySize:              1024,
		LabelMatchers:         LabelMatchers{{"job": "prometheus"}},
		Fingerprint:           "abc123",
		Type:                  QueryTypeInstant,
		Step:                  15.0,
		Start:                 now.Add(-1 * time.Hour),
		End:                   now,
		TotalQueryableSamples: 1000,
		PeakSamples:           500,
	}

	assert.Equal(t, now, query.TS)
	assert.Equal(t, "up", query.QueryParam)
	assert.Equal(t, now, query.TimeParam)
	assert.Equal(t, 100*time.Millisecond, query.Duration)
	assert.Equal(t, 200, query.StatusCode)
	assert.Equal(t, 1024, query.BodySize)
	assert.Equal(t, LabelMatchers{{"job": "prometheus"}}, query.LabelMatchers)
	assert.Equal(t, "abc123", query.Fingerprint)
	assert.Equal(t, QueryTypeInstant, query.Type)
	assert.Equal(t, 15.0, query.Step)
	assert.Equal(t, now.Add(-1*time.Hour), query.Start)
	assert.Equal(t, now, query.End)
	assert.Equal(t, 1000, query.TotalQueryableSamples)
	assert.Equal(t, 500, query.PeakSamples)
}

func TestLabelMatchers_Type(t *testing.T) {
	matchers := LabelMatchers{
		{"job": "prometheus", "instance": "localhost:9090"},
		{"__name__": "up"},
	}

	assert.Len(t, matchers, 2)
	assert.Equal(t, "prometheus", matchers[0]["job"])
	assert.Equal(t, "localhost:9090", matchers[0]["instance"])
	assert.Equal(t, "up", matchers[1]["__name__"])
}

func TestQueryTypesResult_Struct(t *testing.T) {
	totalQueries := 100
	instantPercent := 60.0
	rangePercent := 40.0

	result := QueryTypesResult{
		TotalQueries:   &totalQueries,
		InstantPercent: &instantPercent,
		RangePercent:   &rangePercent,
	}

	assert.Equal(t, &totalQueries, result.TotalQueries)
	assert.Equal(t, &instantPercent, result.InstantPercent)
	assert.Equal(t, &rangePercent, result.RangePercent)
}

func TestAverageDurationResult_Struct(t *testing.T) {
	avgDuration := 150.5
	deltaPercent := 10.2

	result := AverageDurationResult{
		AvgDuration:  &avgDuration,
		DeltaPercent: &deltaPercent,
	}

	assert.Equal(t, &avgDuration, result.AvgDuration)
	assert.Equal(t, &deltaPercent, result.DeltaPercent)
}

func TestQueryRateResult_Struct(t *testing.T) {
	successTotal := 95
	successRatePercent := 95.0
	errorTotal := 5
	errorRatePercent := 5.0

	result := QueryRateResult{
		SuccessTotal:       &successTotal,
		SuccessRatePercent: &successRatePercent,
		ErrorTotal:         &errorTotal,
		ErrorRatePercent:   &errorRatePercent,
	}

	assert.Equal(t, &successTotal, result.SuccessTotal)
	assert.Equal(t, &successRatePercent, result.SuccessRatePercent)
	assert.Equal(t, &errorTotal, result.ErrorTotal)
	assert.Equal(t, &errorRatePercent, result.ErrorRatePercent)
}

func TestPagedResult_Struct(t *testing.T) {
	data := []string{"item1", "item2", "item3"}

	result := PagedResult{
		TotalPages: 2,
		Total:      15,
		Data:       data,
	}

	assert.Equal(t, 2, result.TotalPages)
	assert.Equal(t, 15, result.Total)
	assert.Equal(t, data, result.Data)
}

func TestQueriesBySerieNameResult_Struct(t *testing.T) {
	now := time.Now().UTC()

	result := QueriesBySerieNameResult{
		Query:           "up{job=\"prometheus\"}",
		AvgDuration:     120.5,
		AvgPeakySamples: 800.0,
		MaxPeakSamples:  1200,
		TS:              now,
	}

	assert.Equal(t, "up{job=\"prometheus\"}", result.Query)
	assert.Equal(t, 120.5, result.AvgDuration)
	assert.Equal(t, 800.0, result.AvgPeakySamples)
	assert.Equal(t, 1200, result.MaxPeakSamples)
	assert.Equal(t, now, result.TS)
}

func TestQueryStatusDistributionResult_Struct(t *testing.T) {
	result := QueryStatusDistributionResult{
		Time:      "2023-01-01T12:00:00Z",
		Status2xx: 90,
		Status4xx: 5,
		Status5xx: 5,
	}

	assert.Equal(t, "2023-01-01T12:00:00Z", result.Time)
	assert.Equal(t, 90, result.Status2xx)
	assert.Equal(t, 5, result.Status4xx)
	assert.Equal(t, 5, result.Status5xx)
}

func TestQueryLatencyTrendsResult_Struct(t *testing.T) {
	result := QueryLatencyTrendsResult{
		Value: 150.5,
		P95:   200,
		Time:  "2023-01-01T12:00:00Z",
	}

	assert.Equal(t, 150.5, result.Value)
	assert.Equal(t, 200, result.P95)
	assert.Equal(t, "2023-01-01T12:00:00Z", result.Time)
}

func TestQueryThroughputAnalysisResult_Struct(t *testing.T) {
	result := QueryThroughputAnalysisResult{
		Value: 1000.0,
		Time:  "2023-01-01T12:00:00Z",
	}

	assert.Equal(t, 1000.0, result.Value)
	assert.Equal(t, "2023-01-01T12:00:00Z", result.Time)
}

func TestQueryErrorAnalysisResult_Struct(t *testing.T) {
	result := QueryErrorAnalysisResult{
		Time:  "2023-01-01T12:00:00Z",
		Value: 5.0,
	}

	assert.Equal(t, "2023-01-01T12:00:00Z", result.Time)
	assert.Equal(t, 5.0, result.Value)
}

func TestMetricProducersResult_Struct(t *testing.T) {
	result := MetricProducersResult{
		Job:    "prometheus",
		Series: 1000,
	}

	assert.Equal(t, "prometheus", result.Job)
	assert.Equal(t, 1000, result.Series)
}

func TestMetricUsageStatics_Struct(t *testing.T) {
	producers := []MetricProducersResult{
		{Job: "prometheus", Series: 500},
		{Job: "node-exporter", Series: 300},
	}

	result := MetricUsageStatics{
		SerieCount:      1000,
		LabelCount:      50,
		AlertCount:      10,
		RecordCount:     5,
		DashboardCount:  20,
		TotalAlerts:     15,
		TotalRecords:    8,
		TotalDashboards: 25,
		Producers:       producers,
	}

	assert.Equal(t, 1000, result.SerieCount)
	assert.Equal(t, 50, result.LabelCount)
	assert.Equal(t, 10, result.AlertCount)
	assert.Equal(t, 5, result.RecordCount)
	assert.Equal(t, 20, result.DashboardCount)
	assert.Equal(t, 15, result.TotalAlerts)
	assert.Equal(t, 8, result.TotalRecords)
	assert.Equal(t, 25, result.TotalDashboards)
	assert.Equal(t, producers, result.Producers)
}

func TestRulesUsage_Struct(t *testing.T) {
	now := time.Now().UTC()

	result := RulesUsage{
		Serie:      "up",
		GroupName:  "default",
		Name:       "up_alert",
		Expression: "up == 0",
		Kind:       "alert",
		Labels:     []string{"severity", "job"},
		CreatedAt:  now,
	}

	assert.Equal(t, "up", result.Serie)
	assert.Equal(t, "default", result.GroupName)
	assert.Equal(t, "up_alert", result.Name)
	assert.Equal(t, "up == 0", result.Expression)
	assert.Equal(t, "alert", result.Kind)
	assert.Equal(t, []string{"severity", "job"}, result.Labels)
	assert.Equal(t, now, result.CreatedAt)
}

func TestDashboardUsage_Struct(t *testing.T) {
	now := time.Now().UTC()

	result := DashboardUsage{
		Id:        "dashboard-1",
		Serie:     "up",
		Name:      "System Overview",
		URL:       "http://localhost:3000/dashboard",
		CreatedAt: now,
	}

	assert.Equal(t, "dashboard-1", result.Id)
	assert.Equal(t, "up", result.Serie)
	assert.Equal(t, "System Overview", result.Name)
	assert.Equal(t, "http://localhost:3000/dashboard", result.URL)
	assert.Equal(t, now, result.CreatedAt)
}

func TestRulesUsageParams_Struct(t *testing.T) {
	tr := TimeRange{
		From: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
	}

	params := RulesUsageParams{
		Serie:     "up",
		Kind:      "alert",
		Filter:    "severity=critical",
		Page:      1,
		PageSize:  20,
		SortBy:    "created_at",
		SortOrder: "desc",
		TimeRange: tr,
	}

	assert.Equal(t, "up", params.Serie)
	assert.Equal(t, "alert", params.Kind)
	assert.Equal(t, "severity=critical", params.Filter)
	assert.Equal(t, 1, params.Page)
	assert.Equal(t, 20, params.PageSize)
	assert.Equal(t, "created_at", params.SortBy)
	assert.Equal(t, "desc", params.SortOrder)
	assert.Equal(t, tr, params.TimeRange)
}

func TestDashboardUsageParams_Struct(t *testing.T) {
	tr := TimeRange{
		From: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
	}

	params := DashboardUsageParams{
		Serie:     "up",
		Filter:    "title=overview",
		Page:      1,
		PageSize:  20,
		SortBy:    "created_at",
		SortOrder: "desc",
		TimeRange: tr,
	}

	assert.Equal(t, "up", params.Serie)
	assert.Equal(t, "title=overview", params.Filter)
	assert.Equal(t, 1, params.Page)
	assert.Equal(t, 20, params.PageSize)
	assert.Equal(t, "created_at", params.SortBy)
	assert.Equal(t, "desc", params.SortOrder)
	assert.Equal(t, tr, params.TimeRange)
}

func TestMetricQueryPerformanceStatistics_Struct(t *testing.T) {
	queryRate := &QueryRateResult{
		SuccessTotal:       &[]int{95}[0],
		SuccessRatePercent: &[]float64{95.0}[0],
		ErrorTotal:         &[]int{5}[0],
		ErrorRatePercent:   &[]float64{5.0}[0],
	}

	totalQueries := 100
	averageSamples := 800.0
	peakSamples := 1200
	averageDuration := 150.5
	sampleRate := 1000.0

	result := MetricQueryPerformanceStatistics{
		QueryRate:       queryRate,
		TotalQueries:    &totalQueries,
		AverageSamples:  &averageSamples,
		PeakSamples:     &peakSamples,
		AverageDuration: &averageDuration,
		SampleRate:      &sampleRate,
	}

	assert.Equal(t, queryRate, result.QueryRate)
	assert.Equal(t, &totalQueries, result.TotalQueries)
	assert.Equal(t, &averageSamples, result.AverageSamples)
	assert.Equal(t, &peakSamples, result.PeakSamples)
	assert.Equal(t, &averageDuration, result.AverageDuration)
	assert.Equal(t, &sampleRate, result.SampleRate)
}

func TestQueriesBySerieNameParams_Struct(t *testing.T) {
	tr := TimeRange{
		From: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
	}

	params := QueriesBySerieNameParams{
		SerieName: "up",
		TimeRange: tr,
		Page:      1,
		PageSize:  20,
		Filter:    "duration>100ms",
		SortBy:    "avg_duration",
		SortOrder: "desc",
	}

	assert.Equal(t, "up", params.SerieName)
	assert.Equal(t, tr, params.TimeRange)
	assert.Equal(t, 1, params.Page)
	assert.Equal(t, 20, params.PageSize)
	assert.Equal(t, "duration>100ms", params.Filter)
	assert.Equal(t, "avg_duration", params.SortBy)
	assert.Equal(t, "desc", params.SortOrder)
}

func TestSeriesMetadataParams_Struct(t *testing.T) {
	params := SeriesMetadataParams{
		Page:      1,
		PageSize:  20,
		SortBy:    "name",
		SortOrder: "asc",
		Filter:    "type=counter",
		Type:      "counter",
	}

	assert.Equal(t, 1, params.Page)
	assert.Equal(t, 20, params.PageSize)
	assert.Equal(t, "name", params.SortBy)
	assert.Equal(t, "asc", params.SortOrder)
	assert.Equal(t, "type=counter", params.Filter)
	assert.Equal(t, "counter", params.Type)
}
