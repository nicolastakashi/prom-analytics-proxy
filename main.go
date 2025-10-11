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

	"github.com/oklog/run"

	"github.com/nicolastakashi/prom-analytics-proxy/cmd/api"
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
	apiFlagSet.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [command] [flags]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  api     Run the proxy server (default)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		apiFlagSet.PrintDefaults()
	}
	log.RegisterFlags(apiFlagSet)

	api.RegisterFlags(apiFlagSet, &configFile)

	args := os.Args[1:]
	if len(args) > 0 {
		first := args[0]
		if first == "-h" || first == "--help" || first == "help" {
			apiFlagSet.Usage()
			os.Exit(0)
		}
		if first == "api" {
			args = args[1:]
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
				apiFlagSet.Usage()
				os.Exit(0)
			}
		} else if !strings.HasPrefix(first, "-") {
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", first)
			fmt.Fprintln(os.Stderr, "Available command: api (default). You can run it explicitly as:")
			fmt.Fprintln(os.Stderr, "  prom-analytics-proxy api [flags]")
			fmt.Fprintln(os.Stderr, "")
			apiFlagSet.Usage()
			os.Exit(2)
		}
	}

	err := apiFlagSet.Parse(args)
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
}
