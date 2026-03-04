package engine

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Engine maintains the simulated state for all NF instances in a scenario.
// Thread-safe: metric state is mutex-protected for concurrent emitter reads.
type Engine struct {
	scenario Scenario
	mu       sync.RWMutex
	elapsed  time.Duration
	rng      *rand.Rand

	// metricState tracks current value per instance per metric key.
	// Key format: "metricName:label1=val1,label2=val2"
	metricState map[string]map[string]float64
}

// New creates an engine from a scenario, initializing metric state to baselines.
func New(scenario Scenario) *Engine {
	e := &Engine{
		scenario:    scenario,
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
		metricState: make(map[string]map[string]float64),
	}

	for _, nf := range scenario.NFs {
		state := make(map[string]float64)
		for _, m := range nf.Metrics {
			key := metricKey(m.Name, m.Labels)
			state[key] = m.Baseline
		}
		e.metricState[nf.InstanceID] = state
	}

	return e
}

// Advance moves the virtual clock forward by d and updates all metric values.
func (e *Engine) Advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	seconds := d.Seconds()
	e.elapsed += d

	for _, nf := range e.scenario.NFs {
		state := e.metricState[nf.InstanceID]
		if state == nil {
			continue
		}

		for _, m := range nf.Metrics {
			key := metricKey(m.Name, m.Labels)

			switch m.Type {
			case "counter":
				rate := m.RatePerSecond
				// Check for active events that scale the rate.
				for _, ev := range nf.Events {
					if ev.Metric == m.Name && e.isEventActive(ev) && ev.RateScale != 0 {
						rate *= ev.RateScale
					}
				}
				state[key] += rate * seconds

			case "gauge":
				// Check for active events that override the value.
				overridden := false
				for _, ev := range nf.Events {
					if ev.Metric == m.Name && e.isEventActive(ev) && ev.Override != 0 {
						state[key] = ev.Override
						overridden = true
						break
					}
				}
				if !overridden {
					jitter := 0.0
					if m.Jitter > 0 {
						jitter = (e.rng.Float64()*2 - 1) * m.Jitter
					}
					state[key] = m.Baseline + jitter
				}
			}
		}
	}
}

// isEventActive returns true if the event's time window contains the current elapsed time.
func (e *Engine) isEventActive(ev Event) bool {
	elapsedSec := int(e.elapsed.Seconds())
	return elapsedSec >= ev.StartSec && elapsedSec < ev.StartSec+ev.DurationS
}

// State returns the current metric values for a given NF instance.
// Keys are "metricName:label1=val1,label2=val2".
func (e *Engine) State(instanceID string) map[string]float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	src := e.metricState[instanceID]
	if src == nil {
		return nil
	}

	// Return a copy to avoid data races.
	cp := make(map[string]float64, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp
}

// PrometheusOutput renders the current state of an NF instance as Prometheus
// exposition format text.
func (e *Engine) PrometheusOutput(instanceID string) []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()

	nf := e.findNF(instanceID)
	if nf == nil {
		return nil
	}

	state := e.metricState[instanceID]
	if state == nil {
		return nil
	}

	var b strings.Builder

	// Group metrics by name for TYPE/HELP comments.
	type metricEntry struct {
		metric BaseMetric
		key    string
		value  float64
	}
	byName := make(map[string][]metricEntry)
	for _, m := range nf.Metrics {
		key := metricKey(m.Name, m.Labels)
		val := state[key]
		byName[m.Name] = append(byName[m.Name], metricEntry{metric: m, key: key, value: val})
	}

	// Sort metric names for deterministic output.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entries := byName[name]
		promType := "gauge"
		if entries[0].metric.Type == "counter" {
			promType = "counter"
		}

		fmt.Fprintf(&b, "# HELP %s Simulated %s metric\n", name, promType)
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, promType)

		for _, entry := range entries {
			if len(entry.metric.Labels) == 0 {
				fmt.Fprintf(&b, "%s %g\n", name, entry.value)
			} else {
				labelStr := formatLabels(entry.metric.Labels)
				fmt.Fprintf(&b, "%s{%s} %g\n", name, labelStr, entry.value)
			}
		}
	}

	return []byte(b.String())
}

// GNMIValues returns current metric values keyed by gNMI path for an NF instance.
// The path mapping comes from the caller (schema gnmi_paths).
func (e *Engine) GNMIValues(instanceID string, pathToMetricKey map[string]string) map[string]float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	state := e.metricState[instanceID]
	if state == nil {
		return nil
	}

	result := make(map[string]float64, len(pathToMetricKey))
	for gnmiPath, mk := range pathToMetricKey {
		if val, ok := state[mk]; ok {
			result[gnmiPath] = val
		}
	}
	return result
}

// GetNF returns the SimulatedNF config for the given instance ID.
func (e *Engine) GetNF(instanceID string) *SimulatedNF {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.findNF(instanceID)
}

// GetScenario returns the loaded scenario.
func (e *Engine) GetScenario() *Scenario {
	return &e.scenario
}

func (e *Engine) findNF(instanceID string) *SimulatedNF {
	for i := range e.scenario.NFs {
		if e.scenario.NFs[i].InstanceID == instanceID {
			return &e.scenario.NFs[i]
		}
	}
	return nil
}

// metricKey builds the state map key from a metric name and its labels.
func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	// Sort label keys for deterministic key generation.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return name + ":" + strings.Join(parts, ",")
}

// formatLabels renders labels as Prometheus label syntax: key="value",key2="value2"
func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return strings.Join(parts, ",")
}
