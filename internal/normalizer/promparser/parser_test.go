package promparser_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/normalizer/promparser"
)

func TestParse_CountersAndGauges(t *testing.T) {
	input := []byte(`# HELP amf_n1_message_total Total N1 messages processed
# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 50042
amf_n1_message_total{msg_type="registration_reject"} 15
amf_n1_message_total{msg_type="deregistration_request"} 1200
# HELP amf_connected_ue Current number of connected UEs
# TYPE amf_connected_ue gauge
amf_connected_ue 1053
`)
	metrics, err := promparser.Parse(input)
	require.NoError(t, err)
	assert.Len(t, metrics, 4)

	reg := findMetric(t, metrics, "amf_n1_message_total", map[string]string{"msg_type": "registration_request"})
	assert.Equal(t, 50042.0, reg.Value)
	assert.Equal(t, "counter", reg.Type)

	ue := findMetric(t, metrics, "amf_connected_ue", nil)
	assert.Equal(t, 1053.0, ue.Value)
	assert.Equal(t, "gauge", ue.Type)
}

func TestParse_EmptyInput(t *testing.T) {
	metrics, err := promparser.Parse([]byte(""))
	require.NoError(t, err)
	assert.Empty(t, metrics)
}

func TestParse_NoTypeAnnotation(t *testing.T) {
	// Metrics without # TYPE should parse as "untyped".
	input := []byte("some_metric{label=\"value\"} 42.5\n")
	metrics, err := promparser.Parse(input)
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	assert.Equal(t, "untyped", metrics[0].Type)
	assert.Equal(t, 42.5, metrics[0].Value)
	assert.Equal(t, "some_metric", metrics[0].Name)
	assert.Equal(t, map[string]string{"label": "value"}, metrics[0].Labels)
}

func TestParse_Free5GCRealisticPayload(t *testing.T) {
	// Realistic free5GC AMF metrics payload — 6 metric lines across 3 families.
	input := []byte(`# HELP amf_n1_message_total Total N1 messages
# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
amf_n1_message_total{msg_type="registration_reject"} 3
amf_n1_message_total{msg_type="deregistration_request"} 50
# HELP amf_connected_ue Connected UE count
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# HELP amf_handover_total Handover attempts
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 200
amf_handover_total{result="success"} 195
`)
	metrics, err := promparser.Parse(input)
	require.NoError(t, err)
	assert.Len(t, metrics, 6, "should parse all 6 metric lines")

	// Spot-check a few values.
	ho := findMetric(t, metrics, "amf_handover_total", map[string]string{"result": "success"})
	assert.Equal(t, 195.0, ho.Value)
	assert.Equal(t, "counter", ho.Type)

	ue := findMetric(t, metrics, "amf_connected_ue", nil)
	assert.Equal(t, 950.0, ue.Value)
	assert.Equal(t, "gauge", ue.Type)
}

func TestParse_HistogramProducesCountAndSum(t *testing.T) {
	input := []byte(`# HELP http_request_duration_seconds Request latency
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.1"} 24054
http_request_duration_seconds_bucket{le="0.5"} 33444
http_request_duration_seconds_bucket{le="+Inf"} 144320
http_request_duration_seconds_sum 53423
http_request_duration_seconds_count 144320
`)
	metrics, err := promparser.Parse(input)
	require.NoError(t, err)

	sum := findMetric(t, metrics, "http_request_duration_seconds_sum", nil)
	assert.Equal(t, 53423.0, sum.Value)
	assert.Equal(t, "histogram", sum.Type)

	count := findMetric(t, metrics, "http_request_duration_seconds_count", nil)
	assert.Equal(t, 144320.0, count.Value)
}

func TestParse_SummaryProducesCountAndSum(t *testing.T) {
	input := []byte(`# HELP rpc_duration_seconds RPC latency
# TYPE rpc_duration_seconds summary
rpc_duration_seconds{quantile="0.5"} 4773
rpc_duration_seconds{quantile="0.9"} 9001
rpc_duration_seconds_sum 1.7560473e+07
rpc_duration_seconds_count 2693
`)
	metrics, err := promparser.Parse(input)
	require.NoError(t, err)

	sum := findMetric(t, metrics, "rpc_duration_seconds_sum", nil)
	assert.InDelta(t, 1.7560473e+07, sum.Value, 1.0)
	assert.Equal(t, "summary", sum.Type)

	count := findMetric(t, metrics, "rpc_duration_seconds_count", nil)
	assert.Equal(t, 2693.0, count.Value)
}

// findMetric locates a ParsedMetric by name and exact label match.
func findMetric(t *testing.T, metrics []promparser.ParsedMetric, name string, labels map[string]string) *promparser.ParsedMetric {
	t.Helper()
	for _, m := range metrics {
		if m.Name != name {
			continue
		}
		if labels == nil || (len(labels) == 0 && len(m.Labels) == 0) {
			return &m
		}
		match := true
		for k, v := range labels {
			if m.Labels[k] != v {
				match = false
				break
			}
		}
		if match && len(m.Labels) == len(labels) {
			return &m
		}
	}
	t.Fatalf("metric %s with labels %v not found in %d metrics", name, labels, len(metrics))
	return nil
}
