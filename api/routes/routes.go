package routes

import (
	"encoding/json"
	"log"
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

func NewRoutes(upstream *url.URL, dbProvider db.Provider, queryIngester *ingester.QueryIngester) (*routes, error) {
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	r := &routes{
		upstream:      upstream,
		handler:       proxy,
		queryIngester: queryIngester,
		dbProvider:    dbProvider,
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(r.passthrough))
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
	bodySize   int
}

func (r *recordingResponseWriter) WriteHeader(status int) {
	r.statusCode = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recordingResponseWriter) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bodySize += n
	return n, err
}

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	queryParam := req.FormValue("query")
	timeParam := req.FormValue("time")

	var timeParamNormalized time.Time
	if timeParam == "" {
		timeParamNormalized = time.Now()
	} else {
		timeParamNormalized, _ = time.Parse(time.RFC3339, timeParam)
	}

	recw := &recordingResponseWriter{ResponseWriter: w}
	r.handler.ServeHTTP(recw, req)

	r.queryIngester.Ingest(db.Query{
		TS:         start,
		QueryParam: queryParam,
		TimeParam:  timeParamNormalized,
		Duration:   time.Since(start),
		StatusCode: recw.statusCode,
		BodySize:   recw.bodySize,
	})
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
