package normalizer

import (
	"fmt"
	"strings"
	"sync"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/normalizer/formula"
	"github.com/argus-5g/argus/internal/normalizer/gnmiparser"
	"github.com/argus-5g/argus/internal/normalizer/promparser"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/argus-5g/argus/internal/schema/dsl"
)

// Engine normalizes raw vendor telemetry into the unified Argus 5G schema.
// Thread-safe: counter store access is mutex-protected for concurrent Normalize
// calls from multiple collector goroutines.
type Engine struct {
	registry *schema.Registry
	store    CounterStore
	mu       sync.Mutex
}

// NewEngine creates a normalization engine backed by the given schema registry.
// If store is nil, a volatile MemoryStore is used (state lost on restart).
func NewEngine(registry *schema.Registry, store CounterStore) *Engine {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Engine{
		registry: registry,
		store:    store,
	}
}

// sourceKey builds the counter-state lookup key for a given source.
// Format: "vendor:nfType:instanceID" — uniquely identifies a telemetry stream
// so counter deltas track per-instance state independently.
func sourceKey(src collector.SourceInfo) string {
	return src.Vendor + ":" + src.NFType + ":" + src.InstanceID
}

// namespaceFor constructs the schema namespace from an NF type.
// Convention: "argus.5g." + lowercase(nfType).
func namespaceFor(nfType string) string {
	return "argus.5g." + strings.ToLower(nfType)
}

// CanHandle returns true if the registry has a schema and vendor mapping
// for this record's vendor + NFType combination.
func (e *Engine) CanHandle(r collector.RawRecord) bool {
	ns := namespaceFor(r.Source.NFType)
	_, err := e.registry.GetSchema(ns)
	if err != nil {
		return false
	}
	// Verify the vendor has mappings in this schema.
	s, _ := e.registry.GetSchema(ns)
	_, hasVendor := s.Mappings[r.Source.Vendor]
	return hasVendor
}

// Normalize transforms a RawRecord into NormalizedRecords following the schema's
// evaluation order. Base KPIs are resolved from parsed metrics; derived KPIs are
// computed from resolved base values via formula evaluation.
//
// Returns error for total failures (unsupported protocol, unparseable payload,
// missing schema). Individual KPI failures are captured in NormalizeResult.Partial.
func (e *Engine) Normalize(r collector.RawRecord) (NormalizeResult, error) {
	ns := namespaceFor(r.Source.NFType)
	nfSchema, err := e.registry.GetSchema(ns)
	if err != nil {
		return NormalizeResult{}, fmt.Errorf("no schema for namespace %q: %w", ns, err)
	}

	if _, hasVendor := nfSchema.Mappings[r.Source.Vendor]; !hasVendor {
		return NormalizeResult{}, fmt.Errorf("no mapping for vendor %q in namespace %q",
			r.Source.Vendor, ns)
	}

	var parsed []promparser.ParsedMetric
	switch r.Protocol {
	case collector.ProtocolPrometheus:
		parsed, err = promparser.Parse(r.Payload)
		if err != nil {
			return NormalizeResult{}, fmt.Errorf("parse prometheus payload: %w", err)
		}
	case collector.ProtocolGNMI:
		parsed, err = gnmiparser.Parse(r.Payload)
		if err != nil {
			return NormalizeResult{}, fmt.Errorf("parse gnmi payload: %w", err)
		}
	default:
		return NormalizeResult{}, fmt.Errorf("unsupported protocol %q", r.Protocol)
	}

	// Index parsed metrics by name for O(1) lookup during KPI resolution.
	metricsByName := make(map[string][]promparser.ParsedMetric, len(parsed))
	for _, pm := range parsed {
		metricsByName[pm.Name] = append(metricsByName[pm.Name], pm)
	}

	evalOrder := e.registry.EvaluationOrder(ns)
	kpiDefs := indexKPIDefs(nfSchema.KPIs)

	// resolvedValues tracks successfully resolved KPI values for derived formula evaluation.
	resolvedValues := make(map[string]float64, len(evalOrder))
	// failedKPIs tracks which KPIs failed so derived KPIs can propagate failures.
	failedKPIs := make(map[string]NormalizeError)

	var result NormalizeResult

	sk := sourceKey(r.Source)

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, kpiName := range evalOrder {
		kpi, ok := kpiDefs[kpiName]
		if !ok {
			continue
		}

		if kpi.Derived {
			val, nerr := e.evaluateDerived(kpi, resolvedValues, failedKPIs)
			if nerr != nil {
				failedKPIs[kpiName] = *nerr
				if !nerr.Unsupported {
					result.Partial = append(result.Partial, *nerr)
				}
				continue
			}
			resolvedValues[kpiName] = val
			result.Records = append(result.Records, NormalizedRecord{
				Namespace:     ns,
				KPIName:       kpiName,
				Value:         val,
				Unit:          kpi.Unit,
				Timestamp:     r.Timestamp,
				Attributes:    buildAttributes(r.Source),
				SpecRef:       kpi.SpecRef,
				SchemaVersion: r.SchemaVersion,
			})
			continue
		}

		// Base KPI: resolve from vendor metric mapping.
		mapping, err := e.registry.GetMapping(ns, r.Source.Vendor, kpiName)
		if err != nil {
			// No mapping for this vendor — the KPI is not supported, not a failure.
			// Mark in failedKPIs so derived KPIs that depend on it are skipped,
			// but don't count it as a partial failure (no noise in telemetry).
			failedKPIs[kpiName] = NormalizeError{
				KPIName:     kpiName,
				Reason:      fmt.Sprintf("no mapping: %v", err),
				Unsupported: true,
			}
			continue
		}

		var val float64
		var matched bool
		var extractedLabels map[string]string
		if mapping.SourceTemplate != "" {
			val, extractedLabels, matched = matchTemplateMetric(parsed, mapping)
		} else {
			candidates := metricsByName[metricLookupKey(mapping, r.Protocol)]
			val, matched = matchMetric(candidates, mapping)
		}
		if !matched {
			// Prometheus counters that haven't been incremented emit no time series.
			// Treat absence as 0 rather than a failure — this allows derived KPIs
			// (e.g. success_rate) to resolve when failure counters haven't fired.
			if mapping.Type == "counter" {
				val = 0
			} else {
				nerr := NormalizeError{
					KPIName: kpiName,
					Raw:     mapping.PrometheusMetric,
					Reason:  fmt.Sprintf("no matching series for metric %q with labels %v", mapping.PrometheusMetric, mapping.Labels),
				}
				failedKPIs[kpiName] = nerr
				result.Partial = append(result.Partial, nerr)
				continue
			}
		}

		// Counter delta computation for counter-type metrics.
		if mapping.Type == "counter" {
			val = e.applyCounterDelta(sk, kpiName, val, mapping.ResetAware)
		}

		resolvedValues[kpiName] = val
		result.Records = append(result.Records, NormalizedRecord{
			Namespace:     ns,
			KPIName:       kpiName,
			Value:         val,
			Unit:          kpi.Unit,
			Timestamp:     r.Timestamp,
			Attributes:    buildAttributes(r.Source),
			Labels:        extractedLabels,
			SpecRef:       kpi.SpecRef,
			SchemaVersion: r.SchemaVersion,
		})
	}

	return result, nil
}

