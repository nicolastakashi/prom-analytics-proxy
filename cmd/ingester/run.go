package ingester

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	internalIngester "github.com/nicolastakashi/prom-analytics-proxy/internal/ingester"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func RegisterFlags(fs *flag.FlagSet, configFile *string) {
	fs.StringVar(configFile, "config-file", "", "Path to the configuration file, it takes precedence over the command line flags.")
	fs.StringVar(&config.DefaultConfig.Ingester.OTLP.ListenAddress, "otlp-listen-address", ":4317", "The address the metrics ingester should listen on.")
	fs.StringVar(&config.DefaultConfig.Ingester.Protocol, "ingester-protocol", string(config.ProtocolOTLP), "Metrics ingestion protocol (otlp)")
	fs.DurationVar(&config.DefaultConfig.Ingester.GracefulShutdownTimeout, "ingester-graceful-timeout", 30*time.Second, "Max time to wait for graceful shutdown")
	fs.DurationVar(&config.DefaultConfig.Ingester.DrainDelay, "ingester-drain-delay", 2*time.Second, "Delay after marking NOT_SERVING before shutdown begins")
	fs.StringVar(&config.DefaultConfig.Ingester.OTLP.DownstreamAddress, "otlp-downstream-address", "", "Optional downstream OTLP gRPC address to forward filtered metrics")
	fs.StringVar(&config.DefaultConfig.Ingester.MetricsListenAddress, "ingester-metrics-listen-address", config.DefaultConfig.Ingester.MetricsListenAddress, "The HTTP address to expose Prometheus metrics")
	fs.StringVar(&config.DefaultConfig.Database.Provider, "database-provider", "", "The provider of database to use for retrieving query data. Supported values: postgresql, sqlite.")
	fs.BoolVar(&config.DefaultConfig.Ingester.DryRun, "ingester-dry-run", false, "When true, performs filtering analysis and records metrics but does not actually drop any data")
	fs.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "otlp-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for OTLP server (bytes)")
	fs.IntVar(&config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "otlp-max-send-bytes", config.DefaultConfig.Ingester.OTLP.GRPCMaxSendMsgSizeBytes, "Max gRPC send message size for OTLP server (bytes)")
	fs.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "otlp-downstream-max-recv-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxRecvMsgSizeBytes, "Max gRPC receive message size for downstream OTLP client (bytes)")
	fs.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "otlp-downstream-max-send-bytes", config.DefaultConfig.Ingester.OTLP.DownstreamGRPCMaxSendMsgSizeBytes, "Max gRPC send message size for downstream OTLP client (bytes)")
	fs.IntVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxAttempts, "otlp-downstream-retry-max-attempts", config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxAttempts, "Downstream OTLP retry max attempts")
	fs.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryInitialBackoff, "otlp-downstream-retry-initial-backoff", config.DefaultConfig.Ingester.OTLP.DownstreamRetryInitialBackoff, "Downstream OTLP retry initial backoff (duration)")
	fs.DurationVar(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxBackoff, "otlp-downstream-retry-max-backoff", config.DefaultConfig.Ingester.OTLP.DownstreamRetryMaxBackoff, "Downstream OTLP retry max backoff (duration)")
	fs.Float64Var(&config.DefaultConfig.Ingester.OTLP.DownstreamRetryBackoffMultiplier, "otlp-downstream-retry-backoff-multiplier", config.DefaultConfig.Ingester.OTLP.DownstreamRetryBackoffMultiplier, "Downstream OTLP retry backoff multiplier")
	fs.Func("otlp-downstream-retry-codes", "Comma-separated gRPC status codes to retry (e.g., UNAVAILABLE,RESOURCE_EXHAUSTED)", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.OTLP.DownstreamRetryCodes = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.OTLP.DownstreamRetryCodes = out
		return nil
	})
	fs.Func("ingester-allowed-jobs", "Comma-separated list of allowed jobs to ingest metrics from", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.OTLP.AllowedJobs = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.OTLP.AllowedJobs = out
		return nil
	})
	fs.Func("ingester-denied-jobs", "Comma-separated list of denied jobs to ingest metrics from", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.OTLP.DeniedJobs = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.OTLP.DeniedJobs = out
		return nil
	})

	db.RegisterPostGreSQLFlags(fs)
	db.RegisterSqliteFlags(fs)
}

func Run() error {
	var g run.Group
	ctx := context.Background()

	dbProvider, err := db.GetDbProvider(ctx, db.DatabaseProvider(config.DefaultConfig.Database.Provider))
	if err != nil {
		return fmt.Errorf("create db provider: %w", err)
	}

	defer func() {
		if err := dbProvider.Close(); err != nil {
			slog.ErrorContext(ctx, "error closing database provider", "err", err)
		}
	}()

	switch config.DefaultConfig.Ingester.Protocol {
	case string(config.ProtocolOTLP):
		ctx, cancel := context.WithCancel(ctx)
		otlp := internalIngester.NewOtlpIngester(config.DefaultConfig, dbProvider)
		g.Add(func() error {
			return otlp.Run(ctx)
		}, func(err error) {
			slog.ErrorContext(ctx, "error running ingester", "err", err)
			cancel()
		})
	default:
		return fmt.Errorf("unknown protocol: %s", config.DefaultConfig.Ingester.Protocol)
	}

	// Metrics HTTP server
	{
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{
			Addr:         config.DefaultConfig.Ingester.MetricsListenAddress,
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		g.Add(func() error {
			slog.Info("ingester: exposing metrics", "address", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return err
			}
			return nil
		}, func(err error) {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(c)
		})
	}

	g.Add(run.SignalHandler(ctx, syscall.SIGINT, syscall.SIGTERM))

	return g.Run()
}
