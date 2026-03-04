package correlator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/normalizer"
)

// mockRule records Evaluate calls and returns configured events.
type mockRule struct {
	name     string
	severity string
	events   []CorrelationEvent
	called   int
}

func (r *mockRule) Name() string     { return r.name }
func (r *mockRule) Severity() string { return r.severity }
func (r *mockRule) Evaluate(w WindowSnapshot) []CorrelationEvent {
	r.called++
	return r.events
}

func TestEngine_IngestAndSnapshot(t *testing.T) {
	e := NewEngine(30*time.Second, nil)

	now := time.Now()
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     100,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.success_rate",
		Value:     0.95,
		Timestamp: now.Add(time.Second),
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})

	snapshots := e.Snapshots(now.Add(2 * time.Second))
	require.Len(t, snapshots, 1)

	snap := snapshots[0]
	assert.Equal(t, "310-260", snap.PLMN)
	assert.Len(t, snap.Samples["argus.5g.amf"], 2)
	assert.Len(t, snap.Samples["argus.5g.amf"]["registration.attempt_count"], 1)
	assert.Equal(t, 100.0, snap.Samples["argus.5g.amf"]["registration.attempt_count"][0].Value)
}

func TestEngine_WindowExpiry(t *testing.T) {
	e := NewEngine(5*time.Second, nil)

	old := time.Now().Add(-10 * time.Second)
	recent := time.Now()

	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     100,
		Timestamp: old,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     200,
		Timestamp: recent,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})

	snapshots := e.Snapshots(recent)
	require.Len(t, snapshots, 1)

	samples := snapshots[0].Samples["argus.5g.amf"]["registration.attempt_count"]
	assert.Len(t, samples, 1, "expired sample should be evicted")
	assert.Equal(t, 200.0, samples[0].Value)
}

func TestEngine_EvaluateRules(t *testing.T) {
	rule := &mockRule{
		name:     "TestRule",
		severity: "warning",
		events: []CorrelationEvent{
			{RuleName: "TestRule", Severity: "warning", PLMN: "310-260"},
		},
	}

	e := NewEngine(30*time.Second, []CorrelationRule{rule})

	now := time.Now()
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     100,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})

	events := e.EvaluateAll(now)
	assert.Len(t, events, 1)
	assert.Equal(t, "TestRule", events[0].RuleName)
	assert.Equal(t, 1, rule.called)
}

func TestEngine_MultiplePLMNs(t *testing.T) {
	e := NewEngine(30*time.Second, nil)

	now := time.Now()
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     100,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "310-260",
		},
	})
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     200,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID: "311-480",
		},
	})

	snapshots := e.Snapshots(now)
	assert.Len(t, snapshots, 2, "should have separate windows per PLMN")
}

func TestEngine_SliceIDKeying(t *testing.T) {
	e := NewEngine(30*time.Second, nil)

	now := time.Now()
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     100,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID:  "310-260",
			SliceID: &normalizer.SliceID{SST: 1, SD: "000001"},
		},
	})
	e.Ingest(normalizer.NormalizedRecord{
		Namespace: "argus.5g.amf",
		KPIName:   "registration.attempt_count",
		Value:     200,
		Timestamp: now,
		Attributes: normalizer.ResourceAttributes{
			PLMNID:  "310-260",
			SliceID: &normalizer.SliceID{SST: 2, SD: "000002"},
		},
	})

	snapshots := e.Snapshots(now)
	assert.Len(t, snapshots, 2, "different slices should have separate windows")
}