// indexKPIDefs builds a name->KPIDefinition index for fast lookup.
func indexKPIDefs(kpis []schema.KPIDefinition) map[string]*schema.KPIDefinition {
	idx := make(map[string]*schema.KPIDefinition, len(kpis))
	for i := range kpis {
		idx[kpis[i].Name] = &kpis[i]
	}
	return idx
}

// matchTemplateMetric attempts to match parsed metrics against a DSL source_template.
// Iterates all parsed metrics and returns the value, extracted labels, and match result.
// Labels are extracted via LabelExtract rules applied to path segments.
func matchTemplateMetric(parsed []promparser.ParsedMetric, mapping *schema.MetricMapping) (float64, map[string]string, bool) {
	for _, pm := range parsed {
		vars, ok := dsl.MatchTemplate(mapping.SourceTemplate, pm.Name)
		if !ok {
			continue
		}

		labels := make(map[string]string)

		// Copy any matched metric labels.
		for k, v := range pm.Labels {
			labels[k] = v
		}

		// Apply LabelExtract rules: extract values from path segments.
		if len(mapping.LabelExtract) > 0 {
			segments := splitPathSegments(pm.Name)
			for _, rule := range mapping.LabelExtract {
				if rule.PathSegment >= 0 && rule.PathSegment < len(segments) {
					labels[rule.LabelName] = segments[rule.PathSegment]
				}
				// Out-of-bounds segment index is silently skipped (no panic).
			}
		}

		// Template variable captures also become labels.
		for k, v := range vars {
			labels[k] = v
		}

		return pm.Value, labels, true
	}
	return 0, nil, false
}

