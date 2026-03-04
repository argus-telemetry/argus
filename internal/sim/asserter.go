// Package sim provides test and certification utilities for scenario validation.
//
// ScenarioAsserter evaluates whether a scenario's expected_events block was
// satisfied by the correlation events observed during execution. Designed as
// the core engine for argus-certify (v0.3 CLI).
package sim

import (
	"fmt"
	"sort"
	"time"

	"github.com/argus-5g/argus/internal/correlator"
	"github.com/argus-5g/argus/simulator/engine"
)

// ScenarioAsserter evaluates expected events from a scenario against
// observed correlation events.
type ScenarioAsserter interface {
	Evaluate(scenario engine.Scenario, events <-chan correlator.CorrelationEvent) AssertResult
}

// AssertResult captures the outcome of evaluating a scenario's expected events.
type AssertResult struct {
	Passed   bool
	Failures []AssertFailure
	Duration time.Duration
}

// AssertFailure describes a single failed assertion.
type AssertFailure struct {
	Rule           string
	ExpectedWithin time.Duration
	ActualAt       time.Duration // 0 if event never arrived
	Reason         string
}

// Asserter implements ScenarioAsserter with configurable timeout behavior.
type Asserter struct{}

// NewAsserter creates a new Asserter.
func NewAsserter() *Asserter {
	return &Asserter{}
}

// Evaluate blocks until all expected events are observed or their deadlines expire.
// For scenarios with expected_events: [], it waits for the maximum within_seconds
// (or 5s if no events) and asserts zero events were produced.
func (a *Asserter) Evaluate(scenario engine.Scenario, events <-chan correlator.CorrelationEvent) AssertResult {
	start := time.Now()

	if len(scenario.ExpectedEvents) == 0 {
		return a.evaluateZeroEvents(events, start)
	}

	return a.evaluateExpectedEvents(scenario.ExpectedEvents, events, start)
}

func (a *Asserter) evaluateZeroEvents(events <-chan correlator.CorrelationEvent, start time.Time) AssertResult {
	// Wait 5s and assert no events arrive.
	timeout := 5 * time.Second
	deadline := time.After(timeout)

	var unexpected []correlator.CorrelationEvent
	for {
		select {
		case ev := <-events:
			unexpected = append(unexpected, ev)
		case <-deadline:
			dur := time.Since(start)
			if len(unexpected) > 0 {
				var failures []AssertFailure
				for _, ev := range unexpected {
					failures = append(failures, AssertFailure{
						Rule:   ev.RuleName,
						Reason: fmt.Sprintf("unexpected event %s (severity=%s) during steady state", ev.RuleName, ev.Severity),
					})
				}
				return AssertResult{Passed: false, Failures: failures, Duration: dur}
			}
			return AssertResult{Passed: true, Duration: dur}
		}
	}
}

func (a *Asserter) evaluateExpectedEvents(
	expected []engine.ExpectedEvent,
	events <-chan correlator.CorrelationEvent,
	start time.Time,
) AssertResult {
	// Track which expectations are satisfied.
	type expectation struct {
		exp     engine.ExpectedEvent
		matched bool
		at      time.Duration
	}
	expectations := make([]expectation, len(expected))
	for i, e := range expected {
		expectations[i] = expectation{exp: e}
	}

	// Global timeout: max(within_seconds) across all expectations.
	maxTimeout := 0
	for _, e := range expected {
		if e.WithinSeconds > maxTimeout {
			maxTimeout = e.WithinSeconds
		}
	}
	deadline := time.After(time.Duration(maxTimeout) * time.Second)

	for {
		allMatched := true
		for _, exp := range expectations {
			if !exp.matched {
				allMatched = false
				break
			}
		}
		if allMatched {
			return AssertResult{Passed: true, Duration: time.Since(start)}
		}

		select {
		case ev := <-events:
			elapsed := time.Since(start)
			for i := range expectations {
				if expectations[i].matched {
					continue
				}
				if matchesExpectation(ev, expectations[i].exp) {
					expectations[i].matched = true
					expectations[i].at = elapsed
				}
			}
		case <-deadline:
			dur := time.Since(start)
			var failures []AssertFailure
			for _, exp := range expectations {
				if !exp.matched {
					failures = append(failures, AssertFailure{
						Rule:           exp.exp.Rule,
						ExpectedWithin: time.Duration(exp.exp.WithinSeconds) * time.Second,
						Reason:         fmt.Sprintf("expected %s (severity=%s) within %ds, never observed", exp.exp.Rule, exp.exp.Severity, exp.exp.WithinSeconds),
					})
				}
			}
			return AssertResult{Passed: false, Failures: failures, Duration: dur}
		}
	}
}

func matchesExpectation(ev correlator.CorrelationEvent, exp engine.ExpectedEvent) bool {
	if ev.RuleName != exp.Rule {
		return false
	}
	if ev.Severity != exp.Severity {
		return false
	}
	if len(exp.AffectedNFs) > 0 {
		// All expected NFs must be present in the event.
		eventNFs := make(map[string]bool)
		for _, nf := range ev.AffectedNFs {
			eventNFs[nf] = true
		}
		for _, nf := range exp.AffectedNFs {
			if !eventNFs[nf] {
				return false
			}
		}
	}
	return true
}

// FormatResult produces a human-readable summary of an AssertResult.
func FormatResult(scenario string, result AssertResult) string {
	if result.Passed {
		return fmt.Sprintf("PASS: %s (%s)", scenario, result.Duration.Truncate(time.Millisecond))
	}

	msg := fmt.Sprintf("FAIL: %s (%s)\n", scenario, result.Duration.Truncate(time.Millisecond))
	// Sort failures by rule name for deterministic output.
	sort.Slice(result.Failures, func(i, j int) bool {
		return result.Failures[i].Rule < result.Failures[j].Rule
	})
	for _, f := range result.Failures {
		msg += fmt.Sprintf("  - %s\n", f.Reason)
	}
	return msg
}
