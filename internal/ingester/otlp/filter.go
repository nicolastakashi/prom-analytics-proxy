package otlp

import (
	"strings"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

type FilterConfig struct {
	DropMetricNames    map[string]struct{}
	DropMetricPrefixes []string

	MetricKeep func(ctx FilterContext) bool
}

type FilterContext struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Metric   *metricspb.Metric
}

type AttrView []*commonpb.KeyValue

func (a AttrView) Get(key string) string {
	for _, kv := range a {
		if kv.Key == key && kv.GetValue() != nil {
			switch v := kv.Value.Value.(type) {
			case *commonpb.AnyValue_StringValue:
				return v.StringValue
			}
		}
	}
	return ""
}

func (c FilterContext) ResourceAttr(key string) string {
	if c.Resource == nil {
		return ""
	}
	return AttrView(c.Resource.Attributes).Get(key)
}

func FilterExport(req *colmetricspb.ExportMetricsServiceRequest, cfg FilterConfig) (dropped int) {
	rms := req.ResourceMetrics[:0]
	for _, rm := range req.ResourceMetrics {
		newRM, d1 := filterResourceMetrics(rm, cfg)
		dropped += d1
		if newRM != nil {
			rms = append(rms, newRM)
		}
	}
	req.ResourceMetrics = rms
	return dropped
}

func filterResourceMetrics(rm *metricspb.ResourceMetrics, cfg FilterConfig) (*metricspb.ResourceMetrics, int) {
	var dropped int
	newSMS := rm.ScopeMetrics[:0]
	for _, sm := range rm.ScopeMetrics {
		newSM, d1 := filterScopeMetrics(rm.Resource, sm, cfg)
		dropped += d1
		if newSM != nil {
			newSMS = append(newSMS, newSM)
		}
	}
	if len(newSMS) == 0 {
		return nil, dropped
	}
	rm.ScopeMetrics = newSMS
	return rm, dropped
}

func filterScopeMetrics(res *resourcepb.Resource, sm *metricspb.ScopeMetrics, cfg FilterConfig) (*metricspb.ScopeMetrics, int) {
	var dropped int
	newMetrics := sm.Metrics[:0]
	for _, m := range sm.Metrics {
		ctx := FilterContext{Resource: res, Scope: sm.Scope, Metric: m}

		if shouldDropMetric(ctx, cfg) {
			dropped += countMetricDatapoints(m)
			continue
		}

		if metricIsEmpty(m) {
			continue
		}
		newMetrics = append(newMetrics, m)
	}
	if len(newMetrics) == 0 {
		return nil, dropped
	}
	sm.Metrics = newMetrics
	return sm, dropped
}

func shouldDropMetric(ctx FilterContext, cfg FilterConfig) bool {
	name := ctx.Metric.GetName()
	if cfg.DropMetricNames != nil {
		if _, ok := cfg.DropMetricNames[name]; ok {
			return true
		}
	}
	for _, p := range cfg.DropMetricPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	if cfg.MetricKeep != nil && !cfg.MetricKeep(ctx) {
		return true
	}
	return false
}

// Datapoint-level filtering intentionally removed for simplicity.

func metricIsEmpty(m *metricspb.Metric) bool {
	switch dt := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return len(dt.Gauge.DataPoints) == 0
	case *metricspb.Metric_Sum:
		return len(dt.Sum.DataPoints) == 0
	case *metricspb.Metric_Histogram:
		return len(dt.Histogram.DataPoints) == 0
	case *metricspb.Metric_ExponentialHistogram:
		return len(dt.ExponentialHistogram.DataPoints) == 0
	case *metricspb.Metric_Summary:
		return len(dt.Summary.DataPoints) == 0
	default:
		return false
	}
}

func countMetricDatapoints(m *metricspb.Metric) (n int) {
	switch dt := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return len(dt.Gauge.DataPoints)
	case *metricspb.Metric_Sum:
		return len(dt.Sum.DataPoints)
	case *metricspb.Metric_Histogram:
		return len(dt.Histogram.DataPoints)
	case *metricspb.Metric_ExponentialHistogram:
		return len(dt.ExponentialHistogram.DataPoints)
	case *metricspb.Metric_Summary:
		return len(dt.Summary.DataPoints)
	default:
		return 0
	}
}
