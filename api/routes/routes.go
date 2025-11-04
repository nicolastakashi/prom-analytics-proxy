package routes

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/metalmatze/signal/server/signalhttp"
	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/api/response"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/ingester"
	metricsUsageV1 "github.com/perses/metrics-usage/pkg/api/v1"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	yaml "gopkg.in/yaml.v3"
)

type LimitsConfig struct {
	MetadataLimit uint64
	SeriesLimit   uint64
}

type routes struct {
	handler http.Handler
	mux     *http.ServeMux

	queryIngester     *ingester.QueryIngester
	dbProvider        db.Provider
	includeQueryStats bool
	promAPI           v1.API
	metadataLimit     string
	seriesLimit       *uint64
	limits            LimitsConfig
	config            *config.Config
}

type Option func(*routes)

func WithDBProvider(dbProvider db.Provider) Option {
	return func(r *routes) {
		r.dbProvider = dbProvider
	}
}

func WithQueryIngester(queryIngester *ingester.QueryIngester) Option {
	return func(r *routes) {
		r.queryIngester = queryIngester
	}
}

func WithConfig(cfg *config.Config) Option {
	return func(r *routes) {
		r.config = cfg
	}
}

func WithHandlers(uiFS fs.FS, registry *prometheus.Registry, isTracingEnabled bool) Option {
	return func(r *routes) {
		i := signalhttp.NewHandlerInstrumenter(registry, []string{"handler"})
		mux := http.NewServeMux()
		mux.Handle("/", r.ui(uiFS))
		mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
		mux.Handle("/api/", http.HandlerFunc(r.passthrough))
		mux.Handle("/api/v1/query", i.NewHandler(
			prometheus.Labels{"handler": "query"},
			otelhttp.NewHandler(http.HandlerFunc(r.query), "/api/v1/query"),
		))
		mux.Handle("/api/v1/query_range", i.NewHandler(
			prometheus.Labels{"handler": "query_range"},
			otelhttp.NewHandler(http.HandlerFunc(r.query_range), "/api/v1/query_range"),
		))
		mux.Handle("/api/v1/query/types", http.HandlerFunc(r.queryTypes))
		mux.Handle("/api/v1/query/average_duration", http.HandlerFunc(r.averageDuration))
		mux.Handle("/api/v1/query/rate", http.HandlerFunc(r.queryRate))
		mux.Handle("/api/v1/query/status_distribution", http.HandlerFunc(r.queryStatusDistribution))
		mux.Handle("/api/v1/query/latency", http.HandlerFunc(r.queryLatencyTrends))
		mux.Handle("/api/v1/query/throughput", http.HandlerFunc(r.queryThroughputAnalysis))
		mux.Handle("/api/v1/query/errors", http.HandlerFunc(r.queryErrorAnalysis))
		mux.Handle("/api/v1/query/time_range_distribution", http.HandlerFunc(r.queryTimeRangeDistribution))
		mux.Handle("/api/v1/query/executions", http.HandlerFunc(r.queryExecutions))
		// recent queries endpoint removed; use /api/v1/query/expressions instead
		mux.Handle("/api/v1/query/expressions", http.HandlerFunc(r.queryExpressions))
		mux.Handle("/api/v1/seriesMetadata", http.HandlerFunc(r.seriesMetadata))
		mux.Handle("/api/v1/metricStatistics/{name}", http.HandlerFunc(r.GetMetricStatistics))
		mux.Handle("/api/v1/metricQueryPerformanceStatistics/{name}", http.HandlerFunc(r.GetMetricQueryPerformanceStatistics))
		mux.Handle("/api/v1/serieExpressions/{name}", http.HandlerFunc(r.serieExpressions))
		mux.Handle("/api/v1/serieUsage/{name}", http.HandlerFunc(r.GetMetricUsage))
		mux.Handle("/api/v1/jobs", http.HandlerFunc(r.listJobs))
		mux.Handle("/api/v1/metrics/unused", http.HandlerFunc(r.metricsUnused))

		// endpoint for perses metrics usage push from the client
		mux.Handle("/api/v1/metrics", http.HandlerFunc(r.PushMetricsUsage))
		mux.Handle("/api/v1/configs", http.HandlerFunc(r.getConfigs))

		mux.Handle("/api/v1/backfill", http.HandlerFunc(r.backfill))
		r.mux = mux
	}
}

