package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/argus-5g/argus/internal/normalizer"
)

func sampleRecords() []normalizer.NormalizedRecord {
	return []normalizer.NormalizedRecord{
		{
			Namespace: "argus.5g.gnb",
			KPIName:   "prb.utilization_ratio",
			Value:     0.72,
			Unit:      "ratio",
			Timestamp: time.Now(),
			SpecRef:   "3GPP TS 28.552 §5.1.1.3",
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "oai",
				NFType:       "gNB",
				NFInstanceID: "gnb-01",
				PLMNID:       "310-260",
				DataCenter:   "dc-us-east-1",
			},
			SchemaVersion: "v1",
		},
		{
			Namespace: "argus.5g.amf",
			KPIName:   "registration.success_rate",
			Value:     0.997,
			Unit:      "ratio",
			Timestamp: time.Now(),
			SpecRef:   "3GPP TS 28.552 §5.1.1.1",
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "free5gc",
				NFType:       "AMF",
				NFInstanceID: "amf-01",
				PLMNID:       "310-260",
				DataCenter:   "dc-us-east-1",
				SliceID:      &normalizer.SliceID{SST: 1, SD: "000001"},
			},
			SchemaVersion: "v1",
		},
	}
}

// collectMetrics writes records and triggers a manual collection, returning all
// collected Metrics across all scopes.
func collectMetrics(t *testing.T, records []normalizer.NormalizedRecord) []metricdata.Metrics {
	t.Helper()

	w, reader, err := newWriterWithReader(Config{BatchSize: 100})
	require.NoError(t, err)
	defer w.Close()

	ctx := context.Background()
	require.NoError(t, w.Write(ctx, records))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	var all []metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		all = append(all, sm.Metrics...)
	}
	return all
}

// findMetric returns the first Metrics entry matching the given name, or fails the test.
func findMetric(t *testing.T, metrics []metricdata.Metrics, name string) metricdata.Metrics {
	t.Helper()
	for _, m := range metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("metric %q not found in collected metrics", name)
	return metricdata.Metrics{}
}

func TestWriter_Name(t *testing.T) {
	w, _, err := newWriterWithReader(Config{})
	require.NoError(t, err)
	defer w.Close()
	assert.Equal(t, "otlp", w.Name())
}

func TestWriter_BatchSize(t *testing.T) {
	w, _, err := newWriterWithReader(Config{BatchSize: 100})
	require.NoError(t, err)
	defer w.Close()
	assert.Equal(t, 100, w.BatchSize())
}

func TestWriter_Write_MetricNames(t *testing.T) {
	metrics := collectMetrics(t, sampleRecords())

	names := make(map[string]bool)
	for _, m := range metrics {
		names[m.Name] = true
	}
	assert.True(t, names["argus.5g.gnb.prb.utilization_ratio"], "expected gNB PRB metric")
	assert.True(t, names["argus.5g.amf.registration.success_rate"], "expected AMF registration metric")
}

func TestWriter_Write_MetricValues(t *testing.T) {
	metrics := collectMetrics(t, sampleRecords())

	gnb := findMetric(t, metrics, "argus.5g.gnb.prb.utilization_ratio")
	gauge, ok := gnb.Data.(metricdata.Gauge[float64])
	require.True(t, ok, "expected Gauge[float64] data type")
	require.Len(t, gauge.DataPoints, 1)
	assert.InDelta(t, 0.72, gauge.DataPoints[0].Value, 1e-9)

	amf := findMetric(t, metrics, "argus.5g.amf.registration.success_rate")
	gauge, ok = amf.Data.(metricdata.Gauge[float64])
	require.True(t, ok)
	require.Len(t, gauge.DataPoints, 1)
	assert.InDelta(t, 0.997, gauge.DataPoints[0].Value, 1e-9)
}

func TestWriter_Write_ResourceAttributes(t *testing.T) {
	metrics := collectMetrics(t, sampleRecords())

	gnb := findMetric(t, metrics, "argus.5g.gnb.prb.utilization_ratio")
	gauge, ok := gnb.Data.(metricdata.Gauge[float64])
	require.True(t, ok)
	require.Len(t, gauge.DataPoints, 1)

	attrs := gauge.DataPoints[0].Attributes

	val, found := attrs.Value(attribute.Key("nf.type"))
	assert.True(t, found)
	assert.Equal(t, "gNB", val.AsString())

	val, found = attrs.Value(attribute.Key("nf.vendor"))
	assert.True(t, found)
	assert.Equal(t, "oai", val.AsString())

	val, found = attrs.Value(attribute.Key("nf.instance_id"))
	assert.True(t, found)
	assert.Equal(t, "gnb-01", val.AsString())

	val, found = attrs.Value(attribute.Key("network.plmn_id"))
	assert.True(t, found)
	assert.Equal(t, "310-260", val.AsString())
}

func TestWriter_Write_Unit(t *testing.T) {
	metrics := collectMetrics(t, sampleRecords())

	gnb := findMetric(t, metrics, "argus.5g.gnb.prb.utilization_ratio")
	assert.Equal(t, "ratio", gnb.Unit)
}

func TestWriter_Write_SliceAttributes(t *testing.T) {
	metrics := collectMetrics(t, sampleRecords())

	amf := findMetric(t, metrics, "argus.5g.amf.registration.success_rate")
	gauge, ok := amf.Data.(metricdata.Gauge[float64])
	require.True(t, ok)
	require.Len(t, gauge.DataPoints, 1)

	attrs := gauge.DataPoints[0].Attributes

	val, found := attrs.Value(attribute.Key("network.slice.sst"))
	assert.True(t, found, "expected network.slice.sst attribute")
	assert.Equal(t, "1", val.AsString())

	val, found = attrs.Value(attribute.Key("network.slice.sd"))
	assert.True(t, found, "expected network.slice.sd attribute")
	assert.Equal(t, "000001", val.AsString())
}

func TestWriter_Flush(t *testing.T) {
	w, _, err := newWriterWithReader(Config{})
	require.NoError(t, err)
	defer w.Close()
	assert.NoError(t, w.Flush(context.Background()))
}

func TestWriter_Close(t *testing.T) {
	w, _, err := newWriterWithReader(Config{})
	require.NoError(t, err)

	assert.NoError(t, w.Close())
	// Double close must be safe.
	assert.NoError(t, w.Close())
}
