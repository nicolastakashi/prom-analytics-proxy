package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/MichaHoffmann/prom-analytics-proxy/internal/db"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
)

type routes struct {
	upstream *url.URL
	handler  http.Handler
	mux      *http.ServeMux

	queryIngester     *ingester.QueryIngester
	dbProvider        db.Provider
	includeQueryStats bool
}

type Response struct {
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

type Data struct {
	ResultType string `json:"resultType"`
	Stats      Stats  `json:"stats"`
}

type Stats struct {
	Timings Timings `json:"timings"`
	Samples Samples `json:"samples"`
}

type Timings struct {
	EvalTotalTime        float64 `json:"evalTotalTime"`
	ResultSortTime       float64 `json:"resultSortTime"`
	QueryPreparationTime float64 `json:"queryPreparationTime"`
	InnerEvalTime        float64 `json:"innerEvalTime"`
	ExecQueueTime        float64 `json:"execQueueTime"`
	ExecTotalTime        float64 `json:"execTotalTime"`
}

type Samples struct {
	TotalQueryableSamples int `json:"totalQueryableSamples"`
	PeakSamples           int `json:"peakSamples"`
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

func WithUIFS(uiFS fs.FS) Option {
	return func(r *routes) {
		mux := http.NewServeMux()
		mux.Handle("/", r.ui(uiFS))
		mux.Handle("/api/", http.HandlerFunc(r.passthrough))
		mux.Handle("/api/v1/query", http.HandlerFunc(r.query))
		mux.Handle("/api/v1/query_range", http.HandlerFunc(r.query_range))
		mux.Handle("/api/v1/analytics", http.HandlerFunc(r.analytics))
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
		r.upstream = upstream
		r.handler = proxy
	}
}

func WithIncludeQueryStats(includeQueryStats bool) Option {
	return func(r *routes) {
		r.includeQueryStats = includeQueryStats
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

func (r *routes) passthrough(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

type recordingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

// NewCustomResponseWriter creates a new instance of CustomResponseWriter
func newCustomResponseWriter(w http.ResponseWriter) *recordingResponseWriter {
	return &recordingResponseWriter{w, http.StatusOK, &bytes.Buffer{}}
}

// WriteHeader to capture status code
func (rw *recordingResponseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write to capture body
func (rw *recordingResponseWriter) Write(b []byte) (int, error) {
	rw.body.Write(b)                  // Write to buffer
	return rw.ResponseWriter.Write(b) // Write response to client
}

func (r *routes) parseQueryResponse(recw *recordingResponseWriter) *Response {
	if !r.includeQueryStats {
		return nil
	}

	var response Response
	if err := json.NewDecoder(recw.body).Decode(&response); err != nil {
		log.Printf("unable to decode response body: %v", err)
		return nil
	}

	if response.Status != "success" {
		log.Printf("query did not succeed: %v", response.Status)
		return nil
	}

	return &response
}

func getTimeParam(req *http.Request, param string) time.Time {
	if timeParam := req.FormValue(param); timeParam != "" {
		timeParamNormalized, err := time.Parse(time.RFC3339, timeParam)
		if err != nil {
			log.Printf("unable to parse time parameter: %v", err)
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

	recw := newCustomResponseWriter(w)
	r.handler.ServeHTTP(recw, req)

	if response := r.parseQueryResponse(recw); response != nil {
		query.TotalQueryableSamples = response.Data.Stats.Samples.TotalQueryableSamples
		query.PeakSamples = response.Data.Stats.Samples.PeakSamples
	}

	query.Duration = time.Since(start)
	query.StatusCode = recw.statusCode
	query.BodySize = recw.body.Len()

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

	recw := newCustomResponseWriter(w)
	r.handler.ServeHTTP(recw, req)

	if response := r.parseQueryResponse(recw); response != nil {
		query.TotalQueryableSamples = response.Data.Stats.Samples.TotalQueryableSamples
		query.PeakSamples = response.Data.Stats.Samples.PeakSamples
	}

	query.Duration = time.Since(start)
	query.StatusCode = recw.statusCode
	query.BodySize = recw.body.Len()

	r.queryIngester.Ingest(query)
}

func (r *routes) analytics(w http.ResponseWriter, req *http.Request) {
	query := req.FormValue("query")
	if query == "" {
		http.Error(w, "missing query parameter", http.StatusBadRequest)
		return
	}

	rows, err := r.dbProvider.Query(req.Context(), query)
	if err != nil {
		log.Printf("unable to execute query: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		log.Printf("unable to fetch columns: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var data []map[string]interface{}

	for rows.Next() {
		columnPointers := make([]interface{}, len(columns))
		columnValues := make([]interface{}, len(columns))
		for i := range columnValues {
			columnPointers[i] = &columnValues[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			log.Printf("unable to scan row: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		rowMap := make(map[string]interface{})
		for i, colName := range columns {
			var v interface{}
			switch columnValues[i].(type) {
			case []uint8:
				v = string(columnValues[i].([]uint8))
			case []string:
				v = columnValues[i].([]string)
			case nil:
				v = nil
			default:
				v = columnValues[i]
			}
			rowMap[colName] = v
		}

		data = append(data, rowMap)
	}

	if err := rows.Err(); err != nil {
		log.Printf("error iterating rows: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"columns": columns,
		"data":    data,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("unable to encode results to JSON: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
		slog.Error(err.Error())
		return nil
	}

	return uiHandler.ServeHTTP
}