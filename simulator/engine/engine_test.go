package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/simulator/engine"
)

func makeScenario(nfs ...engine.SimulatedNF) engine.Scenario {
	return engine.Scenario{
		Name:        "test-scenario",
		Description: "unit test scenario",
		NFs:         nfs,
	}
}

func TestEngine_SteadyState(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "amf_n1_message_total",
				Type:          "counter",
				Baseline:      1000,
				RatePerSecond: 10,
			},
			{
				Name:     "amf_connected_ue",
				Type:     "gauge",
				Baseline: 950,
				Jitter:   50,
			},
		},
	}

	eng := engine.New(makeScenario(nf))
	eng.Advance(15 * time.Second)

	state := eng.State("amf-001")
	require.NotNil(t, state)

	// Counter: baseline (1000) + rate (10) * seconds (15) = 1150.
	assert.Equal(t, 1150.0, state["amf_n1_message_total"])

	// Gauge: within baseline +/- jitter.
	gauge := state["amf_connected_ue"]
	assert.InDelta(t, 950, gauge, 50, "gauge should be within baseline +/- jitter")
}

func TestEngine_EventInjection(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "amf_n1_message_total",
				Type:          "counter",
				Baseline:      0,
				RatePerSecond: 10,
			},
		},
		Events: []engine.Event{
			{
				Name:      "traffic-spike",
				StartSec:  30,
				DurationS: 60,
				Metric:    "amf_n1_message_total",
				RateScale: 10,
			},
		},
	}

	eng := engine.New(makeScenario(nf))

	// Advance to t=31s (past event start).
	eng.Advance(31 * time.Second)

	state := eng.State("amf-001")
	require.NotNil(t, state)

	// First 31 seconds: 30s at rate 10/s = 300, then 1s at rate 10*10=100/s = 100.
	// But Advance is a single call, so the entire 31s uses the event check at elapsed=31s.
	// At elapsed=31s, the event is active (30 <= 31 < 90), so rate = 10 * 10 = 100.
	// value = 0 + 100 * 31 = 3100.
	// This is the simplified model: Advance applies the rate at the final elapsed time.
	assert.Equal(t, 3100.0, state["amf_n1_message_total"])

	// Advance 1 more second (now at t=32s). Event still active.
	eng.Advance(1 * time.Second)
	state = eng.State("amf-001")

	// Additional: 100 * 1 = 100. Total = 3200.
	assert.Equal(t, 3200.0, state["amf_n1_message_total"])
}

func TestEngine_PrometheusOutput(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "amf_n1_message_total",
				Labels:        map[string]string{"msg_type": "registration_request"},
				Type:          "counter",
				Baseline:      1000,
				RatePerSecond: 10,
			},
			{
				Name:     "amf_connected_ue",
				Type:     "gauge",
				Baseline: 950,
			},
		},
	}

	eng := engine.New(makeScenario(nf))
	output := string(eng.PrometheusOutput("amf-001"))

	// TYPE comments present.
	assert.Contains(t, output, "# TYPE amf_n1_message_total counter")
	assert.Contains(t, output, "# TYPE amf_connected_ue gauge")

	// HELP comments present.
	assert.Contains(t, output, "# HELP amf_n1_message_total")
	assert.Contains(t, output, "# HELP amf_connected_ue")

	// Labeled metric uses Prometheus label syntax.
	assert.Contains(t, output, `amf_n1_message_total{msg_type="registration_request"} 1000`)

	// Unlabeled metric rendered without braces.
	assert.Contains(t, output, "amf_connected_ue 950")
}

func TestEngine_IndependentInstances(t *testing.T) {
	nf1 := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "amf_n1_message_total",
				Type:          "counter",
				Baseline:      1000,
				RatePerSecond: 10,
			},
		},
	}

	nf2 := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-002",
		Protocol:   "prometheus",
		Port:       9091,
		Metrics: []engine.BaseMetric{
			{
				Name:          "amf_n1_message_total",
				Type:          "counter",
				Baseline:      5000,
				RatePerSecond: 100,
			},
		},
	}

	eng := engine.New(makeScenario(nf1, nf2))
	eng.Advance(10 * time.Second)

	state1 := eng.State("amf-001")
	state2 := eng.State("amf-002")
	require.NotNil(t, state1)
	require.NotNil(t, state2)

	// amf-001: 1000 + 10*10 = 1100
	assert.Equal(t, 1100.0, state1["amf_n1_message_total"])

	// amf-002: 5000 + 100*10 = 6000
	assert.Equal(t, 6000.0, state2["amf_n1_message_total"])
}

func TestEngine_GaugeEventOverride(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "UPF",
		Vendor:     "free5gc",
		InstanceID: "upf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:     "upf_session_count",
				Type:     "gauge",
				Baseline: 500,
				Jitter:   10,
			},
		},
		Events: []engine.Event{
			{
				Name:      "session-spike",
				StartSec:  10,
				DurationS: 30,
				Metric:    "upf_session_count",
				Override:  2000,
			},
		},
	}

	eng := engine.New(makeScenario(nf))

	// Before event window: gauge should be near baseline.
	eng.Advance(5 * time.Second)
	state := eng.State("upf-001")
	require.NotNil(t, state)
	assert.InDelta(t, 500, state["upf_session_count"], 10)

	// During event window: gauge overridden to exact value.
	eng.Advance(10 * time.Second) // now at t=15s, event active [10, 40)
	state = eng.State("upf-001")
	assert.Equal(t, 2000.0, state["upf_session_count"])

	// After event window: gauge returns to baseline +/- jitter.
	eng.Advance(30 * time.Second) // now at t=45s, event ended at t=40
	state = eng.State("upf-001")
	assert.InDelta(t, 500, state["upf_session_count"], 10)
}

