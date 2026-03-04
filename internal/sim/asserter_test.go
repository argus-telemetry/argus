package sim

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// --- Field-level assertion tests ---

func ptrFloat(v float64) *float64 { return &v }

func TestEvidenceChecks_GT(t *testing.T) {
	a := engine.EvidenceAssertion{GT: ptrFloat(0.0)}
	checks := evidenceChecks("rate", 3.2, a)
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed)
	assert.Equal(t, "gt", checks[0].Op)
}

func TestEvidenceChecks_GT_Fail(t *testing.T) {
	a := engine.EvidenceAssertion{GT: ptrFloat(5.0)}
	checks := evidenceChecks("rate", 3.2, a)
	require.Len(t, checks, 1)
	assert.False(t, checks[0].Passed)
}

func TestEvidenceChecks_LT(t *testing.T) {
	a := engine.EvidenceAssertion{LT: ptrFloat(0.9)}
	checks := evidenceChecks("success_rate", 0.71, a)
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed)
}

func TestEvidenceChecks_LT_Fail(t *testing.T) {
	a := engine.EvidenceAssertion{LT: ptrFloat(0.5)}
	checks := evidenceChecks("success_rate", 0.71, a)
	require.Len(t, checks, 1)
	assert.False(t, checks[0].Passed)
}

func TestEvidenceChecks_GTE(t *testing.T) {
	a := engine.EvidenceAssertion{GTE: ptrFloat(3.0)}
	checks := evidenceChecks("x", 3.0, a)
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed, "3.0 >= 3.0 should pass")
}

func TestEvidenceChecks_LTE(t *testing.T) {
	a := engine.EvidenceAssertion{LTE: ptrFloat(3.0)}
	checks := evidenceChecks("x", 3.0, a)
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed, "3.0 <= 3.0 should pass")
}

func TestEvidenceChecks_EQ(t *testing.T) {
	a := engine.EvidenceAssertion{EQ: ptrFloat(42.0)}
	checks := evidenceChecks("x", 42.0, a)
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed)
}

func TestEvidenceChecks_EQ_Fail(t *testing.T) {
	a := engine.EvidenceAssertion{EQ: ptrFloat(42.0)}
	checks := evidenceChecks("x", 41.0, a)
	require.Len(t, checks, 1)
	assert.False(t, checks[0].Passed)
}

func TestEvidenceChecks_Combined(t *testing.T) {
	// Range check: gt 0 AND lt 1
	a := engine.EvidenceAssertion{GT: ptrFloat(0.0), LT: ptrFloat(1.0)}
	checks := evidenceChecks("ratio", 0.5, a)
	require.Len(t, checks, 2)
	assert.True(t, checks[0].Passed)
	assert.True(t, checks[1].Passed)
}

func TestCheckFieldAssertions_Evidence(t *testing.T) {
	ev := correlator.CorrelationEvent{
		RuleName:    "RegistrationStorm",
		Severity:    "critical",
		AffectedNFs: []string{"AMF"},
		Evidence: map[string]float64{
			"registration.attempt_rate":  3.2,
			"registration.success_rate": 0.71,
		},
	}
	exp := engine.ExpectedEvent{
		Rule:        "RegistrationStorm",
		Severity:    "critical",
		AffectedNFs: []string{"AMF"},
		Evidence: map[string]engine.EvidenceAssertion{
			"registration.attempt_rate":  {GT: ptrFloat(0.0)},
			"registration.success_rate": {LT: ptrFloat(0.9)},
		},
	}

	checks, allPassed := checkFieldAssertions(ev, exp)
	assert.True(t, allPassed)
	// severity + affected_nfs + 2 evidence checks = 4
	assert.GreaterOrEqual(t, len(checks), 4)
}

func TestCheckFieldAssertions_MissingEvidence(t *testing.T) {
	ev := correlator.CorrelationEvent{
		RuleName: "X",
		Severity: "critical",
		Evidence: map[string]float64{},
	}
	exp := engine.ExpectedEvent{
		Rule:     "X",
		Severity: "critical",
		Evidence: map[string]engine.EvidenceAssertion{
			"missing_key": {GT: ptrFloat(0.0)},
		},
	}

	_, allPassed := checkFieldAssertions(ev, exp)
	assert.False(t, allPassed)
}

func TestEvaluate_WithEvidenceAssertions(t *testing.T) {
	scenario := engine.Scenario{
		Name: "test",
		ExpectedEvents: []engine.ExpectedEvent{
			{
				Rule:          "RegistrationStorm",
				Severity:      "critical",
				WithinSeconds: 2,
				AffectedNFs:   []string{"AMF"},
				Evidence: map[string]engine.EvidenceAssertion{
					"registration.attempt_rate": {GT: ptrFloat(0.0)},
				},
			},
		},
	}

	ch := make(chan correlator.CorrelationEvent, 1)
	ch <- correlator.CorrelationEvent{
		RuleName:    "RegistrationStorm",
		Severity:    "critical",
		AffectedNFs: []string{"AMF"},
		Evidence:    map[string]float64{"registration.attempt_rate": 5.0},
	}

	a := NewAsserter()
	result := a.Evaluate(scenario, ch)
	assert.True(t, result.Passed)
	assert.NotEmpty(t, result.Details)
	assert.Equal(t, 1, result.PassCount)
}

func TestAssertResult_JSON(t *testing.T) {
	result := AssertResult{
		Passed:    true,
		Scenario:  "test",
		Total:     1,
		PassCount: 1,
		Duration:  2 * time.Second,
		Details: []AssertDetail{
			{Rule: "X", Passed: true, Elapsed: time.Second},
		},
	}
	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded AssertResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Passed)
	assert.Equal(t, "test", decoded.Scenario)
	assert.Len(t, decoded.Details, 1)
}
