package normalizer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/normalizer/promparser"
	"github.com/argus-5g/argus/internal/schema"
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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{Vendor: "unknown", NFType: "AMF"},
	}
	assert.False(t, engine.CanHandle(raw))
}

func TestEngine_UnknownNFType(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	raw := collector.RawRecord{
		Source: collector.SourceInfo{Vendor: "free5gc", NFType: "NSSF"},
	}
	assert.False(t, engine.CanHandle(raw))
}

func TestEngine_UnsupportedProtocol(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	raw := makeRaw("free5gc", "AMF", "amf-001", "some netconf data")
	raw.Protocol = collector.ProtocolNETCONF

	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported protocol")
}

func TestEngine_GaugePassthrough(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

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
	engine := NewEngine(reg, nil)

	raw := makeRaw("free5gc", "NSSF", "nssf-001", `some payload`)
	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no schema")
}

func TestEngine_NormalizeReturnsErrorForMissingVendor(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	raw := makeRaw("nokia", "AMF", "amf-nokia-001", `some payload`)
	_, err := engine.Normalize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mapping for vendor")
}

func TestEngine_EmptyPayload(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

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

// gnbPayload builds a serialized gNMI SubscribeResponse containing all 8 base
// gNB KPI updates matching the oai vendor mapping in schema/v1/gnb.yaml.
func gnbPayload(t *testing.T, prb, dlBps, ulBps, hoAttempt, hoSuccess, rrcUe, cellAvail, interferenceDL float64) []byte {
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
					{Path: gnmiPath("/gnb/cell/interference/dl"), Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: interferenceDL}}},
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
	engine := NewEngine(reg, nil)

	raw := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.72, 500e6, 150e6, 1000, 980, 64, 0.998, -95.0))

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	// 8 base KPIs + 1 derived (handover.success_rate) = 9 records.
	assert.Len(t, result.Records, 9)
	assert.Empty(t, result.Partial)

	// Base KPIs — first scrape emits raw values.
	assertKPI(t, result, "prb.utilization_ratio", 0.72)
	assertKPI(t, result, "throughput.downlink_bps", 500e6)
	assertKPI(t, result, "throughput.uplink_bps", 150e6)
	assertKPI(t, result, "handover.attempt_count", 1000)
	assertKPI(t, result, "handover.success_count", 980)
	assertKPI(t, result, "rrc.connected_ue_count", 64)
	assertKPI(t, result, "interference.dl_dBm", -95.0)
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
	engine := NewEngine(reg, nil)

	raw1 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.70, 400e6, 100e6, 500, 490, 50, 0.999, -93.0))
	raw2 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.75, 450e6, 120e6, 600, 585, 55, 0.997, -94.5))

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
	engine := NewEngine(reg, nil)

	raw1 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.80, 500e6, 200e6, 50000, 49000, 100, 0.999, -90.0))
	raw2 := makeGNMIRaw("oai", "GNB", "gnb-001",
		gnbPayload(t, 0.65, 300e6, 80e6, 15, 14, 40, 0.995, -92.0))

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

// --- LabelExtract tests ---
// These tests exercise LabelExtract at the matchTemplateMetric function level
// since Prometheus exposition format doesn't support slash-delimited paths.
// The real-world use case is gNMI (Nokia ENM), tested here with unit-level
// function calls.

func TestLabelExtract_FromTemplatePath(t *testing.T) {
	mapping := &schema.MetricMapping{
		SourceTemplate:     "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts",
		Type:               "counter",
		LabelMatchStrategy: "exact",
		LabelExtract: []schema.LabelExtractRule{
			{PathSegment: 4, LabelName: "instance_id"},
		},
	}

	parsed := []promparser.ParsedMetric{
		{Name: "pm/stats/AMF-1/reg/inst-42/attempts", Value: 500},
	}

	val, labels, matched := matchTemplateMetric(parsed, mapping)
	require.True(t, matched)
	assert.Equal(t, 500.0, val)
	assert.NotNil(t, labels)
	assert.Equal(t, "inst-42", labels["instance_id"], "segment 4 should extract instance_id")
}