func TestEngine_StateNonexistentInstance(t *testing.T) {
	eng := engine.New(makeScenario())
	state := eng.State("does-not-exist")
	assert.Nil(t, state)
}

func TestEngine_PrometheusOutputNonexistentInstance(t *testing.T) {
	eng := engine.New(makeScenario())
	output := eng.PrometheusOutput("does-not-exist")
	assert.Nil(t, output)
}

func TestEngine_PrometheusOutputSorted(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{Name: "z_metric", Type: "gauge", Baseline: 1},
			{Name: "a_metric", Type: "gauge", Baseline: 2},
			{Name: "m_metric", Type: "gauge", Baseline: 3},
		},
	}

	eng := engine.New(makeScenario(nf))
	output := string(eng.PrometheusOutput("amf-001"))

	// Verify metrics appear in sorted order.
	aIdx := strings.Index(output, "a_metric")
	mIdx := strings.Index(output, "m_metric")
	zIdx := strings.Index(output, "z_metric")

	assert.Greater(t, mIdx, aIdx, "m_metric should appear after a_metric")
	assert.Greater(t, zIdx, mIdx, "z_metric should appear after m_metric")
}

func TestEngine_GetNF(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
	}

	eng := engine.New(makeScenario(nf))

	found := eng.GetNF("amf-001")
	require.NotNil(t, found)
	assert.Equal(t, "AMF", found.Type)
	assert.Equal(t, "free5gc", found.Vendor)

	notFound := eng.GetNF("amf-999")
	assert.Nil(t, notFound)
}

func TestEngine_LabelScopedEvent(t *testing.T) {
	// Two counters share the same metric name but differ by label.
	// The event targets only the "attempted" label set.
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "open5gs",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "open5gs_amf_registration_total",
				Labels:        map[string]string{"status": "attempted"},
				Type:          "counter",
				Baseline:      0,
				RatePerSecond: 10,
			},
			{
				Name:          "open5gs_amf_registration_total",
				Labels:        map[string]string{"status": "failed"},
				Type:          "counter",
				Baseline:      0,
				RatePerSecond: 1,
			},
		},
		Events: []engine.Event{
			{
				Name:      "storm",
				StartSec:  0,
				DurationS: 60,
				Metric:    "open5gs_amf_registration_total",
				Labels:    map[string]string{"status": "attempted"},
				RateScale: 50,
			},
		},
	}

	eng := engine.New(makeScenario(nf))
	eng.Advance(10 * time.Second)

	state := eng.State("amf-001")
	require.NotNil(t, state)

	// Attempted: scaled by 50x → 10 * 50 * 10 = 5000
	attempted := state["open5gs_amf_registration_total:status=attempted"]
	assert.Equal(t, 5000.0, attempted)

	// Failed: NOT scaled → 1 * 10 = 10
	failed := state["open5gs_amf_registration_total:status=failed"]
	assert.Equal(t, 10.0, failed)
}

func TestEngine_LabelScopedEvent_NoLabels_MatchesAll(t *testing.T) {
	// Event without labels should scale all metrics with that name (backward compat).
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "open5gs",
		InstanceID: "amf-001",
		Protocol:   "prometheus",
		Port:       9090,
		Metrics: []engine.BaseMetric{
			{
				Name:          "some_counter",
				Labels:        map[string]string{"variant": "a"},
				Type:          "counter",
				Baseline:      0,
				RatePerSecond: 10,
			},
			{
				Name:          "some_counter",
				Labels:        map[string]string{"variant": "b"},
				Type:          "counter",
				Baseline:      0,
				RatePerSecond: 10,
			},
		},
		Events: []engine.Event{
			{
				Name:      "spike",
				StartSec:  0,
				DurationS: 60,
				Metric:    "some_counter",
				RateScale: 5,
			},
		},
	}

	eng := engine.New(makeScenario(nf))
	eng.Advance(10 * time.Second)

	state := eng.State("amf-001")
	require.NotNil(t, state)

	// Both variants scaled by 5x → 10 * 5 * 10 = 500 each.
	assert.Equal(t, 500.0, state["some_counter:variant=a"])
	assert.Equal(t, 500.0, state["some_counter:variant=b"])
}

func TestEngine_GNMIValues(t *testing.T) {
	nf := engine.SimulatedNF{
		Type:       "AMF",
		Vendor:     "free5gc",
		InstanceID: "amf-001",
		Protocol:   "gnmi",
		Port:       50051,
		Metrics: []engine.BaseMetric{
			{Name: "amf_connected_ue", Type: "gauge", Baseline: 950},
		},
	}

	eng := engine.New(makeScenario(nf))

	pathMap := map[string]string{
		"/amf/ue/connected": "amf_connected_ue",
	}
	vals := eng.GNMIValues("amf-001", pathMap)
	require.NotNil(t, vals)
	assert.Equal(t, 950.0, vals["/amf/ue/connected"])

	// Non-existent instance returns nil.
	assert.Nil(t, eng.GNMIValues("does-not-exist", pathMap))
}
