package routes

import (
	"bytes"
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
)

type routes struct {
	handler http.Handler
	mux     *http.ServeMux

	queryIngester     *ingester.QueryIngester
	dbProvider        db.Provider
	includeQueryStats bool
	promAPI           v1.API
	metadataLimit     string
	seriesLimit       *uint64
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

func WithMetadataLimit(limit uint64) Option {
	return func(r *routes) {
		if limit > 0 {
			r.metadataLimit = strconv.FormatUint(limit, 10)
		}
	}
}

func WithSeriesLimit(limit uint64) Option {
	return func(r *routes) {
		r.seriesLimit = &limit
	}
}

func NewRoutes(opts ...Option) (*routes, error) {
	r := &routes{
		mux: http.NewServeMux(), // Initialize mux to avoid nil pointer dereference
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

func writeJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("unable to encode results to JSON", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "missing query parameter", http.StatusBadRequest)
		return
	}

	data, err := r.dbProvider.Query(req.Context(), query)
	if err != nil {
		slog.Error("unable to execute query", "err", err)
		http.Error(w, fmt.Sprintf("unable to execute query: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(w, data)
}

func (r *routes) queryShortcuts(w http.ResponseWriter, req *http.Request) {
	data := r.dbProvider.QueryShortCuts()
	writeJSONResponse(w, data)
}

func (r *routes) seriesMetadata(w http.ResponseWriter, req *http.Request) {
	metadata, err := r.promAPI.Metadata(req.Context(), "", r.metadataLimit)
	if err != nil {
		slog.Error("unable to retrieve series metadata", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(w, metadata)
}

func (r *routes) serieMetadata(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	labels, _, err := r.promAPI.LabelNames(req.Context(), []string{name}, time.Now().Add(-1*time.Minute), time.Now())
	if err != nil {
		slog.Error("unable to retrieve series metadata", "err", err)
		http.Error(w, "unable to retrieve series metadata", http.StatusInternalServerError)
		return
	}

	series, _, err := r.promAPI.Series(req.Context(), []string{name}, time.Now().Add(-5*time.Minute), time.Now(), v1.WithLimit(*r.seriesLimit))
	if err != nil {
		slog.Error("unable to retrieve series count", "err", err)
		http.Error(w, "unable to retrieve series count", http.StatusInternalServerError)
		return
	}

	writeJSONResponse(w, models.SerieMetadata{
		Labels:      labels,
		SeriesCount: len(series),
	})
}

func (r *routes) serieExpressions(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")

	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		slog.Error("unable to parse page parameter", "err", err)
		http.Error(w, "unable to parse page parameter", http.StatusBadRequest)
		return
	}

	pageSize, err := getQueryParamAsInt(req, "pageSize", 1)
	if err != nil {
		slog.Error("unable to parse pageSize parameter", "err", err)
		http.Error(w, "unable to parse pageSize parameter", http.StatusBadRequest)
		return
	}

	data, err := r.dbProvider.GetQueriesBySerieName(req.Context(), name, page, pageSize)
	if err != nil {
		slog.Error("unable to retrieve series expressions", "err", err)
		http.Error(w, "unable to retrieve series expressions", http.StatusInternalServerError)
		return
	}

	writeJSONResponse(w, data)
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

var usage = make(map[string]*metricsUsageV1.MetricUsage)

func (r *routes) PushMetricsUsage(w http.ResponseWriter, req *http.Request) {
	if err := json.NewDecoder(req.Body).Decode(&usage); err != nil {
		slog.Error("unable to decode request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
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

		if err := r.dbProvider.InsertRulesUsage(req.Context(), rulesUsage); err != nil {
			slog.Error("unable to insert rules usage", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

		if err := r.dbProvider.InsertDashboardUsage(req.Context(), dashboardUsage); err != nil {
			slog.Error("unable to insert dashboard usage", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (r *routes) GetSerieUsage(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		http.Error(w, "missing name parameter", http.StatusBadRequest)
		return
	}

	kind := req.URL.Query().Get("kind")
	if kind == "" {
		http.Error(w, "missing kind parameter", http.StatusBadRequest)
		return
	}

	page, err := getQueryParamAsInt(req, "page", 1)
	if err != nil {
		slog.Error("unable to parse page parameter", "err", err)
		http.Error(w, "unable to parse page parameter", http.StatusBadRequest)
		return
	}

	pageSize, err := getQueryParamAsInt(req, "pageSize", 1)
	if err != nil {
		slog.Error("unable to parse pageSize parameter", "err", err)
		http.Error(w, "unable to parse pageSize parameter", http.StatusBadRequest)
		return
	}

	if kind == "dashboard" {
		dashboards, err := r.dbProvider.GetDashboardUsage(req.Context(), name, page, pageSize)
		if err != nil {
			slog.Error("unable to retrieve series dashboards", "err", err)
			http.Error(w, "unable to retrieve series dashboards", http.StatusInternalServerError)
			return
		}
		writeJSONResponse(w, dashboards)
		return
	}

	alerts, err := r.dbProvider.GetRulesUsage(req.Context(), name, kind, page, pageSize)
	if err != nil {
		slog.Error("unable to retrieve series expressions", "err", err)
		http.Error(w, "unable to retrieve series expressions", http.StatusInternalServerError)
		return
	}

	writeJSONResponse(w, alerts)
}
