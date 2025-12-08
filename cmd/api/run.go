package api

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
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
	"github.com/nicolastakashi/prom-analytics-proxy/internal/inventory"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/retention"
)

func RegisterFlags(fs *flag.FlagSet, configFile *string) {
	fs.StringVar(configFile, "config-file", "", "Path to the configuration file, it takes precedence over the command line flags.")
	fs.StringVar(&config.DefaultConfig.Database.Provider, "database-provider", "", "The provider of database to use for storing query data. Supported values: postgresql, sqlite.")
	fs.BoolVar(&config.DefaultConfig.Inventory.Enabled, "inventory-enabled", config.DefaultConfig.Inventory.Enabled, "Enable the metrics inventory syncer")

	fs.Uint64Var(&config.DefaultConfig.MetadataLimit, "metadata-limit", config.DefaultConfig.MetadataLimit, "The maximum number of metric metadata entries to retrieve from the upstream prometheus API. (default 0 which means no limit)")
	fs.Uint64Var(&config.DefaultConfig.SeriesLimit, "series-limit", config.DefaultConfig.SeriesLimit, "The maximum number of series to retrieve from the upstream prometheus API. (default 0 which means no limit)")
	fs.StringVar(&config.DefaultConfig.Server.InsecureListenAddress, "insecure-listen-address", ":9091", "The address the prom-analytics-proxy proxy HTTP server should listen on.")
	fs.StringVar(&config.DefaultConfig.Upstream.URL, "upstream", "", "The URL of the upstream prometheus API.")
	fs.BoolVar(&config.DefaultConfig.Upstream.IncludeQueryStats, "include-query-stats", true, "Request query stats from the upstream prometheus API.")
	fs.IntVar(&config.DefaultConfig.Insert.BufferSize, "insert-buffer-size", 100, "Buffer size for the insert channel.")
	fs.IntVar(&config.DefaultConfig.Insert.BatchSize, "insert-batch-size", 10, "Batch size for inserting queries into the database.")
	fs.DurationVar(&config.DefaultConfig.Insert.Timeout, "insert-timeout", 1*time.Second, "Timeout to insert a query into the database.")
	fs.DurationVar(&config.DefaultConfig.Insert.FlushInterval, "insert-flush-interval", 5*time.Second, "Flush interval for inserting queries into the database.")
	fs.DurationVar(&config.DefaultConfig.Insert.GracePeriod, "insert-grace-period", 5*time.Second, "Grace period to insert pending queries after program shutdown.")

	db.RegisterPostGreSQLFlags(fs)
	db.RegisterSqliteFlags(fs)
	config.RegisterInventoryFlags(fs)
	config.RegisterMemoryLimitFlags(fs)
	config.RegisterRetentionFlags(fs)
}

