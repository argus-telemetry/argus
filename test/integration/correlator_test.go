//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/correlator"
	"github.com/argus-5g/argus/internal/normalizer"
	"github.com/argus-5g/argus/internal/pipeline"
	"github.com/argus-5g/argus/internal/sim"
	"github.com/argus-5g/argus/simulator/engine"
)

// buildCorrelatorPipeline wires up the same pipeline → correlator integration
// path as main.go:219-285. Returns the event sink channel and a function to
// inject NormalizedRecords.
func buildCorrelatorPipeline(t *testing.T, ctx context.Context, windowSize, evalInterval time.Duration) (
	eventCh <-chan correlator.CorrelationEvent,
	inject func(records []normalizer.NormalizedRecord),
) {
	t.Helper()

	pipe := pipeline.NewChannelPipeline(64)
	t.Cleanup(func() { pipe.Close() })

	rules := []correlator.CorrelationRule{
		&correlator.RegistrationStorm{},
		&correlator.SessionDrop{},
		&correlator.RANCoreDivergence{},
	}
	engine := correlator.NewEngine(windowSize, rules)

	sink := make(chan correlator.CorrelationEvent, 64)
	engine.RegisterEventSink(sink)

	// Ingest goroutine: reads normalized records from pipeline, feeds correlator.
	normCh, err := pipe.Subscribe(ctx, "normalized")
	require.NoError(t, err)

	go func() {
		for data := range normCh {
			var records []normalizer.NormalizedRecord
			if err := json.Unmarshal(data, &records); err != nil {
				continue
			}
			for _, rec := range records {
				engine.Ingest(rec)
			}
		}
	}()

	// Evaluation goroutine: runs rules on a ticker.
	go func() {
		ticker := time.NewTicker(evalInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				engine.EvaluateAll(time.Now())
			}
		}
	}()

	// inject publishes NormalizedRecords to the "normalized" topic.
	inject = func(records []normalizer.NormalizedRecord) {
		data, err := json.Marshal(records)
		if err != nil {
			t.Fatalf("marshal records: %v", err)
		}
		if err := pipe.Publish(ctx, "normalized", data); err != nil {
			t.Fatalf("publish normalized: %v", err)
		}
	}

	return sink, inject
}

// collectEvents drains events from the channel until timeout.
func collectEvents(ch <-chan correlator.CorrelationEvent, timeout time.Duration) []correlator.CorrelationEvent {
	var events []correlator.CorrelationEvent
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-deadline:
			return events
		}
	}
}

func makeRecord(ns, kpi string, value float64, plmn string) normalizer.NormalizedRecord {
	return normalizer.NormalizedRecord{
		Namespace: ns,
		KPIName:   kpi,
		Value:     value,
		Unit:      "ratio",
		Timestamp: time.Now(),
		Attributes: normalizer.ResourceAttributes{
			PLMNID: plmn,
			Vendor: "free5gc",
			NFType: "AMF",
		},
		SchemaVersion: "v1",
	}
}

// TestAlarmStormScenario_FiresRegistrationStorm feeds a registration spike pattern
// (attempt count >3σ above mean + success rate < 0.9) and asserts RegistrationStorm fires.
func TestAlarmStormScenario_FiresRegistrationStorm(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, inject := buildCorrelatorPipeline(t, ctx, 30*time.Second, 200*time.Millisecond)

	plmn := "310-260"

	// Baseline: 10 samples of normal attempt_count with low variance.
	for i := 0; i < 10; i++ {
		inject([]normalizer.NormalizedRecord{
			makeRecord("argus.5g.amf", "registration.attempt_count", float64(10+i%2), plmn),
			makeRecord("argus.5g.amf", "registration.success_rate", 0.99, plmn),
		})
		time.Sleep(20 * time.Millisecond)
	}

	// Spike: attempt count jumps 100x, success rate drops below 0.9.
	inject([]normalizer.NormalizedRecord{
		makeRecord("argus.5g.amf", "registration.attempt_count", 1000, plmn),
		makeRecord("argus.5g.amf", "registration.success_rate", 0.45, plmn),
	})

	events := collectEvents(eventCh, 3*time.Second)

	var stormEvents []correlator.CorrelationEvent
	for _, ev := range events {
		if ev.RuleName == "RegistrationStorm" {
			stormEvents = append(stormEvents, ev)
		}
	}
	require.NotEmpty(t, stormEvents, "RegistrationStorm should have fired")

	ev := stormEvents[0]
	assert.Equal(t, "critical", ev.Severity)
	assert.Equal(t, plmn, ev.PLMN)
	assert.Contains(t, ev.AffectedNFs, "AMF")
	assert.Contains(t, ev.Evidence, "registration.attempt_count")
	assert.Contains(t, ev.Evidence, "registration.success_rate")
}

