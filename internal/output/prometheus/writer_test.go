package prometheus

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/normalizer"
)

func sampleRecords() []normalizer.NormalizedRecord {
	return []normalizer.NormalizedRecord{
		{
			Namespace: "argus.5g.amf",
			KPIName:   "registration.success_rate",
			Value:     0.97,
			Unit:      "ratio",
			Timestamp: time.Now(),
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "free5gc",
				NFType:       "AMF",
				NFInstanceID: "amf-01",
				PLMNID:       "310-260",
				DataCenter:   "dc-us-east-1",
			},
			SchemaVersion: "v1",
		},
		{
			Namespace: "argus.5g.smf",
			KPIName:   "session.active_count",
			Value:     1500,
			Unit:      "count",
			Timestamp: time.Now(),
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "free5gc",
				NFType:       "SMF",
				NFInstanceID: "smf-01",
				PLMNID:       "310-260",
				DataCenter:   "dc-us-east-1",
				SliceID:      &normalizer.SliceID{SST: 1, SD: "000001"},
			},
			SchemaVersion: "v1",
		},
	}
}

func scrapeBody(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

func TestWriter_Name(t *testing.T) {
	w := NewWriter(":0")
	assert.Equal(t, "prometheus", w.Name())
}

func TestWriter_BatchSize(t *testing.T) {
	w := NewWriter(":0")
	assert.Equal(t, 0, w.BatchSize())
}

func TestWriter_Write_ExposesMetrics(t *testing.T) {
	w := NewWriter(":0")
	records := sampleRecords()

	err := w.Write(context.Background(), records)
	require.NoError(t, err)

	body := scrapeBody(t, w.Handler())

	// Metric names: dots replaced by underscores.
	assert.Contains(t, body, "argus_5g_amf_registration_success_rate")
	assert.Contains(t, body, "argus_5g_smf_session_active_count")

	// Values present.
	assert.Contains(t, body, "0.97")
	assert.Contains(t, body, "1500")

	// Labels present.
	assert.Contains(t, body, `vendor="free5gc"`)
	assert.Contains(t, body, `nf_type="AMF"`)
	assert.Contains(t, body, `nf_instance_id="amf-01"`)
	assert.Contains(t, body, `plmn_id="310-260"`)
	assert.Contains(t, body, `data_center="dc-us-east-1"`)

	// Slice labels on SMF record.
	assert.Contains(t, body, `slice_sst="1"`)
	assert.Contains(t, body, `slice_sd="000001"`)
}

func TestWriter_Write_UpdatesExistingGauge(t *testing.T) {
	w := NewWriter(":0")

	first := []normalizer.NormalizedRecord{
		{
			Namespace: "argus.5g.amf",
			KPIName:   "registration.success_rate",
			Value:     0.90,
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "free5gc",
				NFType:       "AMF",
				NFInstanceID: "amf-01",
			},
		},
	}

	err := w.Write(context.Background(), first)
	require.NoError(t, err)

	// Overwrite with updated value.
	second := []normalizer.NormalizedRecord{
		{
			Namespace: "argus.5g.amf",
			KPIName:   "registration.success_rate",
			Value:     0.95,
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "free5gc",
				NFType:       "AMF",
				NFInstanceID: "amf-01",
			},
		},
	}

	err = w.Write(context.Background(), second)
	require.NoError(t, err)

	body := scrapeBody(t, w.Handler())

	// Should contain the updated value, not the old one.
	assert.Contains(t, body, "0.95")
	// Old value should not appear as a standalone metric value line.
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.Contains(line, "argus_5g_amf_registration_success_rate") && !strings.HasPrefix(line, "#") {
			assert.Contains(t, line, "0.95")
			assert.NotContains(t, line, "0.9 ")
		}
	}
}

func TestWriter_Flush_NoOp(t *testing.T) {
	w := NewWriter(":0")
	err := w.Flush(context.Background())
	assert.NoError(t, err)
}

func TestWriter_Close_NoServer(t *testing.T) {
	w := NewWriter(":0")
	err := w.Close()
	assert.NoError(t, err)
}

func TestWriter_Handler_EmptyRegistry(t *testing.T) {
	w := NewWriter(":0")
	body := scrapeBody(t, w.Handler())
	// Empty registry produces no metric families.
	assert.NotContains(t, body, "argus_")
}

func TestWriter_Write_CellIDLabel(t *testing.T) {
	w := NewWriter(":0")

	records := []normalizer.NormalizedRecord{
		{
			Namespace: "argus.5g.gnb",
			KPIName:   "rrc.connection_success_rate",
			Value:     0.99,
			Attributes: normalizer.ResourceAttributes{
				Vendor:       "oai",
				NFType:       "gNB",
				NFInstanceID: "gnb-01",
				CellID:       &normalizer.CellID{GlobalCellID: "cell-001", NCI: 12345, TAC: 1},
			},
		},
	}

	err := w.Write(context.Background(), records)
	require.NoError(t, err)

	body := scrapeBody(t, w.Handler())
	assert.Contains(t, body, `cell_id="cell-001"`)
	assert.Contains(t, body, "argus_5g_gnb_rrc_connection_success_rate")
}

func TestSanitizeMetricName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"argus.5g.amf.registration.success_rate", "argus_5g_amf_registration_success_rate"},
		{"argus.5g.smf.session.active_count", "argus_5g_smf_session_active_count"},
		{"no_dots_here", "no_dots_here"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeMetricName(tt.input))
		})
	}
}
