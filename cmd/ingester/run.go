package ingester

import (
	"context"
	"errors"
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
	"github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func RegisterFlags(fs *flag.FlagSet, configFile *string) {
	fs.StringVar(configFile, "config-file", "", "Path to the configuration file, it takes precedence over the command line flags.")
	fs.StringVar(&config.DefaultConfig.Ingester.Protocol, "ingester-protocol", string(config.ProtocolOTLP), "Metrics ingestion protocol (otlp)")
	fs.DurationVar(&config.DefaultConfig.Ingester.GracefulShutdownTimeout, "ingester-graceful-timeout", 30*time.Second, "Max time to wait for graceful shutdown")
	fs.DurationVar(&config.DefaultConfig.Ingester.DrainDelay, "ingester-drain-delay", 2*time.Second, "Delay after marking NOT_SERVING before shutdown begins")
	fs.StringVar(&config.DefaultConfig.Ingester.MetricsListenAddress, "ingester-metrics-listen-address", config.DefaultConfig.Ingester.MetricsListenAddress, "The HTTP address to expose Prometheus metrics")
	fs.StringVar(&config.DefaultConfig.Database.Provider, "database-provider", "", "The provider of database to use for retrieving query data. Supported values: postgresql, sqlite.")
	fs.BoolVar(&config.DefaultConfig.Ingester.DryRun, "ingester-dry-run", false, "When true, performs filtering analysis and records metrics but does not actually drop any data")
	fs.Func("ingester-allowed-jobs", "Comma-separated list of allowed jobs to ingest metrics from", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.AllowedJobs = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.AllowedJobs = out
		return nil
	})
	fs.Func("ingester-denied-jobs", "Comma-separated list of denied jobs to ingest metrics from", func(v string) error {
		if v == "" {
			config.DefaultConfig.Ingester.DeniedJobs = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		config.DefaultConfig.Ingester.DeniedJobs = out
		return nil
	})

	otlp.RegisterOTLPFlags(fs)
	db.RegisterPostGreSQLFlags(fs)
	db.RegisterSqliteFlags(fs)
	config.RegisterMemoryLimitFlags(fs)
}

func Run() error {
	var g run.Group
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbProvider, err := db.GetDbProvider(ctx, db.DatabaseProvider(config.DefaultConfig.Database.Provider))
	if err != nil {
		return fmt.Errorf("create db provider: %w", err)
	}

	defer func() {
		if err := dbProvider.Close(); err != nil {
			slog.ErrorContext(ctx, "ingester.db.close_error", "err", err)
		}
	}()

	var otlpIngester *internalIngester.OtlpIngester

	switch config.DefaultConfig.Ingester.Protocol {
	case string(config.ProtocolOTLP):
		otlpIngester, err := internalIngester.NewOtlpIngester(config.DefaultConfig, dbProvider)
		if err != nil {
			return fmt.Errorf("create otlp ingester: %w", err)
		}
		g.Add(func() error {
			return otlpIngester.Run(ctx)
		}, func(err error) {
			if err == nil || errors.Is(err, context.Canceled) || errors.As(err, &run.SignalError{}) {
				slog.InfoContext(ctx, "ingester.run.stopped")
			} else {
				slog.ErrorContext(ctx, "ingester.run.error", "err", err)
			}
			cancel()
		})
	default:
		return fmt.Errorf("unknown protocol: %s", config.DefaultConfig.Ingester.Protocol)
	}

	// Metrics and health HTTP server
	{
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())

		// Kubernetes-style liveness probe: returns 200 as long as the process is running.
		mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ok")); err != nil {
				slog.ErrorContext(r.Context(), "ingester.http.livez_write_error", "err", err)
			}
		})

		// Kubernetes-style readiness probe: relies on the OTLP ingester's gRPC health status.
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			if otlpIngester == nil || !otlpIngester.IsReady(r.Context()) {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ok")); err != nil {
				slog.ErrorContext(r.Context(), "ingester.http.readyz_write_error", "err", err)
			}
		})
		srv := &http.Server{
			Addr:         config.DefaultConfig.Ingester.MetricsListenAddress,
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		g.Add(func() error {
			slog.InfoContext(ctx, "ingester.metrics.exposing", "address", srv.Addr)
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
