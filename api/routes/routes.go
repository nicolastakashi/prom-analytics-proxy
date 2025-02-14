package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/metalmatze/signal/server/signalhttp"
	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
	"github.com/nicolastakashi/prom-analytics-proxy/api/response"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/ingester"
	metricsUsageV1 "github.com/perses/metrics-usage/pkg/api/v1"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
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
		mux.Handle("/api/v1/queries", http.HandlerFunc(r.analytics))
		mux.Handle("/api/v1/queryShortcuts", http.HandlerFunc(r.queryShortcuts))
		mux.Handle("/api/v1/seriesMetadata", http.HandlerFunc(r.seriesMetadata))
		mux.Handle("/api/v1/serieMetadata/{name}", http.HandlerFunc(r.serieMetadata))
		mux.Handle("/api/v1/serieExpressions/{name}", http.HandlerFunc(r.serieExpressions))
		mux.Handle("/api/v1/serieUsage/{name}", http.HandlerFunc(r.GetSerieUsage))

		// endpoint for perses metrics usage push from the client
		mux.Handle("/api/v1/metrics", http.HandlerFunc(r.PushMetricsUsage))
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
		timeParamNormalized, err := time.Parse(time.RFC3339, timeParam)
		if err != nil {
			slog.Error("unable to parse time parameter", "err", err)
		}
		return timeParamNormalized
	}
	return time.Now()
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

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
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
	start := time.Now()
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

func (r *routes) analytics(w http.ResponseWriter, req *http.Request) {
	query := req.FormValue("query")
	if query == "" {
		slog.Error("missing query parameter")
		writeErrorResponse(req, w, fmt.Errorf("missing query parameter"), http.StatusBadRequest)
		return
	}

	data, err := r.dbProvider.Query(req.Context(), query)
	if err != nil {
		slog.Error("unable to execute query", "err", err, "query", query)
		writeErrorResponse(req, w, fmt.Errorf("unable to execute query: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, data)
}

func (r *routes) queryShortcuts(w http.ResponseWriter, req *http.Request) {
	data := r.dbProvider.QueryShortCuts()
	writeJSONResponse(req, w, data)
}

func (r *routes) seriesMetadata(w http.ResponseWriter, req *http.Request) {
	metadata, err := r.promAPI.Metadata(req.Context(), "", r.metadataLimit)
	if err != nil {
		slog.Error("unable to retrieve series metadata", "err", err)
		writeErrorResponse(req, w, err, http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, metadata)
}

func (r *routes) serieMetadata(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		slog.Error("missing name parameter")
		writeErrorResponse(req, w, fmt.Errorf("missing name parameter"), http.StatusBadRequest)
		return
	}

	labels, _, err := r.promAPI.LabelNames(req.Context(), []string{name}, time.Now().Add(-1*time.Minute), time.Now())
	if err != nil {
		slog.Error("unable to retrieve label names", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve label names: %w", err), http.StatusInternalServerError)
		return
	}

	series, _, err := r.promAPI.Series(req.Context(), []string{name}, time.Now().Add(-5*time.Minute), time.Now(), v1.WithLimit(*r.seriesLimit))
	if err != nil {
		slog.Error("unable to retrieve series data", "err", err, "name", name)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve series data: %w", err), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, models.SerieMetadata{
		Labels:      labels,
		SeriesCount: len(series),
	})
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

	data, err := r.dbProvider.GetQueriesBySerieName(req.Context(), name, page, pageSize)
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
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
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

func (r *routes) GetSerieUsage(w http.ResponseWriter, req *http.Request) {
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

	if kind == "dashboard" {
		dashboards, err := r.dbProvider.GetDashboardUsage(req.Context(), name, page, pageSize)
		if err != nil {
			slog.Error("unable to retrieve dashboard usage", "err", err)
			writeErrorResponse(req, w, fmt.Errorf("unable to retrieve dashboard usage"), http.StatusInternalServerError)
			return
		}
		writeJSONResponse(req, w, dashboards)
		return
	}

	alerts, err := r.dbProvider.GetRulesUsage(req.Context(), name, kind, page, pageSize)
	if err != nil {
		slog.Error("unable to retrieve rules usage", "err", err)
		writeErrorResponse(req, w, fmt.Errorf("unable to retrieve rules usage"), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(req, w, alerts)
}
