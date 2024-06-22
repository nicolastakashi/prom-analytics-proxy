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
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.StringVar(&insecureListenAddress, "insecure-listen-address", ":9090", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
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

	ctx, cancel := context.WithCancel(context.Background())

	if _, err = db.ExecContext(ctx, `
    CREATE TABLE IF NOT EXISTS queries (ts TIMESTAMP, fingerprint VARCHAR, query_param VARCHAR, time_param TIMESTAMP, label_matchers_list STRING, duration_ms BIGINT)
`); err != nil {
		log.Fatal(err)
	}

	queryIngester := ingester.NewQueryIngester(db, bufSize)

	var g run.Group

	g.Add(func() error {
		queryIngester.Run(ctx)
		return nil
	}, func(error) {
		cancel()
	})

	// Register proxy HTTP Server
	{
		routes, err := newRoutes(upstreamURL, *queryIngester)
		if err != nil {
			log.Fatalf("unable to create routes: %v", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/", routes)
		l, err := net.Listen("tcp", insecureListenAddress)
		if err != nil {
			log.Fatalf("Failed to listen on address: %v", err)
		}
		srv := &http.Server{
			Handler: mux,
			BaseContext: func(_ net.Listener) context.Context {
				return ctx
			},
		}

		g.Add(func() error {
			log.Printf("Listening insecurely on %v", l.Addr())
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				log.Printf("Server stopped with %v", err)
				return err
			}
			return nil
		}, func(error) {
			srv.Close()
			cancel()
		})
	}

	// Register Signal Handler
	{
		g.Add(run.SignalHandler(ctx, syscall.SIGINT, syscall.SIGTERM))
	}

	// Goroutine to enforce a timeout after receiving SIGTERM
	g.Add(func() error {
		<-ctx.Done()
		log.Printf("Received signal. Waiting up to 30 seconds to finish processing...")

		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer timeoutCancel()

		select {
		case <-timeoutCtx.Done():
			if timeoutCtx.Err() == context.DeadlineExceeded {
				log.Printf("Timeout reached. Forcing shutdown.")
				os.Exit(1)
			}
		case <-ctx.Done():
			// This case might never be selected since ctx.Done() has already been triggered.
		}
		return nil
	}, func(error) {
		cancel()
	})

	if err := g.Run(); err != nil {
		if !errors.As(err, &run.SignalError{}) {
			log.Printf("Server stopped with %v", err)
			os.Exit(1)
		}
		log.Print("Caught signal; exiting gracefully...")
	}

}
