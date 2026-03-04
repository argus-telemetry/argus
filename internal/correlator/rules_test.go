package correlator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSnapshot(now time.Time, samples map[string]map[string][]float64) WindowSnapshot {
	s := WindowSnapshot{
		Start:   now.Add(-30 * time.Second),
		End:     now,
		PLMN:    "310-260",
		SliceID: "1:000001",
		Samples: make(map[string]map[string][]Sample),
	}
	for ns, kpis := range samples {
		s.Samples[ns] = make(map[string][]Sample)
		for kpi, vals := range kpis {
			for i, v := range vals {
				s.Samples[ns][kpi] = append(s.Samples[ns][kpi], Sample{
					Value:     v,
					Timestamp: now.Add(-time.Duration(len(vals)-i) * time.Second),
				})
			}
		}
	}
	return s
}

// --- RegistrationStorm ---

func TestRegistrationStorm_Triggers(t *testing.T) {
	rule := &RegistrationStorm{}
	now := time.Now()

	// Need enough baseline samples so the spike doesn't inflate mean/stddev
	// past the point where 3-sigma is unreachable. 20 baseline samples ~100,
	// then a single 10000 spike still exceeds 3-sigma of the full population.
	attempts := []float64{100, 105, 98, 102, 100, 103, 97, 101, 99, 104, 98, 102, 100, 101, 99, 100, 102, 98, 103, 97, 10000}
	successRates := []float64{0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.3}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.amf": {
			"registration.attempt_count": attempts,
			"registration.success_rate":  successRates,
		},
	})

	events := rule.Evaluate(snap)
	require.Len(t, events, 1)
	assert.Equal(t, "RegistrationStorm", events[0].RuleName)
	assert.Equal(t, "critical", events[0].Severity)
	assert.Contains(t, events[0].AffectedNFs, "AMF")
}

func TestRegistrationStorm_NoTrigger_NormalTraffic(t *testing.T) {
	rule := &RegistrationStorm{}
	now := time.Now()

	attempts := []float64{100, 105, 98, 102, 100, 103, 97, 101}
	successRates := []float64{0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.amf": {
			"registration.attempt_count": attempts,
			"registration.success_rate":  successRates,
		},
	})

	events := rule.Evaluate(snap)
	assert.Empty(t, events)
}

// --- SessionDrop ---

func TestSessionDrop_Triggers(t *testing.T) {
	rule := &SessionDrop{}
	now := time.Now()

	sessions := []float64{500, 495, 502, 498, 500, 380, 350, 320}
	amfRate := []float64{0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99, 0.99}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.smf": {
			"session.active_count": sessions,
		},
		"argus.5g.amf": {
			"registration.success_rate": amfRate,
		},
	})

	events := rule.Evaluate(snap)
	require.Len(t, events, 1)
	assert.Equal(t, "SessionDrop", events[0].RuleName)
	assert.Equal(t, "warning", events[0].Severity)
	assert.Contains(t, events[0].AffectedNFs, "SMF")
}

func TestSessionDrop_NoTrigger_AMFAlsoFailing(t *testing.T) {
	rule := &SessionDrop{}
	now := time.Now()

	sessions := []float64{500, 495, 502, 498, 500, 380, 350, 320}
	amfRate := []float64{0.99, 0.99, 0.99, 0.99, 0.99, 0.5, 0.4, 0.3}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.smf": {
			"session.active_count": sessions,
		},
		"argus.5g.amf": {
			"registration.success_rate": amfRate,
		},
	})

	events := rule.Evaluate(snap)
	assert.Empty(t, events, "should not fire when AMF is also degraded")
}

// --- RANCoreDivergence ---

func TestRANCoreDivergence_Triggers(t *testing.T) {
	rule := &RANCoreDivergence{}
	now := time.Now()

	gnbDL := []float64{5e8, 4.9e8, 5.1e8, 5e8, 5e8, 2e8, 1.5e8, 1e8}
	upfDL := []float64{5e8, 4.9e8, 5.1e8, 5e8, 5e8, 4.8e8, 5e8, 5.1e8}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.gnb": {
			"throughput.downlink_bps": gnbDL,
		},
		"argus.5g.upf": {
			"throughput.downlink_bps": upfDL,
		},
	})

	events := rule.Evaluate(snap)
	require.Len(t, events, 1)
	assert.Equal(t, "RANCoreDivergence", events[0].RuleName)
	assert.Contains(t, events[0].AffectedNFs, "gNB")
}

func TestRANCoreDivergence_NoTrigger_BothDrop(t *testing.T) {
	rule := &RANCoreDivergence{}
	now := time.Now()

	gnbDL := []float64{5e8, 5e8, 5e8, 5e8, 2e8, 1.5e8, 1e8, 0.5e8}
	upfDL := []float64{5e8, 5e8, 5e8, 5e8, 2e8, 1.5e8, 1e8, 0.5e8}

	snap := makeSnapshot(now, map[string]map[string][]float64{
		"argus.5g.gnb": {
			"throughput.downlink_bps": gnbDL,
		},
		"argus.5g.upf": {
			"throughput.downlink_bps": upfDL,
		},
	})

	events := rule.Evaluate(snap)
	assert.Empty(t, events, "should not fire when both RAN and core drop")
}
