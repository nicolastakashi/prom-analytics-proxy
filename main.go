package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/oklog/run"
	"github.com/rs/cors"

	"github.com/MichaHoffmann/prom-analytics-proxy/api/routes"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/db"
	"github.com/MichaHoffmann/prom-analytics-proxy/internal/ingester"
)

//go:embed ui/dist/*
var assets embed.FS

func loadEmbedFS(pathPrefix string) (fs.FS, error) {
	return fs.Sub(assets, filepath.Clean(pathPrefix))
}

func main() {
	var (
		insecureListenAddress    string
		upstream                 string
		includeQueryStats        bool
		bufSize                  int
		gracePeriod              time.Duration
		ingestTimeout            time.Duration
		clickHouseProviderConfig db.ClickHouseProviderConfig
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.StringVar(&insecureListenAddress, "insecure-listen-address", ":9091", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	flagset.StringVar(&upstream, "upstream", "", "The URL of the upstream prometheus API.")
	flagset.BoolVar(&includeQueryStats, "include-query-stats", false, "Request query stats from the upstream prometheus API.")
	flagset.IntVar(&bufSize, "buf-size", 100, "Buffer size for the insert channel.")
	flagset.DurationVar(&gracePeriod, "grace-period", 5*time.Second, "Grace period to ingest pending queries after program shutdown.")
	flagset.DurationVar(&ingestTimeout, "ingest-timeout", 100*time.Millisecond, "Timeout to ingest a query into duckdb.")

	flagset.DurationVar(&clickHouseProviderConfig.DiamTimeout, "clickhouse-dial-timeout", 5*time.Second, "Timeout to dial clickhouse.")
	flagset.StringVar(&clickHouseProviderConfig.Addr, "clickhouse-addr", "localhost:9000", "Address of the clickhouse server, comma separated for multiple servers.")
	err := flagset.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("unable to parse flags: %v", err)
	}

	upstreamURL, err := url.Parse(upstream)

	if err != nil {
		log.Fatalf("unable to parse upstream: %v", err)
	}

	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		log.Fatalf("invalid scheme for upstream URL %q, only 'http' and 'https' are supported", upstream)
	}

	var g run.Group

	dbProvider, err := db.NewClickHouseProvider(context.Background(), clickHouseProviderConfig)
	if err != nil {
		log.Fatalf("unable to connect to db: %v", err)
	}
	defer dbProvider.Close()

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

		uiFS, err := loadEmbedFS("ui/dist")
		if err != nil {
			slog.Error(err.Error())
		}

		routes, err := routes.NewRoutes(
			routes.WithIncludeQueryStats(includeQueryStats),
			routes.WithProxy(upstreamURL),
			routes.WithDBProvider(dbProvider),
			routes.WithQueryIngester(queryIngester),
			routes.WithUIFS(uiFS),
		)

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
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("error shutting down server: %v", err)
			}
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
