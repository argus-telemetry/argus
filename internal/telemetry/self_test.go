package telemetry

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRegistered(t *testing.T) (*Metrics, *prometheus.Registry) {
	t.Helper()
	m := NewMetrics()
	reg := prometheus.NewRegistry()
	m.Register(reg)
	return m, reg
}

func TestNewMetrics_AllFieldsNonNil(t *testing.T) {
	m := NewMetrics()
	assert.NotNil(t, m.CollectorScrapeDuration)
	assert.NotNil(t, m.CollectorScrapeSuccess)
	assert.NotNil(t, m.CollectorScrapeErrors)
	assert.NotNil(t, m.CollectorRecordsTotal)
	assert.NotNil(t, m.NormalizerRecordsTotal)
	assert.NotNil(t, m.NormalizerPartialFails)
	assert.NotNil(t, m.NormalizerTotalFails)
	assert.NotNil(t, m.PipelinePublishTotal)
	assert.NotNil(t, m.OutputWriteTotal)
	assert.NotNil(t, m.OutputWriteErrors)
	assert.NotNil(t, m.SchemaLoadSuccess)
}

func TestRegister_NoPanic(t *testing.T) {
	m := NewMetrics()
	reg := prometheus.NewRegistry()
	require.NotPanics(t, func() { m.Register(reg) })
}

func TestRecordScrape_Success(t *testing.T) {
	m, reg := newRegistered(t)

	m.RecordScrape("free5gc-amf", 150*time.Millisecond, true)

	val := testutil.ToFloat64(m.CollectorScrapeSuccess.WithLabelValues("free5gc-amf"))
	assert.Equal(t, 1.0, val)

	// Verify histogram was observed by gathering from the registry.
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == "argus_collector_scrape_duration_seconds" {
			require.Len(t, f.GetMetric(), 1)
			h := f.GetMetric()[0].GetHistogram()
			require.NotNil(t, h)
			assert.Equal(t, uint64(1), h.GetSampleCount())
			assert.InDelta(t, 0.15, h.GetSampleSum(), 0.001)
			return
		}
	}
	t.Fatal("argus_collector_scrape_duration_seconds not found in gathered metrics")
}

func TestRecordScrape_Failure(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordScrape("free5gc-smf", 200*time.Millisecond, false)

	val := testutil.ToFloat64(m.CollectorScrapeSuccess.WithLabelValues("free5gc-smf"))
	assert.Equal(t, 0.0, val)
}

func TestRecordScrapeError(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordScrapeError("free5gc", "AMF", "auth")
	m.RecordScrapeError("free5gc", "AMF", "auth")
	m.RecordScrapeError("free5gc", "AMF", "network")

	auth := testutil.ToFloat64(m.CollectorScrapeErrors.WithLabelValues("free5gc", "AMF", "auth"))
	assert.Equal(t, 2.0, auth)

	net := testutil.ToFloat64(m.CollectorScrapeErrors.WithLabelValues("free5gc", "AMF", "network"))
	assert.Equal(t, 1.0, net)

	timeout := testutil.ToFloat64(m.CollectorScrapeErrors.WithLabelValues("free5gc", "AMF", "timeout"))
	assert.Equal(t, 0.0, timeout)
}

func TestRecordNormalize(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordNormalize("free5gc", "AMF", 42, 3)

	records := testutil.ToFloat64(m.NormalizerRecordsTotal.WithLabelValues("free5gc", "AMF"))
	assert.Equal(t, 42.0, records)

	partial := testutil.ToFloat64(m.NormalizerPartialFails.WithLabelValues("free5gc", "AMF"))
	assert.Equal(t, 3.0, partial)
}

func TestRecordNormalize_ZeroPartialFails(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordNormalize("open5gs", "SMF", 10, 0)

	records := testutil.ToFloat64(m.NormalizerRecordsTotal.WithLabelValues("open5gs", "SMF"))
	assert.Equal(t, 10.0, records)

	// Counter not incremented when partialFails == 0.
	partial := testutil.ToFloat64(m.NormalizerPartialFails.WithLabelValues("open5gs", "SMF"))
	assert.Equal(t, 0.0, partial)
}

func TestRecordPublish(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordPublish("normalized-kpis", 100)
	m.RecordPublish("normalized-kpis", 50)

	val := testutil.ToFloat64(m.PipelinePublishTotal.WithLabelValues("normalized-kpis"))
	assert.Equal(t, 150.0, val)
}

func TestRecordWrite_Success(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordWrite("prometheus", 25, nil)

	written := testutil.ToFloat64(m.OutputWriteTotal.WithLabelValues("prometheus"))
	assert.Equal(t, 25.0, written)

	errs := testutil.ToFloat64(m.OutputWriteErrors.WithLabelValues("prometheus"))
	assert.Equal(t, 0.0, errs)
}

func TestRecordWrite_Error(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordWrite("opensearch", 10, errors.New("connection refused"))

	written := testutil.ToFloat64(m.OutputWriteTotal.WithLabelValues("opensearch"))
	assert.Equal(t, 10.0, written)

	errs := testutil.ToFloat64(m.OutputWriteErrors.WithLabelValues("opensearch"))
	assert.Equal(t, 1.0, errs)
}

func TestRecordSchemaLoad(t *testing.T) {
	m, _ := newRegistered(t)

	m.RecordSchemaLoad("v1", true)
	val := testutil.ToFloat64(m.SchemaLoadSuccess.WithLabelValues("v1"))
	assert.Equal(t, 1.0, val)

	// Flip to failure — gauge overwrites.
	m.RecordSchemaLoad("v1", false)
	val = testutil.ToFloat64(m.SchemaLoadSuccess.WithLabelValues("v1"))
	assert.Equal(t, 0.0, val)
}

func TestMetricNames_Prefix(t *testing.T) {
	m, reg := newRegistered(t)

	// Touch all metric families so they appear in Gather().
	m.RecordScrape("x", time.Millisecond, true)
	m.RecordScrapeError("x", "X", "network")
	m.RecordNormalize("x", "X", 1, 0)
	m.RecordPublish("x", 1)
	m.RecordWrite("x", 1, nil)
	m.RecordSchemaLoad("v1", true)

	families, err := reg.Gather()
	require.NoError(t, err)

	for _, f := range families {
		assert.True(t, strings.HasPrefix(f.GetName(), "argus_"),
			"metric %q missing argus_ prefix", f.GetName())
	}
}
