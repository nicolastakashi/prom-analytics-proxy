package otlp

import (
	"strings"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	metricsv1pb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// prometheusMetricName normalizes an OTLP metric name to the name users query in
// Prometheus after OTLP translation.
func prometheusMetricName(m *metricsv1pb.Metric) string {
	if m == nil {
		return ""
	}

	name := m.GetName()
	if name == "" {
		return ""
	}

	sum := m.GetSum()
	if sum != nil && sum.IsMonotonic && !strings.HasSuffix(name, "_total") {
		return name + "_total"
	}

	return name
}

func prometheusCatalogItems(m *metricsv1pb.Metric) []db.MetricCatalogItem {
	name := prometheusMetricName(m)
	if name == "" {
		return nil
	}

	base := db.MetricCatalogItem{
		Name: name,
		Type: otlpTypeToPrometheus(m),
		Help: m.GetDescription(),
		Unit: m.GetUnit(),
	}

	items := []db.MetricCatalogItem{base}

	if m.GetHistogram() != nil || m.GetExponentialHistogram() != nil {
		items = append(items,
			db.MetricCatalogItem{Name: name + "_bucket", Type: "histogram", Help: m.GetDescription(), Unit: m.GetUnit()},
			db.MetricCatalogItem{Name: name + "_sum", Type: "histogram", Help: m.GetDescription(), Unit: m.GetUnit()},
			db.MetricCatalogItem{Name: name + "_count", Type: "histogram", Help: m.GetDescription(), Unit: ""},
		)
	}

	if m.GetSummary() != nil {
		items = append(items,
			db.MetricCatalogItem{Name: name + "_sum", Type: "summary", Help: m.GetDescription(), Unit: m.GetUnit()},
			db.MetricCatalogItem{Name: name + "_count", Type: "summary", Help: m.GetDescription(), Unit: ""},
		)
	}

	return items
}

func prometheusCatalogNames(metrics []*metricsv1pb.Metric) []string {
	names := make([]string, 0, len(metrics))
	for _, m := range metrics {
		for _, item := range prometheusCatalogItems(m) {
			names = append(names, item.Name)
		}
	}
	return names
}
