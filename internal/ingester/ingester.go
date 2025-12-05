package ingester

import (
	"github.com/nicolastakashi/prom-analytics-proxy/internal/ingester/otlp"
)

type (
	OtlpIngester = otlp.OtlpIngester
)

var (
	NewOtlpIngester = otlp.NewOtlpIngester
)
