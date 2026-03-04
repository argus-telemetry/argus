// Package correlator detects cross-NF anomaly patterns by evaluating rules
// against a time-window of normalized KPI samples.
package correlator

import (
	"time"
)

// CorrelationEvent is emitted when a cross-NF anomaly pattern matches.
type CorrelationEvent struct {
	RuleName    string             // e.g. "RegistrationStorm"
	Severity    string             // "critical" | "warning" | "info"
	PLMN        string             // PLMN ID
	SliceID     string             // "SST:SD" or "" if not slice-scoped
	AffectedNFs []string           // e.g. ["AMF", "SMF"]
	Evidence    map[string]float64 // KPI values that triggered the rule
	WindowStart time.Time
	WindowEnd   time.Time
	Timestamp   time.Time
}

// Sample is a single KPI observation in the window.
type Sample struct {
	Value     float64
	Timestamp time.Time
}

// WindowSnapshot provides read access to KPI samples within a time window,
// grouped by namespace and KPI name.
type WindowSnapshot struct {
	Start   time.Time
	End     time.Time
	PLMN    string
	SliceID string
	// Samples[namespace][kpiName] = time-ordered samples within the window.
	Samples map[string]map[string][]Sample
}

// CorrelationRule evaluates a cross-NF anomaly pattern against a window of KPI samples.
type CorrelationRule interface {
	Name() string
	Severity() string
	Evaluate(window WindowSnapshot) []CorrelationEvent
}
