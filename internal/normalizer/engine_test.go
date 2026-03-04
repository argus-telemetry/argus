package normalizer

import (
	"testing"
	"time"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTestRegistry(t *testing.T) *schema.Registry {
	t.Helper()
	reg, err := schema.LoadFromDir("../../schema/v1")
	require.NoError(t, err, "failed to load schema directory")
	return reg
}

func makeRaw(vendor, nfType, instanceID, payload string) collector.RawRecord {
	return collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:     vendor,
			NFType:     nfType,
			InstanceID: instanceID,
			Endpoint:   "http://test:8080",
		},
		Payload:       []byte(payload),
		Protocol:      collector.ProtocolPrometheus,
		Timestamp:     time.Now(),
		SchemaVersion: "v1",
	}
}

func assertKPI(t *testing.T, result NormalizeResult, kpiName string, expectedValue float64) {
	t.Helper()
	for _, r := range result.Records {
		if r.KPIName == kpiName {
			assert.Equal(t, expectedValue, r.Value, "KPI %s", kpiName)
			return
		}
	}
	t.Errorf("KPI %s not found in results", kpiName)
}

func assertKPIDelta(t *testing.T, result NormalizeResult, kpiName string, expected, delta float64) {
	t.Helper()
	for _, r := range result.Records {
		if r.KPIName == kpiName {
			assert.InDelta(t, expected, r.Value, delta, "KPI %s", kpiName)
			return
		}
	}
	t.Errorf("KPI %s not found in results", kpiName)
}

func TestEngine_NormalizeFree5GCAmf(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	payload := []byte(`# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
amf_n1_message_total{msg_type="registration_reject"} 3
amf_n1_message_total{msg_type="deregistration_request"} 50
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 200
amf_handover_total{result="success"} 195
`)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:     "free5gc",
			NFType:     "AMF",
			InstanceID: "amf-001",
			Endpoint:   "http://amf:8080",
		},
		Payload:       payload,
		Protocol:      collector.ProtocolPrometheus,
		Timestamp:     time.Now(),
		SchemaVersion: "v1",
	}

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Partial)

	// Base KPIs — first scrape emits raw counter values.
	assertKPI(t, result, "registration.attempt_count", 1000)
	assertKPI(t, result, "registration.failure_count", 3)
	assertKPI(t, result, "deregistration.count", 50)
	assertKPI(t, result, "ue.connected_count", 950)
	assertKPI(t, result, "handover.attempt_count", 200)
	assertKPI(t, result, "handover.success_count", 195)

	// Derived KPIs.
	// registration.success_rate = (1000 - 3) / 1000 = 0.997
	assertKPIDelta(t, result, "registration.success_rate", 0.997, 0.001)
	// handover.success_rate = 195 / 200 = 0.975
	assertKPIDelta(t, result, "handover.success_rate", 0.975, 0.001)

	// Verify attributes on every record.
	for _, r := range result.Records {
		assert.Equal(t, "argus.5g.amf", r.Namespace)
		assert.Equal(t, "free5gc", r.Attributes.Vendor)
		assert.Equal(t, "AMF", r.Attributes.NFType)
		assert.Equal(t, "amf-001", r.Attributes.NFInstanceID)
		assert.NotEmpty(t, r.SpecRef)
		assert.Equal(t, "v1", r.SchemaVersion)
	}
}

func TestEngine_CounterDelta(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
amf_n1_message_total{msg_type="registration_reject"} 3
amf_n1_message_total{msg_type="deregistration_request"} 50
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 200
amf_handover_total{result="success"} 195
`)
	raw2 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1050
amf_n1_message_total{msg_type="registration_reject"} 5
amf_n1_message_total{msg_type="deregistration_request"} 55
# TYPE amf_connected_ue gauge
amf_connected_ue 960
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 210
amf_handover_total{result="success"} 205
`)

	_, err := engine.Normalize(raw1) // first scrape
	require.NoError(t, err)

	result, err := engine.Normalize(raw2) // second scrape
	require.NoError(t, err)

	// Counter deltas.
	assertKPI(t, result, "registration.attempt_count", 50) // 1050 - 1000
	assertKPI(t, result, "registration.failure_count", 2)  // 5 - 3
	assertKPI(t, result, "deregistration.count", 5)        // 55 - 50

	// Gauge passthrough.
	assertKPI(t, result, "ue.connected_count", 960)

	// Handover deltas.
	assertKPI(t, result, "handover.attempt_count", 10) // 210 - 200
	assertKPI(t, result, "handover.success_count", 10) // 205 - 195

	// Derived KPIs use the delta values from this scrape interval.
	// registration.success_rate = (50 - 2) / 50 = 0.96
	assertKPIDelta(t, result, "registration.success_rate", 0.96, 0.001)
	// handover.success_rate = 10 / 10 = 1.0
	assertKPIDelta(t, result, "handover.success_rate", 1.0, 0.001)
}