// TestSliceSLABreach_FiresSessionDrop feeds a session drop pattern
// (SMF session count drops >20% while AMF success rate stays healthy)
// and asserts SessionDrop fires.
func TestSliceSLABreach_FiresSessionDrop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, inject := buildCorrelatorPipeline(t, ctx, 30*time.Second, 200*time.Millisecond)

	plmn := "310-260"

	// Baseline: healthy session count.
	for i := 0; i < 5; i++ {
		inject([]normalizer.NormalizedRecord{
			makeRecord("argus.5g.smf", "session.active_count", 500, plmn),
			makeRecord("argus.5g.amf", "registration.success_rate", 0.99, plmn),
		})
		time.Sleep(20 * time.Millisecond)
	}

	// Drop: sessions fall >20%, AMF stays healthy (isolating SMF fault).
	inject([]normalizer.NormalizedRecord{
		makeRecord("argus.5g.smf", "session.active_count", 300, plmn),
		makeRecord("argus.5g.amf", "registration.success_rate", 0.98, plmn),
	})

	events := collectEvents(eventCh, 3*time.Second)

	var dropEvents []correlator.CorrelationEvent
	for _, ev := range events {
		if ev.RuleName == "SessionDrop" {
			dropEvents = append(dropEvents, ev)
		}
	}
	require.NotEmpty(t, dropEvents, "SessionDrop should have fired")

	ev := dropEvents[0]
	assert.Equal(t, "warning", ev.Severity)
	assert.Equal(t, plmn, ev.PLMN)
	assert.Contains(t, ev.AffectedNFs, "SMF")
}

// TestRANCoreDivergence_FiresOnGNBDrop feeds a RAN-core divergence pattern
// (gNB DL throughput drops >30% while UPF DL stays stable) and asserts
// RANCoreDivergence fires with gNB in AffectedNFs but NOT UPF.
func TestRANCoreDivergence_FiresOnGNBDrop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, inject := buildCorrelatorPipeline(t, ctx, 30*time.Second, 200*time.Millisecond)

	plmn := "310-260"

	// Baseline: healthy gNB and UPF throughput.
	for i := 0; i < 5; i++ {
		inject([]normalizer.NormalizedRecord{
			makeRecord("argus.5g.gnb", "throughput.downlink_bps", 1e9, plmn),
			makeRecord("argus.5g.upf", "throughput.downlink_bps", 2e9, plmn),
		})
		time.Sleep(20 * time.Millisecond)
	}

	// gNB drops >30%, UPF stable.
	inject([]normalizer.NormalizedRecord{
		makeRecord("argus.5g.gnb", "throughput.downlink_bps", 5e8, plmn),
		makeRecord("argus.5g.upf", "throughput.downlink_bps", 2e9, plmn),
	})

	events := collectEvents(eventCh, 3*time.Second)

	var divEvents []correlator.CorrelationEvent
	for _, ev := range events {
		if ev.RuleName == "RANCoreDivergence" {
			divEvents = append(divEvents, ev)
		}
	}
	require.NotEmpty(t, divEvents, "RANCoreDivergence should have fired")

	ev := divEvents[0]
	assert.Equal(t, "warning", ev.Severity)
	assert.Contains(t, ev.AffectedNFs, "gNB")
	assert.NotContains(t, ev.AffectedNFs, "UPF", "UPF should NOT be in AffectedNFs — fault is in RAN")
}