func WithProxy(upstream *url.URL) Option {
	return func(r *routes) {
		proxy := httputil.NewSingleHostReverseProxy(upstream)
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = upstream.Host // Set the Host header to the target host
			if r.includeQueryStats {
				query := req.URL.Query()
				query.Add("stats", "true")
				req.URL.RawQuery = query.Encode()
			}
		}
		r.handler = proxy
	}
}

func WithPromAPI(upstream *url.URL) Option {
	return func(r *routes) {
		c, err := api.NewClient(api.Config{
			Address: upstream.String(),
		})
		if err != nil {
			slog.Error("unable to create prometheus client", "err", err)
		}
		r.promAPI = v1.NewAPI(c)
	}
}

func WithIncludeQueryStats(includeQueryStats bool) Option {
	return func(r *routes) {
		r.includeQueryStats = includeQueryStats
	}
}

func WithLimits(limits LimitsConfig) Option {
	return func(r *routes) {
		r.limits = limits
		if limits.MetadataLimit > 0 {
			r.metadataLimit = strconv.FormatUint(limits.MetadataLimit, 10)
		}
		r.seriesLimit = &limits.SeriesLimit
	}
}

func NewRoutes(opts ...Option) (*routes, error) {
	r := &routes{
		mux: http.NewServeMux(), // Initialize mux to avoid nil pointer dereference
		limits: LimitsConfig{ // Add default limits
			MetadataLimit: 1000, // Default metadata limit
			SeriesLimit:   1000, // Default series limit
		},
	}

	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

func (r *routes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func getTimeParam(req *http.Request, param string) time.Time {
	if timeParam := req.FormValue(param); timeParam != "" {
		// Attempt RFC3339 parsing first.
		if t, err := time.Parse(time.RFC3339, timeParam); err == nil {
			return t.UTC()
		}

		// Fallback: attempt to parse as Unix timestamp (seconds).
		if ts, err := strconv.ParseInt(timeParam, 10, 64); err == nil {
			return time.Unix(ts, 0).UTC()
		}

		// Fallback: attempt to parse as Unix timestamp with fractional seconds.
		if tf, err := strconv.ParseFloat(timeParam, 64); err == nil {
			seconds := int64(tf)
			nanos := int64((tf - float64(seconds)) * 1e9)
			return time.Unix(seconds, nanos).UTC()
		}

		// All parsing attempts failed – log and fallback.
		slog.Error("failed to parse time parameter", "param", param, "value", timeParam)
	}

	// Default/fallback to current UTC time.
	return time.Now().UTC()
}

func getStepParam(req *http.Request) float64 {
	if stepParam := req.FormValue("step"); stepParam != "" {
		step, _ := strconv.ParseFloat(stepParam, 64)
		return step
	}
	return 15
}

func getQueryParamAsInt(req *http.Request, param string, defaultValue int) (int, error) {
	value := req.URL.Query().Get(param)
	if value == "" {
		return defaultValue, nil
	}
	return strconv.Atoi(value)
}

func writeJSONResponse(req *http.Request, w http.ResponseWriter, response interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("failed to encode JSON response", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("failed to encode response: %w", err), http.StatusInternalServerError)
		return
	}
}

func (r *routes) passthrough(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

func validateQuery(query db.Query) (db.Query, error) {
	if query.QueryParam == "" {
		return query, fmt.Errorf("missing query parameter")
	}
	if query.TimeParam.IsZero() {
		return query, fmt.Errorf("missing time parameter")
	}
	if !slices.Contains(db.KnownQueryTypes, query.Type) {
		return query, fmt.Errorf("invalid query type: %s, only supported is %s", query.Type, db.KnownQueryTypes)
	}
	if http.StatusText(query.StatusCode) == "" {
		return query, fmt.Errorf("invalid status code: %d", query.StatusCode)
	}
	if query.Duration < 0 {
		return query, fmt.Errorf("invalid duration: %d", query.Duration)
	}
	if query.BodySize < 0 {
		return query, fmt.Errorf("invalid body size: %d", query.BodySize)
	}
	if query.Start.IsZero() {
		return query, fmt.Errorf("missing start parameter")
	}
	if query.End.IsZero() {
		return query, fmt.Errorf("missing end parameter")
	}
	if query.Type == db.QueryTypeRange && query.End.Before(query.Start) {
		return query, fmt.Errorf("invalid range: end before start")
	}
	if query.Type == db.QueryTypeRange && query.Step <= 0 {
		return query, fmt.Errorf("invalid step: %d", query.Step)
	}
	return query, nil
}

func (r *routes) backfill(w http.ResponseWriter, req *http.Request) {
	queries := []db.Query{}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&queries); err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid request body: %w", err), http.StatusBadRequest)
		return
	}
	for _, query := range queries {
		validatedQuery, err := validateQuery(query)
		if err != nil {
			writeErrorResponse(req, w, err, http.StatusBadRequest)
			return
		}
		r.queryIngester.Ingest(validatedQuery)
	}
}

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	start := time.Now().UTC()
	query := db.Query{
		TS:   start,
		Type: db.QueryTypeInstant,
	}

	if req.Method == http.MethodPost {
		// Create a buffer to hold the request body
		var bodyBuffer bytes.Buffer
		// Create a TeeReader to duplicate the request body
		bodyReader := io.TeeReader(req.Body, &bodyBuffer)

		// Use bodyReader here so we can both read and pass it downstream
		req.Body = io.NopCloser(bodyReader)

		query.QueryParam = req.FormValue("query")
		query.TimeParam = getTimeParam(req, "time")

		// Replace the request body with a new reader from the buffer so the proxy can still read it
		req.Body = io.NopCloser(&bodyBuffer)
	}

	if req.Method == http.MethodGet {
		query.QueryParam = req.FormValue("query")
		query.TimeParam = getTimeParam(req, "time")
	}

	recw := response.NewResponseWriter(w)
	r.handler.ServeHTTP(recw, req)

	if response := recw.ParseQueryResponse(r.includeQueryStats); response != nil {
		query.TotalQueryableSamples = response.Data.Stats.Samples.TotalQueryableSamples
		query.PeakSamples = response.Data.Stats.Samples.PeakSamples
	}

	query.Duration = time.Since(start)
	query.StatusCode = recw.GetStatusCode()
	query.BodySize = recw.GetBodySize()

	r.queryIngester.Ingest(query)
}