func TestEngine_CounterReset(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 50000
amf_n1_message_total{msg_type="registration_reject"} 100
amf_n1_message_total{msg_type="deregistration_request"} 5000
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 1000
amf_handover_total{result="success"} 990
`)
	raw2 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 12
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 2
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 5
amf_handover_total{result="success"} 4
`)

	_, err := engine.Normalize(raw1)
	require.NoError(t, err)

	result, err := engine.Normalize(raw2)
	require.NoError(t, err)

	// Counter reset: delta is negative, reset_aware=true -> emit newValue.
	assertKPI(t, result, "registration.attempt_count", 12)
	assertKPI(t, result, "registration.failure_count", 0)
	assertKPI(t, result, "deregistration.count", 2)

	// Gauge unaffected by counter reset logic.
	assertKPI(t, result, "ue.connected_count", 950)

	// Handover counters also reset.
	assertKPI(t, result, "handover.attempt_count", 5)
	assertKPI(t, result, "handover.success_count", 4)
}

func TestEngine_PartialFailure(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	// Payload missing handover metrics — those KPIs should go to Partial,
	// and the derived handover.success_rate should also fail.
	raw := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
amf_n1_message_total{msg_type="registration_reject"} 3
amf_n1_message_total{msg_type="deregistration_request"} 50
# TYPE amf_connected_ue gauge
amf_connected_ue 950
`)

	result, err := engine.Normalize(raw)
	require.NoError(t, err, "partial failure should not return top-level error")

	// Base KPIs present in payload should succeed.
	assertKPI(t, result, "registration.attempt_count", 1000)
	assertKPI(t, result, "registration.failure_count", 3)
	assertKPI(t, result, "deregistration.count", 50)
	assertKPI(t, result, "ue.connected_count", 950)

	// Derived registration.success_rate should succeed (dependencies are present).
	assertKPIDelta(t, result, "registration.success_rate", 0.997, 0.001)

	// Handover KPIs should be in Partial.
	partialNames := make(map[string]bool)
	for _, pe := range result.Partial {
		partialNames[pe.KPIName] = true
	}
	assert.True(t, partialNames["handover.attempt_count"], "handover.attempt_count should be partial")
	assert.True(t, partialNames["handover.success_count"], "handover.success_count should be partial")
	assert.True(t, partialNames["handover.success_rate"], "handover.success_rate should be partial (dependency failed)")
}

func TestEngine_UnknownVendor(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{Vendor: "unknown", NFType: "AMF"},
	}
	assert.False(t, engine.CanHandle(raw))
}

func TestEngine_UnknownNFType(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{Vendor: "free5gc", NFType: "NSSF"},
	}
	assert.False(t, engine.CanHandle(raw))
}

func TestEngine_UnsupportedProtocol(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("free5gc", "AMF", "amf-001", "some gnmi data")
	raw.Protocol = collector.ProtocolGNMI

	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported protocol")
}

func TestEngine_GaugePassthrough(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	// Three successive scrapes — gauge should always passthrough current value,
	// never compute deltas.
	raw1 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 100
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 10
# TYPE amf_connected_ue gauge
amf_connected_ue 500
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 50
amf_handover_total{result="success"} 50
`)
	raw2 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 200
amf_n1_message_total{msg_type="registration_reject"} 1
amf_n1_message_total{msg_type="deregistration_request"} 20
# TYPE amf_connected_ue gauge
amf_connected_ue 750
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 60
amf_handover_total{result="success"} 58
`)
	raw3 := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 300
amf_n1_message_total{msg_type="registration_reject"} 2
amf_n1_message_total{msg_type="deregistration_request"} 30
# TYPE amf_connected_ue gauge
amf_connected_ue 400
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 70
amf_handover_total{result="success"} 68
`)

	result1, err := engine.Normalize(raw1)
	require.NoError(t, err)
	assertKPI(t, result1, "ue.connected_count", 500)

	result2, err := engine.Normalize(raw2)
	require.NoError(t, err)
	assertKPI(t, result2, "ue.connected_count", 750) // passthrough, not delta

	result3, err := engine.Normalize(raw3)
	require.NoError(t, err)
	assertKPI(t, result3, "ue.connected_count", 400) // gauge can decrease without being a "reset"
}

