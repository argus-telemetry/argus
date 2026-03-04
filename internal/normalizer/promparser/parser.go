// Package promparser converts Prometheus exposition format text into structured metrics.
// It wraps the canonical prometheus/common/expfmt library, extracting metric name, labels,
// value, and type from each metric family. Malformed lines are skipped — partial results
// beat total failure when scraping real NFs.
package promparser

import (
	"bytes"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// newTextParser creates a Prometheus text parser with legacy metric name validation.
// prometheus/common v0.67+ defaults to UTF8Validation which panics on zero-value;
// NewTextParser with LegacyValidation accepts all historically valid metric names.
func newTextParser() expfmt.TextParser {
	return expfmt.NewTextParser(model.LegacyValidation)
}

// ParsedMetric is a single metric extracted from Prometheus exposition format.
type ParsedMetric struct {
	Name   string            // metric name, e.g. "amf_n1_message_total"
	Labels map[string]string // label key-value pairs
	Value  float64
	Type   string // "counter" | "gauge" | "histogram" | "summary" | "untyped"
}

// Parse reads Prometheus exposition format data and returns all metrics.
// Skips malformed lines rather than failing — partial results are better than none.
// TYPE comments are used to determine metric types; metrics without a TYPE default to "untyped".
func Parse(data []byte) ([]ParsedMetric, error) {
	parser := newTextParser()
	families, err := parser.TextToMetricFamilies(bytes.NewReader(data))
	if err != nil {
		// TextToMetricFamilies returns partial results alongside errors for malformed input.
		// If we got zero families back, the input is truly unparseable.
		if len(families) == 0 {
			return nil, err
		}
		// Partial parse — continue with what we have.
	}

	var metrics []ParsedMetric
	for name, family := range families {
		metricType := typeString(family.GetType())
		for _, m := range family.GetMetric() {
			labels := extractLabels(m)

			switch family.GetType() {
			case dto.MetricType_COUNTER:
				metrics = append(metrics, ParsedMetric{
					Name:   name,
					Labels: labels,
					Value:  m.GetCounter().GetValue(),
					Type:   metricType,
				})

			case dto.MetricType_GAUGE:
				metrics = append(metrics, ParsedMetric{
					Name:   name,
					Labels: labels,
					Value:  m.GetGauge().GetValue(),
					Type:   metricType,
				})

			case dto.MetricType_HISTOGRAM:
				h := m.GetHistogram()
				metrics = append(metrics, ParsedMetric{
					Name:   name + "_sum",
					Labels: labels,
					Value:  h.GetSampleSum(),
					Type:   metricType,
				})
				metrics = append(metrics, ParsedMetric{
					Name:   name + "_count",
					Labels: labels,
					Value:  float64(h.GetSampleCount()),
					Type:   metricType,
				})

			case dto.MetricType_SUMMARY:
				s := m.GetSummary()
				metrics = append(metrics, ParsedMetric{
					Name:   name + "_sum",
					Labels: labels,
					Value:  s.GetSampleSum(),
					Type:   metricType,
				})
				metrics = append(metrics, ParsedMetric{
					Name:   name + "_count",
					Labels: labels,
					Value:  float64(s.GetSampleCount()),
					Type:   metricType,
				})

			case dto.MetricType_UNTYPED:
				metrics = append(metrics, ParsedMetric{
					Name:   name,
					Labels: labels,
					Value:  m.GetUntyped().GetValue(),
					Type:   metricType,
				})

			default:
				// Unknown type — treat as untyped to avoid dropping data.
				metrics = append(metrics, ParsedMetric{
					Name:   name,
					Labels: labels,
					Value:  m.GetUntyped().GetValue(),
					Type:   "untyped",
				})
			}
		}
	}

	return metrics, nil
}

// typeString maps dto.MetricType to the lowercase string representation used in ParsedMetric.Type.
func typeString(t dto.MetricType) string {
	switch t {
	case dto.MetricType_COUNTER:
		return "counter"
	case dto.MetricType_GAUGE:
		return "gauge"
	case dto.MetricType_HISTOGRAM:
		return "histogram"
	case dto.MetricType_SUMMARY:
		return "summary"
	default:
		return "untyped"
	}
}

// extractLabels pulls label key-value pairs from a prometheus metric proto.
func extractLabels(m *dto.Metric) map[string]string {
	pairs := m.GetLabel()
	if len(pairs) == 0 {
		return nil
	}
	labels := make(map[string]string, len(pairs))
	for _, lp := range pairs {
		labels[lp.GetName()] = lp.GetValue()
	}
	return labels
}
