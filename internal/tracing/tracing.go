package tracing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/thanos-io/thanos/pkg/tracing/otlp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"gopkg.in/yaml.v3"
)

type kitLogger struct {
	logger *slog.Logger
}

func newKitLogger(logger *slog.Logger) *kitLogger {
	return &kitLogger{logger: logger}
}

func (kl *kitLogger) Log(keyvals ...interface{}) error {
	kl.logger.Log(context.Background(), slog.LevelInfo, "", keyvals...)
	return nil
}

func WithTracing(ctx context.Context, logger *slog.Logger, configFile string) (*trace.TracerProvider, error) {
	f, err := yaml.Marshal(config.DefaultConfig.Tracing)
	if err != nil {
		return nil, fmt.Errorf("unable to read config file: %w", err)
	}

	tp, err := otlp.NewTracerProvider(ctx, newKitLogger(logger), f)
	otel.SetTracerProvider(tp)
	return tp, err
}