func (r *routes) query_range(w http.ResponseWriter, req *http.Request) {
	start := time.Now().UTC()
	query := db.Query{
		TS:   start,
		Type: db.QueryTypeRange,
	}

	if req.Method == http.MethodPost {
		// Create a buffer to hold the request body
		var bodyBuffer bytes.Buffer

		// Create a TeeReader to duplicate the request body
		bodyReader := io.TeeReader(req.Body, &bodyBuffer)

		// Use bodyReader here so we can both read and pass it downstream
		req.Body = io.NopCloser(bodyReader)

		query.QueryParam = req.FormValue("query")
		query.Step = getStepParam(req)
		query.Start = getTimeParam(req, "start")
		query.End = getTimeParam(req, "end")

		// Replace the request body with a new reader from the buffer so the proxy can still read it
		req.Body = io.NopCloser(&bodyBuffer)
	}

	if req.Method == http.MethodGet {
		query.QueryParam = req.FormValue("query")
		query.Step = getStepParam(req)
		query.Start = getTimeParam(req, "start")
		query.End = getTimeParam(req, "end")
	}

	recw := response.NewResponseWriter(w)
	r.handler.ServeHTTP(recw, req)

	if response := recw.ParseQueryResponse(r.includeQueryStats); response != nil {
		query.TotalQueryableSamples = response.Data.Stats.Samples.TotalQueryableSamples
		query.PeakSamples = response.Data.Stats.Samples.PeakSamples
	}

	query.Duration = time.Since(start)
	query.StatusCode = recw.GetStatusCode()
	query.BodySize = recw.GetBodySize()

	r.queryIngester.Ingest(query)
}