// TestNoFalsePositives_SteadyState feeds steady-state KPIs for 2x window duration
// and asserts zero CorrelationEvents. This is the most important test — false
// positives destroy operator trust.
func TestNoFalsePositives_SteadyState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	windowSize := 2 * time.Second
	eventCh, inject := buildCorrelatorPipeline(t, ctx, windowSize, 200*time.Millisecond)

	plmn := "310-260"

	// Feed stable data for 2x window size.
	deadline := time.After(2 * windowSize)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-deadline:
			break loop
		case <-ticker.C:
			inject([]normalizer.NormalizedRecord{
				makeRecord("argus.5g.amf", "registration.attempt_count", 15, plmn),
				makeRecord("argus.5g.amf", "registration.success_rate", 0.995, plmn),
				makeRecord("argus.5g.smf", "session.active_count", 480, plmn),
				makeRecord("argus.5g.gnb", "throughput.downlink_bps", 1e9, plmn),
				makeRecord("argus.5g.upf", "throughput.downlink_bps", 2e9, plmn),
			})
		}
	}

	// Drain any events — there should be zero.
	events := collectEvents(eventCh, 1*time.Second)
	assert.Empty(t, events, "steady state should produce zero correlation events, got %d", len(events))
}

// --- Asserter-based tests: validate scenario YAML expected_events ---

// TestAsserter_AlarmStorm validates the alarm_storm scenario's expected_events
// using the ScenarioAsserter interface.
func TestAsserter_AlarmStorm(t *testing.T) {
	scenario := engine.Scenario{
		Name: "alarm_storm",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "RegistrationStorm", Severity: "critical", WithinSeconds: 10, AffectedNFs: []string{"AMF"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, inject := buildCorrelatorPipeline(t, ctx, 30*time.Second, 200*time.Millisecond)

	// Also build a fresh sink for the asserter (same engine pattern).
	pipe2 := pipeline.NewChannelPipeline(64)
	t.Cleanup(func() { pipe2.Close() })

	rules := []correlator.CorrelationRule{
		&correlator.RegistrationStorm{},
		&correlator.SessionDrop{},
		&correlator.RANCoreDivergence{},
	}
	eng := correlator.NewEngine(30*time.Second, rules)

	sink := make(chan correlator.CorrelationEvent, 64)
	eng.RegisterEventSink(sink)

	plmn := "310-260"

	// Feed baseline then spike (same pattern as TestAlarmStormScenario).
	go func() {
		for i := 0; i < 10; i++ {
			eng.Ingest(makeRecord("argus.5g.amf", "registration.attempt_count", float64(10+i%2), plmn))
			eng.Ingest(makeRecord("argus.5g.amf", "registration.success_rate", 0.99, plmn))
			time.Sleep(20 * time.Millisecond)
		}
		eng.Ingest(makeRecord("argus.5g.amf", "registration.attempt_count", 1000, plmn))
		eng.Ingest(makeRecord("argus.5g.amf", "registration.success_rate", 0.45, plmn))

		// Trigger evaluation.
		for i := 0; i < 10; i++ {
			eng.EvaluateAll(time.Now())
			time.Sleep(200 * time.Millisecond)
		}
	}()

	// Suppress unused variable warning for inject — it's used in the setup.
	_ = inject

	asserter := sim.NewAsserter()
	result := asserter.Evaluate(scenario, sink)
	assert.True(t, result.Passed, "asserter should pass: %s", sim.FormatResult("alarm_storm", result))
}

// TestAsserter_SteadyState validates the steady_state scenario's expected_events: []
// using the ScenarioAsserter interface.
func TestAsserter_SteadyState(t *testing.T) {
	scenario := engine.Scenario{
		Name:           "steady_state",
		ExpectedEvents: []engine.ExpectedEvent{},
	}

	rules := []correlator.CorrelationRule{
		&correlator.RegistrationStorm{},
		&correlator.SessionDrop{},
		&correlator.RANCoreDivergence{},
	}
	eng := correlator.NewEngine(2*time.Second, rules)

	sink := make(chan correlator.CorrelationEvent, 64)
	eng.RegisterEventSink(sink)

	plmn := "310-260"

	// Feed stable data concurrently.
	go func() {
		for i := 0; i < 30; i++ {
			eng.Ingest(makeRecord("argus.5g.amf", "registration.attempt_count", 15, plmn))
			eng.Ingest(makeRecord("argus.5g.amf", "registration.success_rate", 0.995, plmn))
			eng.Ingest(makeRecord("argus.5g.smf", "session.active_count", 480, plmn))
			eng.EvaluateAll(time.Now())
			time.Sleep(100 * time.Millisecond)
		}
	}()

	asserter := sim.NewAsserter()
	result := asserter.Evaluate(scenario, sink)
	assert.True(t, result.Passed, "asserter should pass for steady state: %s", sim.FormatResult("steady_state", result))
}