func TestLabelExtract_MergeWithExistingLabels(t *testing.T) {
	mapping := &schema.MetricMapping{
		SourceTemplate:     "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts",
		Type:               "counter",
		LabelMatchStrategy: "exact",
		LabelExtract: []schema.LabelExtractRule{
			{PathSegment: 4, LabelName: "instance_id"},
		},
	}

	parsed := []promparser.ParsedMetric{
		{
			Name:   "pm/stats/AMF-1/reg/inst-42/attempts",
			Value:  500,
			Labels: map[string]string{"existing_label": "existing_value"},
		},
	}

	_, labels, matched := matchTemplateMetric(parsed, mapping)
	require.True(t, matched)
	// Existing metric labels preserved.
	assert.Equal(t, "existing_value", labels["existing_label"])
	// Template captures.
	assert.Equal(t, "AMF-1", labels["NF"])
	assert.Equal(t, "inst-42", labels["Instance"])
	// LabelExtract rule.
	assert.Equal(t, "inst-42", labels["instance_id"])
}

func TestLabelExtract_ConflictResolution(t *testing.T) {
	// Template capture overwrites LabelExtract when keys collide,
	// because template vars are written last in the merge order.
	mapping := &schema.MetricMapping{
		SourceTemplate:     "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts",
		Type:               "counter",
		LabelMatchStrategy: "exact",
		LabelExtract: []schema.LabelExtractRule{
			// Extract segment 4 as "Instance" — same key as template capture.
			{PathSegment: 4, LabelName: "Instance"},
		},
	}

	parsed := []promparser.ParsedMetric{
		{Name: "pm/stats/AMF-1/reg/inst-42/attempts", Value: 500},
	}

	_, labels, matched := matchTemplateMetric(parsed, mapping)
	require.True(t, matched)
	// Template capture "Instance" = "inst-42" wins over LabelExtract "Instance" = "inst-42".
	// In this case both produce the same value, but the template capture path runs last.
	assert.Equal(t, "inst-42", labels["Instance"])
}

func TestLabelExtract_OutOfBoundsSegment(t *testing.T) {
	mapping := &schema.MetricMapping{
		SourceTemplate:     "/a/{{.X}}/c",
		Type:               "gauge",
		LabelMatchStrategy: "exact",
		LabelExtract: []schema.LabelExtractRule{
			{PathSegment: 99, LabelName: "should_not_exist"},
		},
	}

	parsed := []promparser.ParsedMetric{
		{Name: "a/foo/c", Value: 100},
	}

	// Must not panic.
	val, labels, matched := matchTemplateMetric(parsed, mapping)
	assert.True(t, matched)
	assert.Equal(t, 100.0, val)
	// Out-of-bounds segment silently skipped.
	_, exists := labels["should_not_exist"]
	assert.False(t, exists, "out-of-bounds segment should not produce a label")
	// But template capture still works.
	assert.Equal(t, "foo", labels["X"])
}

// ---------------------------------------------------------------------------
// Ericsson ENM vendor mapping tests
// ---------------------------------------------------------------------------

// ericssonAMFPayload builds an Ericsson ENM Prometheus exposition payload
// using colon-delimited PM counter paths matching the ericsson_enm source_templates.
func ericssonAMFPayload(regAtt, regFail, rrcConn float64) string {
	return `# TYPE ericsson_pm counter
ericsson_pm:pm_job_1:reader_1:pmNrRegInitAttSum:amf_001 ` + ftoa(regAtt) + `
ericsson_pm:pm_job_1:reader_1:pmNrRegInitFailSum:amf_001 ` + ftoa(regFail) + `
# TYPE ericsson_pm_gauge gauge
ericsson_pm:pm_job_1:reader_1:pmNrRrcConnectedUeSum:amf_001 ` + ftoa(rrcConn) + `
`
}

