package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
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

	queryC chan queryColumns

	db *sql.DB
}

type queryColumns struct {
	ts         time.Time
	queryParam string
	timeParam  time.Time
	duration   time.Duration
}

func newRoutes(upstream *url.URL, db *sql.DB, bufSize int) (*routes, error) {
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	r := &routes{
		upstream: upstream,
		handler:  proxy,
		queryC:   make(chan queryColumns, bufSize),
		db:       db,
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(r.passthrough))
	mux.Handle("/api/v1/query", http.HandlerFunc(r.query))
	r.mux = mux

	go r.recordQueries()

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
	queryParam := req.FormValue("query")
	timeParam := req.FormValue("time")

	var timeParamNormalized time.Time
	if timeParam == "" {
		timeParamNormalized = time.Now()
	} else {
		timeParamNormalized, _ = time.Parse(time.RFC3339, timeParam)
	}

	r.handler.ServeHTTP(w, req)

	duration := time.Since(start)

	select {
	case r.queryC <- queryColumns{
		ts:         start,
		queryParam: queryParam,
		timeParam:  timeParamNormalized,
		duration:   duration,
	}:
	default:
	}
}

func (r *routes) recordQueries() {
	for q := range r.queryC {
		fingerprint := fingerprintFromQuery(q.queryParam)
		labelMatchers := labelMatchersFromQuery(q.queryParam)
		labelMatchersBlob, _ := json.Marshal(labelMatchers)
		if _, err := r.db.Exec("INSERT INTO queries VALUES (?, ?, ?, ?, ?, ?)", q.ts, fingerprint, q.queryParam, q.timeParam, labelMatchersBlob, q.duration.Milliseconds()); err != nil {
			log.Printf("unable to write to duckdb: %v", err)
		}
	}
}

func fingerprintFromQuery(query string) string {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return ""
	}
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			for _, m := range n.LabelMatchers {
				if m.Name != "__name__" {
					m.Value = "MASKED"
				}
			}
		}
		return nil
	})
	return fmt.Sprintf("%x", (md5.Sum([]byte(expr.String()))))
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