// splitPathSegments splits a metric path by its primary delimiter, removing empty segments.
// Supports "/" (gNMI/YANG paths), ":" (Prometheus-safe hierarchical paths like Ericsson ENM),
// and "_" (flat Prometheus metric names).
func splitPathSegments(path string) []string {
	// "/" takes precedence (gNMI/path-style).
	if strings.Contains(path, "/") {
		parts := strings.Split(path, "/")
		var result []string
		for _, s := range parts {
			if s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	// ":" for Prometheus-safe hierarchical paths.
	if strings.Contains(path, ":") {
		parts := strings.Split(path, ":")
		var result []string
		for _, s := range parts {
			if s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	// Fallback: split by "_" for flat Prometheus metric names.
	return strings.Split(path, "_")
}

// metricLookupKey returns the metric identifier used to index into the parsed
// metrics map. For gNMI sources, metrics are keyed by their gNMI path string
// (e.g. /gnb/cell/prb/utilization); for Prometheus, by the exposition metric name.
func metricLookupKey(mapping *schema.MetricMapping, proto collector.Protocol) string {
	if proto == collector.ProtocolGNMI {
		return mapping.GNMIPath
	}
	return mapping.PrometheusMetric
}

// matchMetric resolves a raw value from parsed Prometheus metrics using the
// MetricMapping's label match strategy.
//
// Strategies:
//   - "exact": series labels must be a superset of mapping labels (contain all
//     specified labels with matching values). If no labels are specified, any
//     series with that metric name matches.
//   - "sum_by": sum values across all series whose labels are a superset of
//     the mapping labels.
//   - "any": take the first series whose labels are a superset of the mapping labels.
//
// Returns (value, true) on match, (0, false) if no series matches.
func matchMetric(candidates []promparser.ParsedMetric, mapping *schema.MetricMapping) (float64, bool) {
	if len(candidates) == 0 {
		return 0, false
	}

	switch mapping.LabelMatchStrategy {
	case "exact":
		for _, pm := range candidates {
			if labelsMatch(pm.Labels, mapping.Labels) {
				return pm.Value, true
			}
		}
		return 0, false

	case "sum_by":
		var sum float64
		matched := false
		for _, pm := range candidates {
			if labelsSuperset(pm.Labels, mapping.Labels) {
				sum += pm.Value
				matched = true
			}
		}
		return sum, matched

	case "any":
		for _, pm := range candidates {
			if labelsSuperset(pm.Labels, mapping.Labels) {
				return pm.Value, true
			}
		}
		return 0, false

	default:
		// Unknown strategy — fall back to superset match on first candidate.
		for _, pm := range candidates {
			if labelsSuperset(pm.Labels, mapping.Labels) {
				return pm.Value, true
			}
		}
		return 0, false
	}
}

// labelsMatch returns true if the metric labels contain all required labels with
// matching values. For "exact" strategy: when the mapping specifies no labels
// (empty or nil), any series matches. When labels are specified, the metric must
// contain at least those key-value pairs.
func labelsMatch(metricLabels, requiredLabels map[string]string) bool {
	if len(requiredLabels) == 0 {
		return true
	}
	return labelsSuperset(metricLabels, requiredLabels)
}

// labelsSuperset returns true if metricLabels contains every key-value pair in required.
func labelsSuperset(metricLabels, required map[string]string) bool {
	for k, v := range required {
		if metricLabels[k] != v {
			return false
		}
	}
	return true
}

// applyCounterDelta computes the delta between the current and previous counter value.
// On first scrape (no previous state), emits the raw value. On subsequent scrapes,
// emits the delta. Handles counter resets (value drops) when reset_aware is true
// by emitting the new value as-is.
//
// Caller must hold e.mu.
func (e *Engine) applyCounterDelta(sk, kpiName string, newValue float64, resetAware bool) float64 {
	lastValue, hasPrev := e.store.Get(sk, kpiName)
	e.store.Put(sk, kpiName, newValue)

	if !hasPrev {
		// First scrape: emit raw value.
		return newValue
	}

	delta := newValue - lastValue
	if delta < 0 && resetAware {
		// Counter reset detected (e.g., NF restart). Emit current value
		// as the delta — the counter restarted from zero, so the new value
		// represents all events since restart.
		return newValue
	}

	return delta
}

// evaluateDerived computes a derived KPI by evaluating its formula against
// already-resolved base KPI values. Returns a NormalizeError if any dependency
// failed or the formula evaluation errors.
func (e *Engine) evaluateDerived(kpi *schema.KPIDefinition, resolved map[string]float64, failed map[string]NormalizeError) (float64, *NormalizeError) {
	// Check that all dependencies were resolved successfully.
	allUnsupported := true
	for _, dep := range kpi.DependsOn {
		if nerr, isFailed := failed[dep]; isFailed {
			if !nerr.Unsupported {
				allUnsupported = false
			}
			return 0, &NormalizeError{
				KPIName:     kpi.Name,
				Reason:      fmt.Sprintf("dependency %q failed: %s", dep, nerr.Reason),
				Unsupported: allUnsupported,
			}
		}
		if _, ok := resolved[dep]; !ok {
			return 0, &NormalizeError{
				KPIName: kpi.Name,
				Reason:  fmt.Sprintf("dependency %q not resolved", dep),
			}
		}
	}

	// Build vars map for formula evaluation. Include all resolved values,
	// not only direct dependencies — formulas may reference transitive deps.
	vars := make(map[string]float64, len(resolved))
	for k, v := range resolved {
		vars[k] = v
	}

	val, err := formula.Eval(kpi.Formula, vars)
	if err != nil {
		return 0, &NormalizeError{
			KPIName: kpi.Name,
			Raw:     kpi.Formula,
			Reason:  fmt.Sprintf("formula evaluation failed: %v", err),
		}
	}

	return val, nil
}

// buildAttributes constructs ResourceAttributes from a SourceInfo.
func buildAttributes(src collector.SourceInfo) ResourceAttributes {
	return ResourceAttributes{
		NFInstanceID: src.InstanceID,
		NFType:       src.NFType,
		Vendor:       src.Vendor,
	}
}
