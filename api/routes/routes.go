package routes

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"

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

	r := &routes{
		upstream:      upstream,
		handler:       proxy,
		queryIngester: queryIngester,
		dbProvider:    dbProvider,
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/query", http.HandlerFunc(r.query))
	// mux.Handle("/", uiHandler(uiFS))
	// mux.Handle("/api/", http.HandlerFunc(r.passthrough))
	// mux.Handle("/api/v1/analytics", http.HandlerFunc(r.analytics))
	r.mux = mux

	return r, nil
}

func (r *routes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}
