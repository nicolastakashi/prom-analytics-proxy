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
