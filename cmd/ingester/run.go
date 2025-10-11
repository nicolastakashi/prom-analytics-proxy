package ingester

import (
	"flag"
	"fmt"
	"log/slog"
)

// RegisterFlags registers flags for the ingester command, including shared config/DB flags.
func RegisterFlags(fs *flag.FlagSet, configFile *string) {
	fs.StringVar(configFile, "config-file", "", "Path to the configuration file, it takes precedence over the command line flags.")
}

// Run executes the ingester. For now, it's a placeholder returning success.
func Run() error {
	slog.Info("ingester: starting (placeholder)")
	fmt.Println("ingester command is not implemented yet.")
	return nil
}
