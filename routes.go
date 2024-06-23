package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
)

type routes struct {
	upstream *url.URL
	handler  http.Handler
	mux      *http.ServeMux

	queryIngester *ingester.QueryIngester
}

func newRoutes(upstream *url.URL, queryIngester *ingester.QueryIngester) (*routes, error) {
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	r := &routes{
		upstream:      upstream,
		handler:       proxy,
		queryIngester: queryIngester,
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(r.passthrough))
	mux.Handle("/api/v1/query", http.HandlerFunc(r.query))
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

	r.queryIngester.Ingest(ingester.Query{
		TS:         start,
		QueryParam: queryParam,
		TimeParam:  timeParamNormalized,
		Duration:   time.Since(start),
		StatusCode: recw.statusCode,
		BodySize:   recw.bodySize,
	})
}
