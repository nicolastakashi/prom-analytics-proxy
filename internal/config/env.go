package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const EnvPrefix = "PROM_ANALYTICS_PROXY_"

// EnvVarName maps a flag name to its environment variable,
// e.g. database-provider -> PROM_ANALYTICS_PROXY_DATABASE_PROVIDER.
func EnvVarName(flagName string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// AnnotateUsage adds the env var name to each flag's help text.
func AnnotateUsage(fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		f.Usage = fmt.Sprintf("%s (env %s)", f.Usage, EnvVarName(f.Name))
	})
}

// ApplyEnv sets flags from their env vars. Empty values are skipped, since
// Helm charts often render optional variables as FOO=.
func ApplyEnv(fs *flag.FlagSet) error {
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if err != nil {
			return
		}
		name := EnvVarName(f.Name)
		value := os.Getenv(name)
		if value == "" {
			return
		}
		if setErr := fs.Set(f.Name, value); setErr != nil {
			err = fmt.Errorf("invalid value %q for %s: %w", value, name, setErr)
		}
	})
	return err
}

// ParseFlags loads the configuration with the following precedence:
// defaults < config file < env vars < command line flags.
func ParseFlags(fs *flag.FlagSet, args []string, configFile *string) error {
	// Parse once to find the config file, which can also come from env.
	if err := ApplyEnv(fs); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configFile != "" {
		if err := LoadConfig(*configFile); err != nil {
			return err
		}
	}
	// Apply env vars and flags again so they override the config file.
	if err := ApplyEnv(fs); err != nil {
		return err
	}
	return fs.Parse(args)
}
