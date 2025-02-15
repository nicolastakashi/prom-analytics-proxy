package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rs/cors"

	"github.com/nicolastakashi/prom-analytics-proxy/api/routes"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/ingester"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/log"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/tracing"
)

//go:embed ui/dist/*
var assets embed.FS

func loadEmbedFS(pathPrefix string) (fs.FS, error) {
	return fs.Sub(assets, filepath.Clean(pathPrefix))
}

func main() {
	var (
		configFile string
	)

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagset.Usage = func() {
		flagset.PrintDefaults()
	}
	log.RegisterFlags(flagset)

	flagset.StringVar(&configFile, "config-file", "", "Path to the configuration file, it takes precedence over the command line flags.")
	flagset.Uint64("metadata-limit", 0, "The maximum number of metric metadata entries to retrieve from the upstream prometheus API. (default 0 which means no limit)")
	flagset.Uint64("series-limit", 0, "The maximum number of series to retrieve from the upstream prometheus API. (default 0 which means no limit)")
	flagset.StringVar(&config.DefaultConfig.Server.InsecureListenAddress, "insecure-listen-address", ":9091", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	flagset.StringVar(&config.DefaultConfig.Upstream.URL, "upstream", "", "The URL of the upstream prometheus API.")
	flagset.BoolVar(&config.DefaultConfig.Upstream.IncludeQueryStats, "include-query-stats", false, "Request query stats from the upstream prometheus API.")
	flagset.IntVar(&config.DefaultConfig.Insert.BufferSize, "insert-buffer-size", 100, "Buffer size for the insert channel.")
	flagset.IntVar(&config.DefaultConfig.Insert.BatchSize, "insert-batch-size", 10, "Batch size for inserting queries into the database.")
	flagset.DurationVar(&config.DefaultConfig.Insert.Timeout, "insert-timeout", 1*time.Second, "Timeout to insert a query into the database.")
	flagset.DurationVar(&config.DefaultConfig.Insert.FlushInterval, "insert-flush-interval", 5*time.Second, "Flush interval for inserting queries into the database.")
	flagset.DurationVar(&config.DefaultConfig.Insert.GracePeriod, "insert-grace-period", 5*time.Second, "Grace period to insert pending queries after program shutdown.")
	flagset.StringVar(&config.DefaultConfig.Database.Provider, "database-provider", "", "The provider of database to use for storing query data. Supported values: clickhouse, postgresql, sqlite.")

	db.RegisterClickHouseFlags(flagset)
	db.RegisterPostGreSQLFlags(flagset)
	db.RegisterSqliteFlags(flagset)

	err := flagset.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing flags: %v", err)
		os.Exit(1)
	}

	logger, err := log.NewLogger()
	if err != nil {
		fmt.Printf("error creating logger: %v", err)
		os.Exit(1)
	}
	slog.SetDefault(logger)

	if configFile != "" {
		err := config.LoadConfig(configFile)
		if err != nil {
			slog.Error("unable to load config file", "err", err)
			os.Exit(1)
		}
	}

	if config.DefaultConfig.IsTracingEnabled() {
		tp, err := tracing.WithTracing(context.Background(), logger, configFile)
		if err != nil {
			slog.Error("unable to create tracer provider", "err", err)
			os.Exit(1)
		}

		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				slog.Info("Error shutting down tracer provider", "err", err)
			}
		}()
	}

	upstreamURL, err := url.Parse(config.DefaultConfig.Upstream.URL)
	if err != nil {
		slog.Error("unable to parse upstream", "err", err)
		os.Exit(1)
	}

	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		slog.Error(fmt.Sprintf("invalid scheme for upstream URL %q, only 'http' and 'https' are supported", config.DefaultConfig.Upstream.URL))
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	var g run.Group

	dbProvider, err := db.GetDbProvider(context.Background(), db.DatabaseProvider(config.DefaultConfig.Database.Provider))
	if err != nil {
		slog.Error("unable to create db provider", "err", err)
		os.Exit(1)
	}
	defer dbProvider.Close()

	queryIngester := ingester.NewQueryIngester(
		dbProvider,
		ingester.WithBufferSize(config.DefaultConfig.Insert.BufferSize),
		ingester.WithIngestTimeout(config.DefaultConfig.Insert.Timeout),
		ingester.WithShutdownGracePeriod(config.DefaultConfig.Insert.GracePeriod),
		ingester.WithBatchSize(config.DefaultConfig.Insert.BatchSize),
		ingester.WithBatchFlushInterval(config.DefaultConfig.Insert.FlushInterval),
	)

	// Run Ingester loop
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			queryIngester.Run(ctx)
			return nil
		}, func(err error) {
			cancel()
		})
	}

	// Register proxy HTTP Server
	{
		ctx, cancel := context.WithCancel(context.Background())

		uiFS, err := loadEmbedFS("ui/dist")
		if err != nil {
			slog.Error("unable to load embed FS", "err", err)
			os.Exit(1)
		}

		routes, err := routes.NewRoutes(
			routes.WithIncludeQueryStats(config.DefaultConfig.Upstream.IncludeQueryStats),
			routes.WithProxy(upstreamURL),
			routes.WithPromAPI(upstreamURL),
			routes.WithDBProvider(dbProvider),
			routes.WithQueryIngester(queryIngester),
			routes.WithHandlers(uiFS, reg, config.DefaultConfig.IsTracingEnabled()),
			routes.WithLimits(routes.LimitsConfig{
				SeriesLimit:   config.DefaultConfig.SeriesLimit,
				MetadataLimit: config.DefaultConfig.MetadataLimit,
			}),
		)

		if err != nil {
			slog.Error("unable to create routes", "err", err)
			os.Exit(1)
		}

		mux := http.NewServeMux()
		mux.Handle("/", routes)

		corsHandler := cors.New(cors.Options{
			AllowedOrigins:   []string{"*"}, // Allow all origins, adjust this for specific origins
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "Authorization"},
			AllowCredentials: true,
		}).Handler(mux)

		l, err := net.Listen("tcp", config.DefaultConfig.Server.InsecureListenAddress)
		if err != nil {
			slog.Error("failed to listen on address", "err", err)
			os.Exit(1)
		}

		srv := &http.Server{
			Handler: corsHandler,
		}

		g.Add(func() error {
			slog.Info("listening insecurely", "addr", l.Addr())
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				slog.Error("server stopped", "err", err)
				return err
			}
			return nil
		}, func(error) {
			slog.Info("stopping HTTP Server")
			cancel()
			if err := srv.Shutdown(ctx); err != nil {
				slog.Error("error shutting down server", "err", err)
			}
		})
	}

	// Register Signal Handler
	{
		g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	}

	if err := g.Run(); err != nil {
		if !errors.As(err, &run.SignalError{}) {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
		slog.Info("caught signal; exiting gracefully...")
	}
}
