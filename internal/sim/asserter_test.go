package sim

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/argus-5g/argus/internal/correlator"
	"github.com/argus-5g/argus/simulator/engine"
)

func TestEvaluate_MatchesExpectedEvent(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "RegistrationStorm", Severity: "critical", WithinSeconds: 5, AffectedNFs: []string{"AMF"}},
		},
	}

	ch := make(chan correlator.CorrelationEvent, 1)
	ch <- correlator.CorrelationEvent{
		RuleName:    "RegistrationStorm",
		Severity:    "critical",
		AffectedNFs: []string{"AMF"},
	}

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Failures)
}

func TestEvaluate_MismatchedSeverity(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "RegistrationStorm", Severity: "critical", WithinSeconds: 1},
		},
	}

	ch := make(chan correlator.CorrelationEvent, 1)
	ch <- correlator.CorrelationEvent{
		RuleName: "RegistrationStorm",
		Severity: "warning", // wrong severity
	}

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.False(t, result.Passed)
	assert.Len(t, result.Failures, 1)
	assert.Contains(t, result.Failures[0].Reason, "RegistrationStorm")
}

func TestEvaluate_MissingEvent_TimesOut(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "SessionDrop", Severity: "warning", WithinSeconds: 1},
		},
	}

	ch := make(chan correlator.CorrelationEvent) // no events

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.False(t, result.Passed)
	assert.Len(t, result.Failures, 1)
	assert.Equal(t, "SessionDrop", result.Failures[0].Rule)
}

func TestEvaluate_ZeroExpected_NoEvents_Passes(t *testing.T) {
	scenario := engine.Scenario{
		Name:           "steady_state",
		ExpectedEvents: []engine.ExpectedEvent{},
	}

	ch := make(chan correlator.CorrelationEvent) // no events

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.True(t, result.Passed)
}

func TestEvaluate_ZeroExpected_UnexpectedEvent_Fails(t *testing.T) {
	scenario := engine.Scenario{
		Name:           "steady_state",
		ExpectedEvents: []engine.ExpectedEvent{},
	}

	ch := make(chan correlator.CorrelationEvent, 1)
	ch <- correlator.CorrelationEvent{
		RuleName: "RegistrationStorm",
		Severity: "critical",
	}

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.Failures)
	assert.Contains(t, result.Failures[0].Reason, "unexpected")
}

func TestEvaluate_MultipleExpected(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "SessionDrop", Severity: "warning", WithinSeconds: 3, AffectedNFs: []string{"SMF"}},
			{Rule: "RANCoreDivergence", Severity: "warning", WithinSeconds: 3, AffectedNFs: []string{"gNB"}},
		},
	}

	ch := make(chan correlator.CorrelationEvent, 2)
	go func() {
		time.Sleep(100 * time.Millisecond)
		ch <- correlator.CorrelationEvent{RuleName: "SessionDrop", Severity: "warning", AffectedNFs: []string{"SMF"}}
		ch <- correlator.CorrelationEvent{RuleName: "RANCoreDivergence", Severity: "warning", AffectedNFs: []string{"gNB"}}
	}()

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.True(t, result.Passed)
}

func TestEvaluate_AffectedNFsMismatch(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{Rule: "RANCoreDivergence", Severity: "warning", WithinSeconds: 1, AffectedNFs: []string{"gNB", "UPF"}},
		},
	}

	ch := make(chan correlator.CorrelationEvent, 1)
	ch <- correlator.CorrelationEvent{
		RuleName:    "RANCoreDivergence",
		Severity:    "warning",
		AffectedNFs: []string{"gNB"}, // missing UPF
	}

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.False(t, result.Passed)
}

func TestFormatResult_Pass(t *testing.T) {
	result := AssertResult{Passed: true, Duration: 2500 * time.Millisecond}
	out := FormatResult("alarm_storm", result)
	assert.Contains(t, out, "PASS")
	assert.Contains(t, out, "alarm_storm")
}

func TestFormatResult_Fail(t *testing.T) {
	result := AssertResult{
		Passed: false,
		Failures: []AssertFailure{
			{Rule: "SessionDrop", Reason: "expected SessionDrop within 20s, never observed"},
		},
		Duration: 20 * time.Second,
	}
	out := FormatResult("slice_sla_breach", result)
	assert.Contains(t, out, "FAIL")
	assert.Contains(t, out, "SessionDrop")
}
