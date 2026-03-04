package normalizer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/protobuf/proto"

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

// amfPayload builds a free5gc AMF Prometheus exposition payload.
// free5gc v4.2.0 exposes NAS message counters + ue_connectivity gauge.
// No handover event counters exist.
func amfPayload(regReq, regReject, deregReq, ueConn float64) string {
	return `# TYPE free5gc_nas_msg_received_total counter
free5gc_nas_msg_received_total{name="RegistrationRequest"} ` + ftoa(regReq) + `
free5gc_nas_msg_received_total{name="DeregistrationRequestUEOriginatingDeregistration"} ` + ftoa(deregReq) + `
# TYPE free5gc_nas_nas_msg_sent_total counter
free5gc_nas_nas_msg_sent_total{name="RegistrationReject"} ` + ftoa(regReject) + `
# TYPE free5gc_amf_business_ue_connectivity gauge
free5gc_amf_business_ue_connectivity ` + ftoa(ueConn) + `
`
}

func ftoa(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

func TestEngine_NormalizeFree5GCAmf(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:     "free5gc",
			NFType:     "AMF",
			InstanceID: "amf-001",
			Endpoint:   "http://amf:8080",
		},
		Payload:       []byte(amfPayload(1000, 3, 50, 950)),
		Protocol:      collector.ProtocolPrometheus,
		Timestamp:     time.Now(),
		SchemaVersion: "v1",
	}

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	// Base KPIs — first scrape emits raw counter values.
	assertKPI(t, result, "registration.attempt_count", 1000)
	assertKPI(t, result, "registration.failure_count", 3)
	assertKPI(t, result, "deregistration.count", 50)
	assertKPI(t, result, "ue.connected_count", 950)

	// Derived: (1000 - 3) / 1000 = 0.997
	assertKPIDelta(t, result, "registration.success_rate", 0.997, 0.001)

	// Handover KPIs have no free5gc mapping — unsupported, NOT partial failures.
	assert.Empty(t, result.Partial, "no-mapping KPIs should not appear as partial failures")

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

	raw1 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(1000, 3, 50, 950))
	raw2 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(1050, 5, 55, 960))

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

	// Derived: (50 - 2) / 50 = 0.96
	assertKPIDelta(t, result, "registration.success_rate", 0.96, 0.001)
}

func TestEngine_CounterReset(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(50000, 100, 5000, 950))
	raw2 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(12, 0, 2, 950))

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
}

func TestEngine_PartialFailure(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	// Payload with only RegistrationRequest and ue_connectivity.
	// Missing counters (RegistrationReject, deregistration) default to 0.
	// Handover has no mapping for free5gc — always partial.
	raw := makeRaw("free5gc", "AMF", "amf-001", `# TYPE free5gc_nas_msg_received_total counter
free5gc_nas_msg_received_total{name="RegistrationRequest"} 1000
# TYPE free5gc_amf_business_ue_connectivity gauge
free5gc_amf_business_ue_connectivity 950
`)

	result, err := engine.Normalize(raw)
	require.NoError(t, err, "partial failure should not return top-level error")

	// Base KPIs present in payload should succeed.
	assertKPI(t, result, "registration.attempt_count", 1000)
	assertKPI(t, result, "ue.connected_count", 950)

	// Missing counters default to 0 — they still produce records.
	assertKPI(t, result, "registration.failure_count", 0)
	assertKPI(t, result, "deregistration.count", 0)

	// Derived registration.success_rate resolves: (1000-0)/1000 = 1.0
	assertKPIDelta(t, result, "registration.success_rate", 1.0, 0.001)

	// Handover KPIs have no free5gc mapping — unsupported, NOT partial failures.
	assert.Empty(t, result.Partial, "no-mapping KPIs should not appear as partial failures")
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

	raw := makeRaw("free5gc", "AMF", "amf-001", "some netconf data")
	raw.Protocol = collector.ProtocolNETCONF

	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported protocol")
}

