package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/oklog/run"

	"github.com/nicolastakashi/prom-analytics-proxy/cmd/api"
	"github.com/nicolastakashi/prom-analytics-proxy/cmd/ingester"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
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

	apiFlagSet := flag.NewFlagSet("api", flag.ExitOnError)
	ingesterFlagSet := flag.NewFlagSet("ingester", flag.ExitOnError)

	printRootUsage := func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [command] [flags]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  api       Run the proxy server (default)")
		fmt.Fprintln(os.Stderr, "  ingester  Run metrics ingester (experimental)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (api):")
		apiFlagSet.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (metrics ingester):")
		ingesterFlagSet.PrintDefaults()
	}

	apiFlagSet.Usage = printRootUsage
	log.RegisterFlags(apiFlagSet)

	api.RegisterFlags(apiFlagSet, &configFile)

	ingesterFlagSet.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  %s ingester [flags]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Flags (metrics ingester):")
		ingesterFlagSet.PrintDefaults()
	}
	log.RegisterFlags(ingesterFlagSet)
	ingester.RegisterFlags(ingesterFlagSet, &configFile)

	args := os.Args[1:]
	cmd := "api"
	if len(args) > 0 {
		first := args[0]
		if first == "-h" || first == "--help" || first == "help" {
			printRootUsage()
			os.Exit(0)
		}
		if first == "api" || first == "ingester" {
			cmd = first
			args = args[1:]
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
				if cmd == "api" {
					apiFlagSet.Usage()
				} else {
					ingesterFlagSet.Usage()
				}
				os.Exit(0)
			}
		} else if !strings.HasPrefix(first, "-") {
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", first)
			printRootUsage()
			os.Exit(2)
		}
	}

	var err error
	switch cmd {
	case "api":
		err = apiFlagSet.Parse(args)
	case "ingester":
		err = ingesterFlagSet.Parse(args)
	}
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
		if err := config.LoadConfig(configFile); err != nil {
			slog.Error("unable to load config file", "err", err)
			os.Exit(1)
		}
	}
	logConfig(cmd)
	configureGoMemLimit(logger, config.DefaultConfig.MemoryLimit)
	var shutdown func()
	if config.DefaultConfig.IsTracingEnabled() {
		tp, err := tracing.WithTracing(context.Background(), logger, configFile)
		if err != nil {
			slog.Error("unable to create tracer provider", "err", err)
			os.Exit(1)
		}
		shutdown = func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				slog.Info("Error shutting down tracer provider", "err", err)
			}
		}
	} else {
		shutdown = func() {}
	}
	defer shutdown()

	switch cmd {
	case "api":
		uiFS, err := loadEmbedFS("ui/dist")
		if err != nil {
			slog.Error("unable to load embed FS", "err", err)
			os.Exit(1)
		}
		if err := api.Run(uiFS); err != nil {
			if !errors.As(err, &run.SignalError{}) {
				slog.Error("server stopped", "err", err)
				os.Exit(1)
			}
			slog.Info("caught signal; exiting gracefully...")
		}
	case "ingester":
		if err := ingester.Run(); err != nil {
			if !errors.As(err, &run.SignalError{}) {
				slog.Error("ingester stopped", "err", err)
				os.Exit(1)
			}
			slog.Info("ingester: caught signal; exiting gracefully...")
		}
	}
}

