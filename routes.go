package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

type routes struct {
	upstream *url.URL
	handler  http.Handler
	mux      *http.ServeMux

	db *sql.DB
}

func newRoutes(upstream *url.URL, db *sql.DB) (*routes, error) {
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	r := &routes{
		upstream: upstream,
		handler:  proxy,

		db: db,
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

func (r *routes) query(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	query_param := req.FormValue("query")
	time_param := req.FormValue("time")

	var time_param_normalized time.Time
	if time_param == "" {
		time_param_normalized = time.Now()
	} else {
		time_param_normalized, _ = time.Parse(time.RFC3339, time_param)
	}

	r.handler.ServeHTTP(w, req)

	duration := time.Since(start).Milliseconds()
	labelMatchers := labelMatchersFromQuery(query_param)
	labelMatchersBlob, _ := json.Marshal(labelMatchers)

	if _, err := r.db.Exec("INSERT INTO queries VALUES (?, ?, ?, ?, ?)", start, query_param, time_param_normalized, string(labelMatchersBlob), duration); err != nil {
		log.Printf("unable to write to duckdb: %v", err)
	}
}

func labelMatchersFromQuery(query string) []map[string]string {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return nil
	}
	res := make([]map[string]string, 0)
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			v := make(map[string]string, 0)
			for _, m := range n.LabelMatchers {
				v[m.Name] = m.Value
			}
			res = append(res, v)
		}
		return nil
	})
	return res
}
