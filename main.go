package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
	"github.com/oklog/run"

	_ "github.com/marcboeker/go-duckdb"
)

func main() {
	var (
		insecureListenAddress string
		upstream              string
		dbPath                string
		bufSize               int
		gracePeriod           time.Duration
		ingestTimeout         time.Duration
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.StringVar(&insecureListenAddress, "insecure-listen-address", ":9091", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	flagset.StringVar(&upstream, "upstream", "", "The URL of the upstream prometheus API.")
	flagset.StringVar(&dbPath, "db-path", "prom-analytics.db", "The path to the duckdb file.")
	flagset.IntVar(&bufSize, "buf-size", 100, "Buffer size for the insert channel.")
	flagset.DurationVar(&gracePeriod, "grace-period", 5*time.Second, "Grace period to ingest pending queries after program shutdown.")
	flagset.DurationVar(&ingestTimeout, "ingest-timeout", 100*time.Millisecond, "Timeout to ingest a query into duckdb.")
	flagset.Parse(os.Args[1:])

	upstreamURL, err := url.Parse(upstream)

	if err != nil {
		log.Fatalf("unable to parse upstream: %v", err)
	}

	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		log.Fatalf("invalid scheme for upstream URL %q, only 'http' and 'https' are supported", upstream)
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		log.Fatalf("unable to open DB: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("unable to ping DB: %v", err)
	}

	if _, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS queries (ts TIMESTAMP, fingerprint VARCHAR, query_param VARCHAR, time_param TIMESTAMP, label_matchers_list STRING, duration_ms BIGINT)
`); err != nil {
		log.Fatal(err)
	}

	var g run.Group

	queryIngester := ingester.NewQueryIngester(db, bufSize, ingestTimeout, gracePeriod)

	// Run Ingester loop
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			queryIngester.Run(ctx)
			return nil
		}, func(error) {
			log.Printf("stopping Query Ingester")
			cancel()
		})
	}

	// Register proxy HTTP Server
	{
		ctx, cancel := context.WithCancel(context.Background())

		routes, err := newRoutes(upstreamURL, queryIngester)
		if err != nil {
			log.Fatalf("unable to create routes: %v", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/", routes)
		l, err := net.Listen("tcp", insecureListenAddress)
		if err != nil {
			log.Fatalf("failed to listen on address: %v", err)
		}
		srv := &http.Server{
			Handler: mux,
		}

		g.Add(func() error {
			log.Printf("listening insecurely on %v", l.Addr())
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				log.Printf("server stopped with %v", err)
				return err
			}
			return nil
		}, func(error) {
			log.Printf("stopping HTTP Server")
			cancel()
			srv.Shutdown(ctx)
		})
	}

	// Register Signal Handler
	{
		g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	}

	if err := g.Run(); err != nil {
		if !errors.As(err, &run.SignalError{}) {
			log.Printf("server stopped with %v", err)
			os.Exit(1)
		}
		log.Print("caught signal; exiting gracefully...")
	}
}