func TestEngine_DerivedKPIWithFailedDependency(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	// Only provide registration_request, but not registration_reject.
	// registration.failure_count will fail, so registration.success_rate should also fail.
	raw := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
# TYPE amf_connected_ue gauge
amf_connected_ue 950
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 100
amf_handover_total{result="success"} 99
`)

	result, err := engine.Normalize(raw)
	require.NoError(t, err, "partial failure should not return top-level error")

	// registration.attempt_count should succeed.
	assertKPI(t, result, "registration.attempt_count", 1000)

	// registration.failure_count should fail (no registration_reject series).
	partialNames := make(map[string]bool)
	for _, pe := range result.Partial {
		partialNames[pe.KPIName] = true
	}
	assert.True(t, partialNames["registration.failure_count"], "registration.failure_count should be partial")
	assert.True(t, partialNames["registration.success_rate"], "registration.success_rate should be partial (dependency failed)")

	// deregistration.count should also fail (no deregistration_request series).
	assert.True(t, partialNames["deregistration.count"], "deregistration.count should be partial")

	// handover KPIs should succeed.
	assertKPI(t, result, "handover.attempt_count", 100)
	assertKPI(t, result, "handover.success_count", 99)
	assertKPIDelta(t, result, "handover.success_rate", 0.99, 0.001)
}

func TestEngine_IndependentInstances(t *testing.T) {
	// Counter state is tracked per source key. Two different instances
	// should maintain independent counter state.
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1a := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1000
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 0
# TYPE amf_connected_ue gauge
amf_connected_ue 500
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 100
amf_handover_total{result="success"} 100
`)
	raw1b := makeRaw("free5gc", "AMF", "amf-002", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 5000
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 0
# TYPE amf_connected_ue gauge
amf_connected_ue 800
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 200
amf_handover_total{result="success"} 200
`)

	// First scrape for both instances.
	_, err := engine.Normalize(raw1a)
	require.NoError(t, err)
	_, err = engine.Normalize(raw1b)
	require.NoError(t, err)

	// Second scrape: amf-001 gets +50, amf-002 gets +200.
	raw2a := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 1050
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 0
# TYPE amf_connected_ue gauge
amf_connected_ue 500
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 100
amf_handover_total{result="success"} 100
`)
	raw2b := makeRaw("free5gc", "AMF", "amf-002", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 5200
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 0
# TYPE amf_connected_ue gauge
amf_connected_ue 800
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 200
amf_handover_total{result="success"} 200
`)

	result2a, err := engine.Normalize(raw2a)
	require.NoError(t, err)
	assertKPI(t, result2a, "registration.attempt_count", 50)

	result2b, err := engine.Normalize(raw2b)
	require.NoError(t, err)
	assertKPI(t, result2b, "registration.attempt_count", 200)
}

func TestEngine_DerivedKPIZeroDenominator(t *testing.T) {
	// When attempt_count is 0, the ternary formula should return 0, not error.
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("free5gc", "AMF", "amf-001", `# TYPE amf_n1_message_total counter
amf_n1_message_total{msg_type="registration_request"} 0
amf_n1_message_total{msg_type="registration_reject"} 0
amf_n1_message_total{msg_type="deregistration_request"} 0
# TYPE amf_connected_ue gauge
amf_connected_ue 0
# TYPE amf_handover_total counter
amf_handover_total{result="attempt"} 0
amf_handover_total{result="success"} 0
`)

	result, err := engine.Normalize(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Partial)

	// Ternary guard: attempt_count > 0 ? ... : 0 -> should return 0.
	assertKPI(t, result, "registration.success_rate", 0)
	assertKPI(t, result, "handover.success_rate", 0)
}

func TestEngine_Open5GS(t *testing.T) {
	// Verify the engine works with open5gs vendor mappings too.
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("open5gs", "AMF", "amf-open5gs-001", `# TYPE open5gs_amf_registration_total counter
open5gs_amf_registration_total{status="attempted"} 2000
open5gs_amf_registration_total{status="failed"} 10
# TYPE open5gs_amf_deregistration_total counter
open5gs_amf_deregistration_total 100
# TYPE open5gs_amf_ue_connected gauge
open5gs_amf_ue_connected 1500
# TYPE open5gs_amf_handover_total counter
open5gs_amf_handover_total{result="attempted"} 300
open5gs_amf_handover_total{result="successful"} 290
`)

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Partial)

	assertKPI(t, result, "registration.attempt_count", 2000)
	assertKPI(t, result, "registration.failure_count", 10)
	assertKPI(t, result, "deregistration.count", 100)
	assertKPI(t, result, "ue.connected_count", 1500)
	assertKPI(t, result, "handover.attempt_count", 300)
	assertKPI(t, result, "handover.success_count", 290)

	// Derived: (2000 - 10) / 2000 = 0.995
	assertKPIDelta(t, result, "registration.success_rate", 0.995, 0.001)
	// Derived: 290 / 300 = 0.9667
	assertKPIDelta(t, result, "handover.success_rate", 0.9667, 0.001)

	// Verify attributes reflect open5gs.
	for _, r := range result.Records {
		assert.Equal(t, "open5gs", r.Attributes.Vendor)
		assert.Equal(t, "amf-open5gs-001", r.Attributes.NFInstanceID)
	}
}

func TestEngine_NormalizeReturnsErrorForMissingSchema(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("free5gc", "NSSF", "nssf-001", `some payload`)
	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no schema")
}

func TestEngine_NormalizeReturnsErrorForMissingVendor(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("nokia", "AMF", "amf-nokia-001", `some payload`)
	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mapping for vendor")
}

func TestEngine_EmptyPayload(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeRaw("free5gc", "AMF", "amf-001", "")
	result, err := engine.Normalize(raw)

	// Empty payload parses to zero metrics — all KPIs go to Partial.
	// This is not a total failure (the payload format is valid).
	require.NoError(t, err)
	assert.Empty(t, result.Records)
	assert.NotEmpty(t, result.Partial)
}
