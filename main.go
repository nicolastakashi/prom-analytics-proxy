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

	"github.com/oklog/run"

	_ "github.com/marcboeker/go-duckdb"
)

func main() {
	var (
		insecureListenAddress string
		upstream              string
		dbPath                string
		bufSize               int
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.StringVar(&insecureListenAddress, "insecure-listen-address", "", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	flagset.StringVar(&upstream, "upstream", "", "The URL of the upstream prometheus API.")
	flagset.StringVar(&dbPath, "db-path", "prom-analytics.db", "The path to the duckdb file.")
	flagset.IntVar(&bufSize, "buf-size", 100, "Buffer size for the insert channel.")
	flagset.Parse(os.Args[1:])

	upstreamURL, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("unable to parse upstream: %v", err)
	}
	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		log.Fatalf("Invalid scheme for upstream URL %q, only 'http' and 'https' are supported", upstream)
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

	// Register proxy HTTP Server
	{
		routes, err := newRoutes(upstreamURL, db, bufSize)
		if err != nil {
			log.Fatalf("unable to create routes: %v", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/", routes)
		l, err := net.Listen("tcp", insecureListenAddress)
		if err != nil {
			log.Fatalf("Failed to listen on address: %v", err)
		}
		srv := &http.Server{Handler: mux}

		g.Add(func() error {
			log.Printf("Listening insecurely on %v", l.Addr())
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				log.Printf("Server stopped with %v", err)
				return err
			}
			return nil
		}, func(error) {
			srv.Close()
		})
	}

	// Register Signal Handler
	{
		g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	}

	if err := g.Run(); err != nil {
		if !errors.As(err, &run.SignalError{}) {
			log.Printf("Server stopped with %v", err)
			os.Exit(1)
		}
		log.Print("Caught signal; exiting gracefully...")
	}

}