func (r *routes) queryTypes(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	if from.IsZero() {
		from = time.Now().UTC().Add(-7 * 24 * time.Hour)
	}

	if to.IsZero() {
		to = time.Now().UTC()
	}

	data, err := r.dbProvider.GetQueryTypes(req.Context(), db.TimeRange{From: from, To: to}, fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) averageDuration(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetAverageDuration(req.Context(), db.TimeRange{From: from, To: to}, fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryRate(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetQueryRate(req.Context(), db.TimeRange{From: from, To: to}, "", fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryStatusDistribution(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetQueryStatusDistribution(req.Context(), db.TimeRange{From: from, To: to}, fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryLatencyTrends(w http.ResponseWriter, req *http.Request) {

	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	metric_name := req.FormValue("metricName")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetQueryLatencyTrends(req.Context(), db.TimeRange{From: from, To: to}, metric_name, fingerprint)

	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryThroughputAnalysis(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	data, err := r.dbProvider.GetQueryThroughputAnalysis(req.Context(), db.TimeRange{From: from, To: to})
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryErrorAnalysis(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetQueryErrorAnalysis(req.Context(), db.TimeRange{From: from, To: to}, fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryTimeRangeDistribution(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")

	data, err := r.dbProvider.GetQueryTimeRangeDistribution(req.Context(), db.TimeRange{From: from, To: to}, fingerprint)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryExpressions(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid page parameter: %w", err), http.StatusBadRequest)
		return
	}

	pageSize, err := getQueryParamAsInt(req, "pageSize", 10)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid pageSize parameter: %w", err), http.StatusBadRequest)
		return
	}

	sortBy := req.FormValue("sortBy")
	sortOrder := req.FormValue("sortOrder")
	filter := req.FormValue("filter")

	params := db.QueryExpressionsParams{
		TimeRange: db.TimeRange{From: from, To: to},
		Page:      page,
		PageSize:  pageSize,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		Filter:    filter,
	}

	data, err := r.dbProvider.GetQueryExpressions(req.Context(), params)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryExecutions(w http.ResponseWriter, req *http.Request) {
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")
	fingerprint := req.FormValue("fingerprint")
	if fingerprint == "" {
		writeErrorResponse(req, w, fmt.Errorf("missing fingerprint parameter"), http.StatusBadRequest)
		return
	}
	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid page parameter: %w", err), http.StatusBadRequest)
		return
	}
	pageSize, err := getQueryParamAsInt(req, "pageSize", 10)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid pageSize parameter: %w", err), http.StatusBadRequest)
		return
	}
	sortBy := req.FormValue("sortBy")
	sortOrder := req.FormValue("sortOrder")
	typ := req.FormValue("type")
	if typ != "instant" && typ != "range" {
		typ = ""
	}

	params := db.QueryExecutionsParams{
		Fingerprint: fingerprint,
		TimeRange:   db.TimeRange{From: from, To: to},
		Page:        page,
		PageSize:    pageSize,
		SortBy:      sortBy,
		SortOrder:   sortOrder,
		Type:        typ,
	}
	data, err := r.dbProvider.GetQueryExecutions(req.Context(), params)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(req, w, data)
}

func (r *routes) seriesMetadata(w http.ResponseWriter, req *http.Request) {
	params := db.SeriesMetadataParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "name",
		SortOrder: "asc",
		Filter:    "",
		Type:      "",
	}

	if page, err := getQueryParamAsInt(req, "page", 1); err == nil {
		params.Page = page
	}
	if pageSize, err := getQueryParamAsInt(req, "pageSize", 10); err == nil {
		if pageSize < 1 {
			params.PageSize = 1
		} else if pageSize > db.MaxPageSize {
			params.PageSize = db.MaxPageSize
		} else {
			params.PageSize = pageSize
		}
	}
	// Validate sortBy parameter against whitelist to prevent SQL injection
	if sortBy := req.FormValue("sortBy"); sortBy != "" {
		if db.ValidSeriesMetadataSortFields[sortBy] {
			params.SortBy = sortBy
		} else {
			// Invalid sortBy provided - log warning and use safe default
			slog.Warn("invalid sortBy parameter provided", "sortBy", sortBy, "using_default", "name")
			params.SortBy = "name" // Safe default
		}
	}

	// Validate sortOrder parameter against whitelist to prevent SQL injection
	if sortOrder := req.FormValue("sortOrder"); sortOrder != "" {
		normalizedOrder := strings.ToLower(strings.TrimSpace(sortOrder))
		if db.ValidSortDirections[normalizedOrder] {
			params.SortOrder = normalizedOrder
		} else {
			// Invalid sortOrder provided - log warning and use safe default
			slog.Warn("invalid sortOrder parameter provided", "sortOrder", sortOrder, "using_default", "asc")
			params.SortOrder = "asc" // Safe default
		}
	}
	if filter := req.FormValue("filter"); filter != "" {
		params.Filter = filter
	}
	if metricType := req.FormValue("type"); metricType != "" {
		params.Type = metricType
	}

	if unused := req.FormValue("unused"); unused == "true" {
		params.Unused = true
	}

	if job := req.FormValue("job"); job != "" {
		params.Job = job
	}

	data, err := r.dbProvider.GetSeriesMetadata(req.Context(), params)
	if err != nil {
		slog.Error("unable to retrieve series metadata (db)", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve series metadata: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) GetMetricStatistics(w http.ResponseWriter, req *http.Request) {
	// TODO: Eventually we should cache this data

	name := req.PathValue("name")
	if name == "" {
		slog.Error("missing name parameter")
		writeErrorResponse(req, w, fmt.Errorf("missing name parameter"), http.StatusBadRequest)
		return
	}

	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	statistics, err := r.dbProvider.GetMetricStatistics(req.Context(), name, db.TimeRange{From: from, To: to})
	if err != nil {
		slog.Error("unable to retrieve metric statistics", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve metric statistics: %w", err), http.StatusInternalServerError)
		return
	}

	series, _, err := r.promAPI.Series(req.Context(), []string{name}, time.Now().UTC().Add(-5*time.Minute), time.Now().UTC(), v1.WithLimit(*r.seriesLimit))
	if err != nil {
		slog.Error("unable to retrieve series data", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve series data: %w", err), http.StatusInternalServerError)
		return
	}

	labels, _, err := r.promAPI.LabelNames(req.Context(), []string{name}, from, to)
	if err != nil {
		slog.Error("unable to retrieve label names", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve label names: %w", err), http.StatusInternalServerError)
		return
	}

	jobSeriesCount := make(map[string]int)
	for _, serie := range series {
		if job, ok := serie["job"]; ok {
			jobSeriesCount[string(job)]++
		}
	}

	statistics.SerieCount = len(series)
	statistics.LabelCount = len(labels)
	statistics.Producers = make([]db.MetricProducersResult, 0, len(jobSeriesCount))
	for job, count := range jobSeriesCount {
		statistics.Producers = append(statistics.Producers, db.MetricProducersResult{
			Job:    job,
			Series: count,
		})
	}

	writeJSONResponse(req, w, statistics)
}

func (r *routes) GetMetricQueryPerformanceStatistics(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		slog.Error("missing name parameter")
		writeErrorResponse(req, w, fmt.Errorf("missing name parameter"), http.StatusBadRequest)
		return
	}

	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	queryRate, err := r.dbProvider.GetQueryRate(req.Context(), db.TimeRange{From: from, To: to}, name, "")
	if err != nil {
		slog.Error("unable to retrieve metric query performance statistics", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve metric query performance statistics: %w", err), http.StatusInternalServerError)
		return
	}

	queryPerformanceStatistics, err := r.dbProvider.GetMetricQueryPerformanceStatistics(req.Context(), name, db.TimeRange{From: from, To: to})
	if err != nil {
		slog.Error("unable to retrieve metric query performance statistics", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve metric query performance statistics: %w", err), http.StatusInternalServerError)
		return
	}

	queryPerformanceStatistics.QueryRate = queryRate

	writeJSONResponse(req, w, queryPerformanceStatistics)
}

func (r *routes) serieExpressions(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")

	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid page parameter: %w", err), http.StatusBadRequest)
		return
	}

	pageSize, err := getQueryParamAsInt(req, "pageSize", 1)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid pageSize parameter: %w", err), http.StatusBadRequest)
		return
	}

	sortBy := req.URL.Query().Get("sortBy")
	sortOrder := req.URL.Query().Get("sortOrder")
	filter := req.URL.Query().Get("filter")
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	params := db.QueriesBySerieNameParams{
		SerieName: name,
		Page:      page,
		PageSize:  pageSize,
		Filter:    filter,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		TimeRange: db.TimeRange{From: from, To: to},
	}

	data, err := r.dbProvider.GetQueriesBySerieName(req.Context(), params)
	if err != nil {
		slog.Error("failed to retrieve series expressions", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("failed to retrieve series expressions: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) ui(uiFS fs.FS) http.HandlerFunc {
	uiHandler := http.ServeMux{}
	err := fs.WalkDir(uiFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		b, err := fs.ReadFile(uiFS, path)
		if err != nil {
			return fmt.Errorf("failed to read ui file %s: %w", path, err)
		}

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to receive file info %s: %w", path, err)
		}

		paths := []string{fmt.Sprintf("/%s", path)}

		if paths[0] == "/index.html" {
			paths = append(paths, "/")
		}

		for _, path := range paths {
			uiHandler.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
				http.ServeContent(w, r, d.Name(), fi.ModTime(), bytes.NewReader(b))
			})
		}
		return nil
	})
	if err != nil {
		slog.Error("failed to walk ui directory", "err", err)
		return nil
	}

	return uiHandler.ServeHTTP
}

func writeErrorResponse(r *http.Request, w http.ResponseWriter, err error, status int) {
	response := struct {
		Error   string `json:"error"`
		Code    int    `json:"code"`
		TraceID string `json:"traceId,omitempty"`
	}{
		Error:   err.Error(),
		Code:    status,
		TraceID: trace.SpanFromContext(r.Context()).SpanContext().TraceID().String(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("failed to encode JSON response", "err", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (r *routes) PushMetricsUsage(w http.ResponseWriter, req *http.Request) {
	usage := make(map[string]*metricsUsageV1.MetricUsage)

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	if err := json.NewDecoder(req.Body).Decode(&usage); err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid request body: %w", err), http.StatusBadRequest)
		return
	}

	for name, metricUsage := range usage {
		rulesUsage := make([]db.RulesUsage, 0, len(metricUsage.AlertRules)+len(metricUsage.RecordingRules))
		for usage := range metricUsage.AlertRules {
			rulesUsage = append(rulesUsage, db.RulesUsage{
				Serie:      name,
				GroupName:  usage.GroupName,
				Name:       usage.Name,
				Expression: usage.Expression,
				Kind:       string(db.RuleUsageKindAlert),
			})
		}

		for usage := range metricUsage.RecordingRules {
			rulesUsage = append(rulesUsage, db.RulesUsage{
				Serie:      name,
				GroupName:  usage.GroupName,
				Name:       usage.Name,
				Expression: usage.Expression,
				Kind:       string(db.RuleUsageKindRecord),
			})
		}

		if err := r.dbProvider.InsertRulesUsage(ctx, rulesUsage); err != nil {
			slog.Error("unable to insert rules usage", "err", err)
			writeErrorResponse(req, w, fmt.Errorf("unable to insert rules usage: %w", err), http.StatusInternalServerError)
			return
		}

		dashboardUsage := make([]db.DashboardUsage, 0, len(metricUsage.Dashboards))
		for usage := range metricUsage.Dashboards {
			dashboardUsage = append(dashboardUsage, db.DashboardUsage{
				Serie: name,
				Id:    usage.ID,
				Name:  usage.Name,
				URL:   usage.URL,
			})
		}

		if err := r.dbProvider.InsertDashboardUsage(ctx, dashboardUsage); err != nil {
			slog.Error("unable to insert dashboard usage", "err", err)
			writeErrorResponse(req, w, fmt.Errorf("unable to insert dashboard usage: %w", err), http.StatusInternalServerError)
			return
		}
	}
}

func (r *routes) GetMetricUsage(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		slog.Error("missing name parameter")
		writeErrorResponse(req, w, fmt.Errorf("missing name parameter"), http.StatusBadRequest)
		return
	}

	kind := req.URL.Query().Get("kind")
	if kind == "" {
		slog.Error("missing kind parameter", "name", name)
		writeErrorResponse(req, w, fmt.Errorf("missing kind parameter"), http.StatusBadRequest)
		return
	}

	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		slog.Error("invalid page parameter", "err", err, "name", name, "kind", kind)
		writeErrorResponse(req, w, fmt.Errorf("invalid page parameter: %w", err), http.StatusBadRequest)
		return
	}

	pageSize, err := getQueryParamAsInt(req, "pageSize", 1)
	if err != nil {
		slog.Error("invalid pageSize parameter", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("invalid pageSize parameter"), http.StatusBadRequest)
		return
	}

	filter := req.URL.Query().Get("filter")
	sortBy := req.URL.Query().Get("sortBy")
	sortOrder := req.URL.Query().Get("sortOrder")
	from := getTimeParam(req, "from")
	to := getTimeParam(req, "to")

	if kind == "dashboard" {
		params := db.DashboardUsageParams{
			Serie:     name,
			Page:      page,
			PageSize:  pageSize,
			Filter:    filter,
			SortBy:    sortBy,
			SortOrder: sortOrder,
			TimeRange: db.TimeRange{From: from, To: to},
		}

		dashboards, err := r.dbProvider.GetDashboardUsage(req.Context(), params)
		if err != nil {
			slog.Error("unable to retrieve dashboard usage", "err", err)
			writeErrorResponse(req, w, fmt.Errorf("unable to retrieve dashboard usage"), http.StatusInternalServerError)
			return
		}
		writeJSONResponse(req, w, dashboards)
		return
	}

	params := db.RulesUsageParams{
		Serie:     name,
		Kind:      kind,
		Page:      page,
		PageSize:  pageSize,
		Filter:    filter,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		TimeRange: db.TimeRange{From: from, To: to},
	}

	alerts, err := r.dbProvider.GetRulesUsage(req.Context(), params)
	if err != nil {
		slog.Error("unable to retrieve rules usage", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve rules usage"), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, alerts)
}

func (r *routes) getConfigs(w http.ResponseWriter, req *http.Request) {
	if r.config == nil {
		writeErrorResponse(req, w, fmt.Errorf("configuration not available"), http.StatusInternalServerError)
		return
	}

	sanitizedConfig := r.config.GetSanitizedConfig()
	yamlData, err := yaml.Marshal(sanitizedConfig)
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("failed to marshal YAML: %w", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(yamlData); err != nil {
		writeErrorResponse(req, w, fmt.Errorf("failed to write response: %w", err), http.StatusInternalServerError)
		return
	}
}

func (r *routes) listJobs(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	jobs, err := r.dbProvider.ListJobs(ctx)
	if err != nil {
		slog.Error("unable to list jobs", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to list jobs: %w", err), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(req, w, struct {
		Data []string `json:"data"`
	}{Data: jobs})
}

func (r *routes) metricsUnused(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	q := req.URL.Query()
	name := q.Get("name")
	if name == "" {
		writeErrorResponse(req, w, fmt.Errorf("missing name parameter"), http.StatusBadRequest)
		return
	}
	var names []string
	reader := csv.NewReader(strings.NewReader(name))
	reader.FieldsPerRecord = -1
	fields, err := reader.Read()
	if err != nil {
		writeErrorResponse(req, w, fmt.Errorf("invalid name CSV: %w", err), http.StatusBadRequest)
		return
	}
	for _, f := range fields {
		if trimmed := strings.Trim(strings.TrimSpace(f), "\"'"); trimmed != "" {
			names = append(names, trimmed)
		}
	}

	if len(names) == 0 {
		writeErrorResponse(req, w, fmt.Errorf("no valid names provided"), http.StatusBadRequest)
		return
	}
	if len(names) > 100 {
		writeErrorResponse(req, w, fmt.Errorf("too many names; max 100"), http.StatusBadRequest)
		return
	}

	items := make([]models.UnusedMetric, 0, len(names))

	job := strings.Trim(strings.TrimSpace(q.Get("job")), "\"'")

	mm, err := r.dbProvider.GetSeriesMetadataByNames(ctx, names, job)
	if err != nil {
		slog.Error("metricsUnused: GetSeriesMetadataByNames", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve series metadata"), http.StatusInternalServerError)
		return
	}

	for _, m := range mm {
		item := models.UnusedMetric{
			Name:   m.Name,
			Unused: m.AlertCount == 0 && m.RecordCount == 0 && m.DashboardCount == 0 && m.QueryCount == 0,
			Summary: models.UnusedSummary{
				AlertCount:     m.AlertCount,
				RecordCount:    m.RecordCount,
				DashboardCount: m.DashboardCount,
				QueryCount:     m.QueryCount,
			},
		}
		items = append(items, item)
	}

	writeJSONResponse(req, w, struct {
		Data []models.UnusedMetric `json:"data"`
	}{Data: items})
}