func logConfig(cmd string) {
	cfg := config.DefaultConfig.GetSanitizedConfig()

	slog.Info("Upstream configuration",
		"Upstream.URL", cfg.Upstream.URL,
		"Upstream.IncludeQueryStats", cfg.Upstream.IncludeQueryStats,
	)

	slog.Info("Server configuration",
		"Server.InsecureListenAddress", cfg.Server.InsecureListenAddress,
		"Server.PushMetricsUsageTimeout", cfg.Server.PushMetricsUsageTimeout,
	)

	slog.Info("Database configuration",
		"Database.Provider", cfg.Database.Provider,
		"Database.PostgreSQL.Addr", cfg.Database.PostgreSQL.Addr,
		"Database.PostgreSQL.Port", cfg.Database.PostgreSQL.Port,
		"Database.PostgreSQL.Database", cfg.Database.PostgreSQL.Database,
		"Database.PostgreSQL.User", cfg.Database.PostgreSQL.User,
		"Database.PostgreSQL.SSLMode", cfg.Database.PostgreSQL.SSLMode,
		"Database.PostgreSQL.MaxOpenConns", cfg.Database.PostgreSQL.MaxOpenConns,
		"Database.PostgreSQL.MaxIdleConns", cfg.Database.PostgreSQL.MaxIdleConns,
		"Database.PostgreSQL.DialTimeout", cfg.Database.PostgreSQL.DialTimeout,
		"Database.PostgreSQL.ConnMaxLifetime", cfg.Database.PostgreSQL.ConnMaxLifetime,
		"Database.PostgreSQL.ConnMaxIdleTime", cfg.Database.PostgreSQL.ConnMaxIdleTime,
		"Database.SQLite.DatabasePath", cfg.Database.SQLite.DatabasePath,
	)

	slog.Info("Insert configuration",
		"Insert.BatchSize", cfg.Insert.BatchSize,
		"Insert.BufferSize", cfg.Insert.BufferSize,
		"Insert.FlushInterval", cfg.Insert.FlushInterval,
		"Insert.GracePeriod", cfg.Insert.GracePeriod,
		"Insert.Timeout", cfg.Insert.Timeout,
	)

	slog.Info("Memory limit configuration",
		"MemoryLimit.Enabled", cfg.MemoryLimit.Enabled,
		"MemoryLimit.Ratio", cfg.MemoryLimit.Ratio,
		"MemoryLimit.RefreshInterval", cfg.MemoryLimit.RefreshInterval,
	)

	slog.Info("Limits configuration",
		"MetadataLimit", cfg.MetadataLimit,
		"SeriesLimit", cfg.SeriesLimit,
	)

	slog.Info("CORS configuration",
		"CORS.AllowedOrigins", cfg.CORS.AllowedOrigins,
		"CORS.AllowedMethods", cfg.CORS.AllowedMethods,
		"CORS.AllowedHeaders", cfg.CORS.AllowedHeaders,
		"CORS.AllowCredentials", cfg.CORS.AllowCredentials,
		"CORS.MaxAge", cfg.CORS.MaxAge,
	)

	slog.Info("Inventory configuration",
		"Inventory.Enabled", cfg.Inventory.Enabled,
		"Inventory.MetadataSyncEnabled", cfg.Inventory.MetadataSyncEnabled,
		"Inventory.MetadataMetricsNameOnly", cfg.Inventory.MetadataMetricsNameOnly,
		"Inventory.SyncInterval", cfg.Inventory.SyncInterval,
		"Inventory.TimeWindow", cfg.Inventory.TimeWindow,
		"Inventory.RunTimeout", cfg.Inventory.RunTimeout,
		"Inventory.MetadataStepTimeout", cfg.Inventory.MetadataStepTimeout,
		"Inventory.SummaryStepTimeout", cfg.Inventory.SummaryStepTimeout,
		"Inventory.JobSyncEnabled", cfg.Inventory.JobSyncEnabled,
		"Inventory.JobIndexLabelTimeout", cfg.Inventory.JobIndexLabelTimeout,
		"Inventory.JobIndexPerJobTimeout", cfg.Inventory.JobIndexPerJobTimeout,
		"Inventory.JobIndexWorkers", cfg.Inventory.JobIndexWorkers,
	)

	slog.Info("Query processing configuration",
		"QueryProcessing.ExtractHTTPHeaders", cfg.QueryProcessing.ExtractHTTPHeaders,
	)

	slog.Info("Retention configuration",
		"Retention.Enabled", cfg.Retention.Enabled,
		"Retention.Interval", cfg.Retention.Interval,
		"Retention.RunTimeout", cfg.Retention.RunTimeout,
		"Retention.QueriesMaxAge", cfg.Retention.QueriesMaxAge,
	)

	if cmd == "ingester" {
		slog.Info("Ingester configuration",
			"Ingester.Protocol", cfg.Ingester.Protocol,
			"Ingester.MetricsListenAddress", cfg.Ingester.MetricsListenAddress,
			"Ingester.DryRun", cfg.Ingester.DryRun,
			"Ingester.GracefulShutdownTimeout", cfg.Ingester.GracefulShutdownTimeout,
			"Ingester.DrainDelay", cfg.Ingester.DrainDelay,
			"Ingester.AllowedJobs", cfg.Ingester.AllowedJobs,
			"Ingester.DeniedJobs", cfg.Ingester.DeniedJobs,
		)

		slog.Info("Ingester OTLP configuration",
			"Ingester.OTLP.ListenAddress", cfg.Ingester.OTLP.ListenAddress,
			"Ingester.OTLP.DownstreamAddress", cfg.Ingester.OTLP.DownstreamAddress,
			"Ingester.OTLP.GRPCMaxRecvMsgSizeBytes", cfg.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes,
			"Ingester.OTLP.GRPCMaxSendMsgSizeBytes", cfg.Ingester.OTLP.GRPCMaxSendMsgSizeBytes,
			"Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes", cfg.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes,
			"Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes", cfg.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes,
			"Ingester.OTLP.DownstreamRetryMaxAttempts", cfg.Ingester.OTLP.DownstreamRetryMaxAttempts,
			"Ingester.OTLP.DownstreamRetryInitialBackoff", cfg.Ingester.OTLP.DownstreamRetryInitialBackoff,
			"Ingester.OTLP.DownstreamRetryMaxBackoff", cfg.Ingester.OTLP.DownstreamRetryMaxBackoff,
			"Ingester.OTLP.DownstreamRetryBackoffMultiplier", cfg.Ingester.OTLP.DownstreamRetryBackoffMultiplier,
			"Ingester.OTLP.DownstreamRetryCodes", cfg.Ingester.OTLP.DownstreamRetryCodes,
			"Ingester.OTLP.BalancerName", cfg.Ingester.OTLP.BalancerName,
			"Ingester.OTLP.DownstreamConnectMinTimeout", cfg.Ingester.OTLP.DownstreamConnectMinTimeout,
			"Ingester.OTLP.DownstreamConnectBaseDelay", cfg.Ingester.OTLP.DownstreamConnectBaseDelay,
			"Ingester.OTLP.DownstreamConnectMaxDelay", cfg.Ingester.OTLP.DownstreamConnectMaxDelay,
			"Ingester.OTLP.DownstreamConnectBackoffMultiplier", cfg.Ingester.OTLP.DownstreamConnectBackoffMultiplier,
			"Ingester.OTLP.LookupChunkSize", cfg.Ingester.OTLP.LookupChunkSize,
		)

		slog.Info("Ingester Redis configuration",
			"Ingester.Redis.Enabled", cfg.Ingester.Redis.Enabled,
			"Ingester.Redis.Addr", cfg.Ingester.Redis.Addr,
			"Ingester.Redis.Username", cfg.Ingester.Redis.Username,
			"Ingester.Redis.DB", cfg.Ingester.Redis.DB,
			"Ingester.Redis.UsedTTL", cfg.Ingester.Redis.UsedTTL,
			"Ingester.Redis.UnusedTTL", cfg.Ingester.Redis.UnusedTTL,
			"Ingester.Redis.UsedOnly", cfg.Ingester.Redis.UsedOnly,
			"Ingester.Redis.OperationTimeout", cfg.Ingester.Redis.OperationTimeout,
			"Ingester.Redis.DialTimeout", cfg.Ingester.Redis.DialTimeout,
			"Ingester.Redis.ConnWriteTimeout", cfg.Ingester.Redis.ConnWriteTimeout,
			"Ingester.Redis.BatchSize", cfg.Ingester.Redis.BatchSize,
		)

		slog.Info("Ingester catalog sync configuration",
			"Ingester.CatalogSync.Enabled", cfg.Ingester.CatalogSync.Enabled,
			"Ingester.CatalogSync.FlushInterval", cfg.Ingester.CatalogSync.FlushInterval,
			"Ingester.CatalogSync.BufferSize", cfg.Ingester.CatalogSync.BufferSize,
			"Ingester.CatalogSync.SeenTTL", cfg.Ingester.CatalogSync.SeenTTL,
		)
	}
}

func configureGoMemLimit(logger *slog.Logger, cfg config.MemoryLimitConfig) {
	if cfg.Enabled {
		ratio := cfg.Ratio
		if ratio <= 0 || ratio > 1 {
			logger.Warn("memory limit ratio out of bounds; falling back to default", "configured_ratio", cfg.Ratio, "default_ratio", config.DefaultMemoryLimitRatio)
			ratio = config.DefaultMemoryLimitRatio
		}

		opts := []memlimit.Option{
			memlimit.WithRatio(ratio),
			memlimit.WithLogger(logger),
			memlimit.WithProvider(memlimit.ApplyFallback(memlimit.FromCgroup, memlimit.FromSystem)),
		}
		if cfg.RefreshInterval > 0 {
			opts = append(opts, memlimit.WithRefreshInterval(cfg.RefreshInterval))
		}

		limit, err := memlimit.SetGoMemLimitWithOpts(opts...)
		if err != nil {
			logger.Error("failed to configure automatic Go memory limit", "err", err)
			return
		}
		logger.Info("configured automatic Go memory limit", "ratio", ratio, "refresh_interval", cfg.RefreshInterval, "limit_bytes", limit)
		return
	}

	logger.Info("automatic Go memory limit configuration disabled")
}