func TestEngine_GaugePassthrough(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	// Three successive scrapes — gauge should always passthrough current value,
	// never compute deltas.
	raw1 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(100, 0, 10, 500))
	raw2 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(200, 1, 20, 750))
	raw3 := makeRaw("free5gc", "AMF", "amf-001", amfPayload(300, 2, 30, 400))

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

	// Payload with only RegistrationRequest and ue_connectivity.
	// Missing counters (RegistrationReject, deregistration) default to 0.
	raw := makeRaw("free5gc", "AMF", "amf-001", `# TYPE free5gc_nas_msg_received_total counter
free5gc_nas_msg_received_total{name="RegistrationRequest"} 1000
# TYPE free5gc_amf_business_ue_connectivity gauge
free5gc_amf_business_ue_connectivity 950
`)

	result, err := engine.Normalize(raw)
	require.NoError(t, err, "partial failure should not return top-level error")

	// registration.attempt_count should succeed.
	assertKPI(t, result, "registration.attempt_count", 1000)

	// Missing counters default to 0 — registration.failure_count resolves.
	assertKPI(t, result, "registration.failure_count", 0)
	assertKPI(t, result, "deregistration.count", 0)

	// Derived registration.success_rate resolves: (1000-0)/1000 = 1.0
	assertKPIDelta(t, result, "registration.success_rate", 1.0, 0.001)

	// Handover KPIs have no free5gc mapping — unsupported, NOT partial failures.
	assert.Empty(t, result.Partial, "no-mapping KPIs should not appear as partial failures")
}

func TestEngine_IndependentInstances(t *testing.T) {
	// Counter state is tracked per source key. Two different instances
	// should maintain independent counter state.
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1a := makeRaw("free5gc", "AMF", "amf-001", amfPayload(1000, 0, 0, 500))
	raw1b := makeRaw("free5gc", "AMF", "amf-002", amfPayload(5000, 0, 0, 800))

	// First scrape for both instances.
	_, err := engine.Normalize(raw1a)
	require.NoError(t, err)
	_, err = engine.Normalize(raw1b)
	require.NoError(t, err)

	// Second scrape: amf-001 gets +50, amf-002 gets +200.
	raw2a := makeRaw("free5gc", "AMF", "amf-001", amfPayload(1050, 0, 0, 500))
	raw2b := makeRaw("free5gc", "AMF", "amf-002", amfPayload(5200, 0, 0, 800))

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

	raw := makeRaw("free5gc", "AMF", "amf-001", amfPayload(0, 0, 0, 0))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	// Ternary guard: attempt_count > 0 ? ... : 0 -> should return 0.
	assertKPI(t, result, "registration.success_rate", 0)

	// Handover has no free5gc mapping — unsupported, not a partial failure.
	assert.Empty(t, result.Partial, "no-mapping KPIs should not appear as partial failures")
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

	// Empty payload: counters default to 0 (absent counter = not incremented),
	// gauges fail (absence is ambiguous). Derived KPIs resolve if deps are counters.
	require.NoError(t, err)
	assert.NotEmpty(t, result.Records, "counters should default to 0 even with empty payload")
	assert.NotEmpty(t, result.Partial, "gauges should still fail with empty payload")
}

// ---------------------------------------------------------------------------
// gNMI helpers
// ---------------------------------------------------------------------------

// gnmiPath converts a slash-delimited path string (e.g. "/gnb/cell/prb/utilization")
// into a gNMI Path proto. Leading slash is stripped before splitting.
func gnmiPath(path string) *gpb.Path {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	elems := make([]*gpb.PathElem, len(parts))
	for i, p := range parts {
		elems[i] = &gpb.PathElem{Name: p}
	}
	return &gpb.Path{Elem: elems}
}

// gnbPayload builds a serialized gNMI SubscribeResponse containing all 7 base
// gNB KPI updates matching the oai vendor mapping in schema/v1/gnb.yaml.
func gnbPayload(t *testing.T, prb, dlBps, ulBps, hoAttempt, hoSuccess, rrcUe, cellAvail float64) []byte {
	t.Helper()
	resp := &gpb.SubscribeResponse{
		Response: &gpb.SubscribeResponse_Update{
			Update: &gpb.Notification{
				Timestamp: time.Now().UnixNano(),
				Update: []*gpb.Update{
					{Path: gnmiPath("/gnb/cell/prb/utilization"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: prb}}},
					{Path: gnmiPath("/gnb/cell/throughput/downlink"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: dlBps}}},
					{Path: gnmiPath("/gnb/cell/throughput/uplink"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: ulBps}}},
					{Path: gnmiPath("/gnb/handover/attempts"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: hoAttempt}}},
					{Path: gnmiPath("/gnb/handover/successes"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: hoSuccess}}},
					{Path: gnmiPath("/gnb/rrc/connected-ues"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: rrcUe}}},
					{Path: gnmiPath("/gnb/cell/availability"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: cellAvail}}},
				},
			},
		},
	}
	data, err := proto.Marshal(resp)
	require.NoError(t, err, "marshaling gNB SubscribeResponse")
	return data
}