func TestEricssonAMF_RealCounterMapping(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	raw := makeRaw("ericsson_enm", "AMF", "amf-001", ericssonAMFPayload(5000, 12, 920))

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	// pmNrRegInitAttSum -> registration.attempt_count
	assertKPI(t, result, "registration.attempt_count", 5000)
	// pmNrRegInitFailSum -> registration.failure_count
	assertKPI(t, result, "registration.failure_count", 12)
	// pmNrRrcConnectedUeSum -> ue.connected_count
	assertKPI(t, result, "ue.connected_count", 920)

	// Derived: (5000 - 12) / 5000 = 0.9976
	assertKPIDelta(t, result, "registration.success_rate", 0.9976, 0.001)

	// Verify labels extracted from colon-delimited path: segment 4 = instance_id
	for _, r := range result.Records {
		assert.Equal(t, "argus.5g.amf", r.Namespace)
		assert.Equal(t, "ericsson_enm", r.Attributes.Vendor)
		assert.Equal(t, "AMF", r.Attributes.NFType)
		if r.Labels != nil {
			assert.Equal(t, "amf_001", r.Labels["instance_id"],
				"segment 4 of colon-delimited path should extract as instance_id for KPI %s", r.KPIName)
		}
	}
}

func TestEricssonGNB_CellLabelExtract(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	// gNB Ericsson mapping uses segment 4 for instance_id
	raw := makeRaw("ericsson_enm", "GNB", "gnb-001", `# TYPE ericsson_pm gauge
ericsson_pm:pm_job_1:reader_1:pmMacRBSymUsedPdschTypeA:gnb_cell_001 0.72
ericsson_pm:pm_job_1:reader_1:pmRadioTxRankDistrDl:gnb_cell_001 500000000
ericsson_pm:pm_job_1:reader_1:pmRadioTxRankDistrUl:gnb_cell_001 150000000
ericsson_pm:pm_job_1:reader_1:pmRrcConnectedUeSum:gnb_cell_001 64
`)

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	assertKPI(t, result, "prb.utilization_ratio", 0.72)
	assertKPI(t, result, "throughput.downlink_bps", 500e6)
	assertKPI(t, result, "throughput.uplink_bps", 150e6)
	assertKPI(t, result, "rrc.connected_ue_count", 64)

	// Verify instance_id label extracted from segment 4 of colon-delimited path
	for _, r := range result.Records {
		if r.Labels != nil {
			assert.Equal(t, "gnb_cell_001", r.Labels["instance_id"],
				"segment 4 should extract instance_id for KPI %s", r.KPIName)
		}
	}
}

func TestEricssonSlice_SNSSAIExtract(t *testing.T) {
	reg := loadTestRegistry(t)
	engine := NewEngine(reg, nil)

	// Slice Ericsson mapping uses segment 4 for instance_id
	raw := makeRaw("ericsson_enm", "SLICE", "slice-001", `# TYPE ericsson_pm gauge
ericsson_pm:pm_job_1:reader_1:pmSliceLatencyCurrent:slice_snssai_1_010203 8.5
ericsson_pm:pm_job_1:reader_1:pmSliceThroughputCurrent:slice_snssai_1_010203 1000000000
ericsson_pm:pm_job_1:reader_1:pmSliceUeActive:slice_snssai_1_010203 150
`)

	assert.True(t, engine.CanHandle(raw))

	result, err := engine.Normalize(raw)
	require.NoError(t, err)

	assertKPI(t, result, "latency.current_ms", 8.5)
	assertKPI(t, result, "throughput.current_bps", 1e9)
	assertKPI(t, result, "ue.active_count", 150)

	// Verify instance_id label extracted from segment 4 (encodes SNSSAI in the path)
	for _, r := range result.Records {
		if r.Labels != nil {
			assert.Equal(t, "slice_snssai_1_010203", r.Labels["instance_id"],
				"segment 4 should extract instance_id (encodes SNSSAI) for KPI %s", r.KPIName)
		}
	}
}

func TestSplitPathSegments(t *testing.T) {
	tests := []struct {
		path     string
		expected []string
	}{
		{"/pm/stats/AMF/reg/inst/attempts", []string{"pm", "stats", "AMF", "reg", "inst", "attempts"}},
		{"pm/stats/AMF", []string{"pm", "stats", "AMF"}},
		{"prometheus_metric_name", []string{"prometheus", "metric", "name"}},
		{"/a/b/c/", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		result := splitPathSegments(tt.path)
		assert.Equal(t, tt.expected, result, "path=%s", tt.path)
	}
}
