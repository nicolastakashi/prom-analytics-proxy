package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/MichaHoffmann/prom-analytics-proxy/internal/db"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
)

type routes struct {
	upstream *url.URL
	handler  http.Handler
	mux      *http.ServeMux

	queryIngester *ingester.QueryIngester
	dbProvider    db.Provider
}

func NewRoutes(upstream *url.URL, dbProvider db.Provider, queryIngester *ingester.QueryIngester, uiFS fs.FS) (*routes, error) {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = upstream.Host // Set the Host header to the target host
	}

	r := &routes{
		upstream:      upstream,
		handler:       proxy,
		queryIngester: queryIngester,
		dbProvider:    dbProvider,
	}

	mux := http.NewServeMux()
	mux.Handle("/", uiHandler(uiFS))
	mux.Handle("/api/", http.HandlerFunc(r.passthrough))
	mux.Handle("/api/v1/query", http.HandlerFunc(r.query))
	mux.Handle("/api/v1/analytics", http.HandlerFunc(r.analytics))
	r.mux = mux

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

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	recw := newCustomResponseWriter(w)
	r.handler.ServeHTTP(recw, req)

	r.queryIngester.Ingest(db.Query{
		TS:         start,
		QueryParam: req.FormValue("query"),
		TimeParam:  getTimeParam(req),
		Duration:   time.Since(start),
		StatusCode: recw.statusCode,
		BodySize:   recw.body.Len(),
	})
}

func getTimeParam(req *http.Request) time.Time {
	if timeParam := req.FormValue("time"); timeParam != "" {
		timeParamNormalized, _ := time.Parse(time.RFC3339, timeParam)
		return timeParamNormalized
	}
	return time.Now()
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

func uiHandler(uiFS fs.FS) http.HandlerFunc {
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
