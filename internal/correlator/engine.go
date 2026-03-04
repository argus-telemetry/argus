package correlator

import (
	"fmt"
	"sync"
	"time"

	"github.com/argus-5g/argus/internal/normalizer"
)

// Engine accumulates normalized KPI samples in per-PLMN+Slice time windows
// and evaluates correlation rules against those windows.
type Engine struct {
	windowSize time.Duration
	rules      []CorrelationRule
	mu         sync.Mutex
	// windows keyed by "PLMN:SliceSST:SliceSD"
	windows   map[string]*window
	eventSink chan<- CorrelationEvent // test-only hook for observing emitted events
}

type window struct {
	plmn    string
	sliceID string
	// samples[namespace][kpiName] = time-ordered samples
	samples map[string]map[string][]Sample
}

// NewEngine creates a correlator with the given window size and rules.
func NewEngine(windowSize time.Duration, rules []CorrelationRule) *Engine {
	return &Engine{
		windowSize: windowSize,
		rules:      rules,
		windows:    make(map[string]*window),
	}
}

// windowKey builds the map key for a PLMN + optional slice.
func windowKey(plmn string, sid *normalizer.SliceID) string {
	if sid == nil {
		return plmn
	}
	return fmt.Sprintf("%s:%d:%s", plmn, sid.SST, sid.SD)
}

// sliceIDString returns a human-readable slice ID or "".
func sliceIDString(sid *normalizer.SliceID) string {
	if sid == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s", sid.SST, sid.SD)
}

// Ingest adds a normalized record to the appropriate window.
func (e *Engine) Ingest(rec normalizer.NormalizedRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := windowKey(rec.Attributes.PLMNID, rec.Attributes.SliceID)
	w, ok := e.windows[key]
	if !ok {
		w = &window{
			plmn:    rec.Attributes.PLMNID,
			sliceID: sliceIDString(rec.Attributes.SliceID),
			samples: make(map[string]map[string][]Sample),
		}
		e.windows[key] = w
	}

	if w.samples[rec.Namespace] == nil {
		w.samples[rec.Namespace] = make(map[string][]Sample)
	}

	w.samples[rec.Namespace][rec.KPIName] = append(
		w.samples[rec.Namespace][rec.KPIName],
		Sample{Value: rec.Value, Timestamp: rec.Timestamp},
	)
}

// Snapshots returns a WindowSnapshot for each active window, evicting expired samples.
func (e *Engine) Snapshots(now time.Time) []WindowSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	cutoff := now.Add(-e.windowSize)
	var snaps []WindowSnapshot

	for key, w := range e.windows {
		// Evict expired samples and prune empty entries to avoid
		// accumulating zero-length slices for high-cardinality KPI streams.
		empty := true
		for ns, kpis := range w.samples {
			for kpi, samples := range kpis {
				filtered := evict(samples, cutoff)
				if len(filtered) == 0 {
					delete(kpis, kpi)
				} else {
					w.samples[ns][kpi] = filtered
					empty = false
				}
			}
			if len(kpis) == 0 {
				delete(w.samples, ns)
			}
		}

		if empty {
			delete(e.windows, key)
			continue
		}

		// Deep-copy samples for the snapshot.
		snapSamples := make(map[string]map[string][]Sample)
		for ns, kpis := range w.samples {
			snapSamples[ns] = make(map[string][]Sample)
			for kpi, samples := range kpis {
				cp := make([]Sample, len(samples))
				copy(cp, samples)
				snapSamples[ns][kpi] = cp
			}
		}

		snaps = append(snaps, WindowSnapshot{
			Start:   cutoff,
			End:     now,
			PLMN:    w.plmn,
			SliceID: w.sliceID,
			Samples: snapSamples,
		})
	}

	return snaps
}

// RegisterEventSink sets a channel that receives a copy of every CorrelationEvent
// produced by EvaluateAll. Intended for integration tests — not wired in production.
// Must be called before the evaluation goroutine starts.
func (e *Engine) RegisterEventSink(ch chan<- CorrelationEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventSink = ch
}

// EvaluateAll runs all rules against all windows and returns events.
func (e *Engine) EvaluateAll(now time.Time) []CorrelationEvent {
	snaps := e.Snapshots(now)

	var events []CorrelationEvent
	for _, snap := range snaps {
		for _, rule := range e.rules {
			events = append(events, rule.Evaluate(snap)...)
		}
	}

	// Deliver to event sink if registered (non-blocking).
	if e.eventSink != nil {
		for _, ev := range events {
			select {
			case e.eventSink <- ev:
			default:
			}
		}
	}

	return events
}

// evict removes samples older than cutoff.
func evict(samples []Sample, cutoff time.Time) []Sample {
	i := 0
	for i < len(samples) && samples[i].Timestamp.Before(cutoff) {
		i++
	}
	if i == 0 {
		return samples
	}
	return samples[i:]
}
