package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/efficientgo/core/runutil"
	"github.com/oklog/run"
	"github.com/rs/cors"

	"github.com/MichaHoffmann/prom-analytics-proxy/api/routes"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/db"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
)

func main() {
	var (
		insecureListenAddress string
		upstream              string
		dbDir                 string
		bufSize               int
		gracePeriod           time.Duration
		ingestTimeout         time.Duration
		dbFlushPeriod         time.Duration
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.StringVar(&insecureListenAddress, "insecure-listen-address", ":9091", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	flagset.StringVar(&upstream, "upstream", "", "The URL of the upstream prometheus API.")
	flagset.IntVar(&bufSize, "buf-size", 100, "Buffer size for the insert channel.")
	flagset.DurationVar(&gracePeriod, "grace-period", 5*time.Second, "Grace period to ingest pending queries after program shutdown.")
	flagset.DurationVar(&ingestTimeout, "ingest-timeout", 100*time.Millisecond, "Timeout to ingest a query into duckdb.")
	flagset.StringVar(&dbDir, "db-dir", "data", "The directory to the local duckdb parquet files.")
	flagset.DurationVar(&dbFlushPeriod, "db-flush-period", 1*time.Minute, "Interval to cut new parquet file from the current duckdb state.")
	flagset.Parse(os.Args[1:])

	upstreamURL, err := url.Parse(upstream)

	if err != nil {
		log.Fatalf("unable to parse upstream: %v", err)
	}

	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		log.Fatalf("invalid scheme for upstream URL %q, only 'http' and 'https' are supported", upstream)
	}

	var g run.Group

	dbProvider, err := db.NewDBDuckProvider(context.Background(), dbDir)
	if err != nil {
		log.Fatalf("unable to connect to db: %v", err)
	}
	defer dbProvider.Close()

	// Run parquet file flush loop
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			return runutil.Repeat(dbFlushPeriod, ctx.Done(), func() error {
				if err := dbProvider.NextDB(); err != nil {
					return err
				}
				return nil
			})
		}, func(error) {
			log.Printf("stopping to flush parquet files")
			cancel()
		})
	}

	queryIngester := ingester.NewQueryIngester(dbProvider, bufSize, ingestTimeout, gracePeriod)

	// Run Ingester loop
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			queryIngester.Run(ctx)
			return nil
		}, func(error) {
			log.Printf("stopping query ingestion")
			cancel()
		})
	}

	// Register proxy HTTP Server
	{
		ctx, cancel := context.WithCancel(context.Background())

		routes, err := routes.NewRoutes(upstreamURL, dbProvider, queryIngester)
		if err != nil {
			log.Fatalf("unable to create routes: %v", err)
		}

		mux := http.NewServeMux()
		mux.Handle("/", routes)
		corsHandler := cors.New(cors.Options{
			AllowedOrigins:   []string{"*"}, // Allow all origins, adjust this for specific origins
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "Authorization"},
			AllowCredentials: true,
		}).Handler(mux)
		l, err := net.Listen("tcp", insecureListenAddress)
		if err != nil {
			log.Fatalf("failed to listen on address: %v", err)
		}
		srv := &http.Server{
			Handler: corsHandler,
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