// makeGNMIRaw builds a RawRecord for gNMI protocol, analogous to makeRaw for Prometheus.
func makeGNMIRaw(vendor, nfType, instanceID string, payload []byte) collector.RawRecord {
	return collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:     vendor,
			NFType:     nfType,
			InstanceID: instanceID,
			Endpoint:   "gnmi://test:9339",
		},
		Payload:       payload,
		Protocol:      collector.ProtocolGNMI,
		Timestamp:     time.Now(),
		SchemaVersion: "v1",
	}
}

// ---------------------------------------------------------------------------
// gNMI normalization tests
// ---------------------------------------------------------------------------

func TestEngine_NormalizeOAIGnb(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.72, 500e6, 150e6, 1000, 980, 64, 0.998))

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	// 7 base KPIs + 1 derived (handover.success_rate) = 8 records.
	assert.Len(t, result.Records, 8)
	assert.Empty(t, result.Partial)

	// Base KPIs — first scrape emits raw values.
	assertKPI(t, result, "prb.utilization_ratio", 0.72)
	assertKPI(t, result, "throughput.downlink_bps", 500e6)
	assertKPI(t, result, "throughput.uplink_bps", 150e6)
	assertKPI(t, result, "handover.attempt_count", 1000)
	assertKPI(t, result, "handover.success_count", 980)
	assertKPI(t, result, "rrc.connected_ue_count", 64)
	assertKPI(t, result, "cell.availability_ratio", 0.998)

	// Derived: 980 / 1000 = 0.98
	assertKPIDelta(t, result, "handover.success_rate", 0.98, 0.001)

	// Verify attributes on every record.
	for _, r := range result.Records {
		assert.Equal(t, "argus.5g.gnb", r.Namespace)
		assert.Equal(t, "oai", r.Attributes.Vendor)
		assert.Equal(t, "GNB", r.Attributes.NFType)
		assert.Equal(t, "gnb-001", r.Attributes.NFInstanceID)
		assert.NotEmpty(t, r.SpecRef)
		assert.Equal(t, "v1", r.SchemaVersion)
	}
}

func TestEngine_GNMICounterDelta(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.70, 400e6, 100e6, 500, 490, 50, 0.999))
	raw2 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.75, 450e6, 120e6, 600, 585, 55, 0.997))

	_, err := engine.Normalize(raw1) // first scrape
	require.NoError(t, err)

	result, err := engine.Normalize(raw2) // second scrape
	require.NoError(t, err)

	// Counter deltas.
	assertKPI(t, result, "handover.attempt_count", 100) // 600 - 500
	assertKPI(t, result, "handover.success_count", 95)  // 585 - 490

	// Gauge passthrough — always emits current value, not delta.
	assertKPI(t, result, "prb.utilization_ratio", 0.75)
	assertKPI(t, result, "throughput.downlink_bps", 450e6)
	assertKPI(t, result, "throughput.uplink_bps", 120e6)
	assertKPI(t, result, "rrc.connected_ue_count", 55)
	assertKPI(t, result, "cell.availability_ratio", 0.997)

	// Derived: 95 / 100 = 0.95
	assertKPIDelta(t, result, "handover.success_rate", 0.95, 0.001)
}

func TestEngine_GNMICounterReset(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg)

	raw1 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.80, 500e6, 200e6, 50000, 49000, 100, 0.999))
	raw2 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.65, 300e6, 80e6, 15, 14, 40, 0.995))

	_, err := engine.Normalize(raw1)
	require.NoError(t, err)

	result, err := engine.Normalize(raw2)
	require.NoError(t, err)

	// Counter reset: delta is negative, reset_aware=true -> emit newValue.
	assertKPI(t, result, "handover.attempt_count", 15)
	assertKPI(t, result, "handover.success_count", 14)

	// Gauges unaffected by counter reset logic.
	assertKPI(t, result, "prb.utilization_ratio", 0.65)
	assertKPI(t, result, "throughput.downlink_bps", 300e6)
	assertKPI(t, result, "throughput.uplink_bps", 80e6)
	assertKPI(t, result, "rrc.connected_ue_count", 40)
	assertKPI(t, result, "cell.availability_ratio", 0.995)

	// Derived: 14 / 15 = 0.9333...
	assertKPIDelta(t, result, "handover.success_rate", 0.9333, 0.001)
}