func Run(uiFS fs.FS) error {
	upstreamURL, err := url.Parse(config.DefaultConfig.Upstream.URL)
	if err != nil {
		slog.Error("unable to parse upstream", "err", err)
		return fmt.Errorf("parse upstream: %w", err)
	}

	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		slog.Error(fmt.Sprintf("invalid scheme for upstream URL %q, only 'http' and 'https' are supported", config.DefaultConfig.Upstream.URL))
		return fmt.Errorf("invalid upstream scheme: %s", upstreamURL.Scheme)
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
		return fmt.Errorf("create db provider: %w", err)
	}
	defer func() {
		if err := dbProvider.Close(); err != nil {
			slog.Error("error closing database provider", "err", err)
		}
	}()

	queryIngester := ingester.NewQueryIngester(
		reg,
		dbProvider,
		ingester.WithBufferSize(config.DefaultConfig.Insert.BufferSize),
		ingester.WithIngestTimeout(config.DefaultConfig.Insert.Timeout),
		ingester.WithShutdownGracePeriod(config.DefaultConfig.Insert.GracePeriod),
		ingester.WithBatchSize(config.DefaultConfig.Insert.BatchSize),
		ingester.WithBatchFlushInterval(config.DefaultConfig.Insert.FlushInterval),
	)

	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			queryIngester.Run(ctx)
			return nil
		}, func(err error) {
			cancel()
		})
	}

	{
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		routesHandler, err := routes.NewRoutes(
			routes.WithIncludeQueryStats(config.DefaultConfig.Upstream.IncludeQueryStats),
			routes.WithProxy(upstreamURL),
			routes.WithPromAPI(upstreamURL),
			routes.WithDBProvider(dbProvider),
			routes.WithQueryIngester(queryIngester),
			routes.WithHandlers(uiFS, reg, config.DefaultConfig.IsTracingEnabled()),
			routes.WithConfig(config.DefaultConfig),
			routes.WithLimits(routes.LimitsConfig{
				SeriesLimit:   config.DefaultConfig.SeriesLimit,
				MetadataLimit: config.DefaultConfig.MetadataLimit,
			}),
		)

		if err != nil {
			slog.Error("unable to create routes", "err", err)
			return fmt.Errorf("create routes: %w", err)
		}

		mux := http.NewServeMux()
		mux.Handle("/", routesHandler)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			mux.ServeHTTP(w, r)
		})

		corsHandler := cors.New(cors.Options{
			AllowedOrigins:   config.DefaultConfig.CORS.AllowedOrigins,
			AllowedMethods:   config.DefaultConfig.CORS.AllowedMethods,
			AllowedHeaders:   config.DefaultConfig.CORS.AllowedHeaders,
			AllowCredentials: config.DefaultConfig.CORS.AllowCredentials,
			MaxAge:           config.DefaultConfig.CORS.MaxAge,
		}).Handler(handler)

		l, err := net.Listen("tcp", config.DefaultConfig.Server.InsecureListenAddress)
		if err != nil {
			slog.Error("failed to listen on address", "err", err)
			return fmt.Errorf("listen: %w", err)
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

	if config.DefaultConfig.Inventory.Enabled {
		inv, err := inventory.NewSyncer(dbProvider, config.DefaultConfig.Upstream.URL, config.DefaultConfig, reg)
		if err != nil {
			slog.Error("unable to create inventory syncer", "err", err)
		} else {
			switch db.DatabaseProvider(config.DefaultConfig.Database.Provider) {
			case db.PostGreSQL:
				dbProvider.WithDB(func(d *sql.DB) {
					ctx, cancel := context.WithCancel(context.Background())
					g.Add(func() error {
						inventory.WithPGAdvisoryLeadership(ctx, d, 0x6d657472696373, inv.RunLeaderless)
						return nil
					}, func(err error) { cancel() })
				})
			default:
				ctx, cancel := context.WithCancel(context.Background())
				g.Add(func() error { inv.RunLeaderless(ctx); return nil }, func(err error) { cancel() })
			}
		}
	}

	if config.DefaultConfig.Retention.Enabled {
		retWorker, err := retention.NewWorker(dbProvider, config.DefaultConfig, reg)
		if err != nil {
			slog.Error("unable to create retention worker", "err", err)
		} else {
			switch db.DatabaseProvider(config.DefaultConfig.Database.Provider) {
			case db.PostGreSQL:
				dbProvider.WithDB(func(d *sql.DB) {
					ctx, cancel := context.WithCancel(context.Background())
					g.Add(func() error {
						inventory.WithPGAdvisoryLeadership(ctx, d, int64(0x726574656e74696f), retWorker.RunLeaderless)
						return nil
					}, func(err error) { cancel() })
				})
			default:
				ctx, cancel := context.WithCancel(context.Background())
				g.Add(func() error { retWorker.RunLeaderless(ctx); return nil }, func(err error) { cancel() })
			}
		}
	}

	{
		g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	}

	if err := g.Run(); err != nil {
		if !errors.As(err, &run.SignalError{}) {
			return err
		}
	}
	return nil
}
