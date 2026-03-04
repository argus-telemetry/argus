package engine

// Scenario defines a simulation run with one or more simulated NF instances.
type Scenario struct {
	Name           string          `yaml:"name"`
	Description    string          `yaml:"description"`
	Duration       int             `yaml:"duration"` // seconds, 0 = indefinite
	NFs            []SimulatedNF   `yaml:"nfs"`
	ExpectedEvents []ExpectedEvent `yaml:"expected_events,omitempty"`
}

// ExpectedEvent declares a correlation event that the scenario should produce.
// Used by the certification asserter to validate rule behavior against stimulus.
// An empty expected_events list (or explicit `expected_events: []`) asserts zero events.
type ExpectedEvent struct {
	Rule          string   `yaml:"rule"`
	Severity      string   `yaml:"severity"`
	WithinSeconds int      `yaml:"within_seconds"`
	AffectedNFs   []string `yaml:"affected_nfs"`
}

// SimulatedNF describes a single simulated network function.
type SimulatedNF struct {
	Type       string       `yaml:"type"`        // "AMF" | "SMF" | "UPF" | "gNB"
	Vendor     string       `yaml:"vendor"`      // "free5gc" | "open5gs" | "oai"
	InstanceID string       `yaml:"instance_id"` // e.g. "amf-001"
	Protocol   string       `yaml:"protocol"`    // "prometheus" | "gnmi"
	Port       int          `yaml:"port"`        // listen port for emitter
	Metrics    []BaseMetric `yaml:"metrics"`
	Events     []Event      `yaml:"events,omitempty"`
}

// BaseMetric defines a simulated metric's steady-state behavior.
type BaseMetric struct {
	Name          string            `yaml:"name"`
	Labels        map[string]string `yaml:"labels,omitempty"`
	Type          string            `yaml:"type"` // "counter" | "gauge"
	Baseline      float64           `yaml:"baseline"`
	RatePerSecond float64           `yaml:"rate_per_second,omitempty"` // counters: increment rate
	Jitter        float64           `yaml:"jitter,omitempty"`         // gauges: +/- random range
}

// Event overrides metric behavior at a specific time window.
type Event struct {
	Name      string  `yaml:"name"`
	StartSec  int     `yaml:"start_sec"`
	DurationS int     `yaml:"duration_sec"`
	Metric    string  `yaml:"metric"`              // metric name to override
	Override  float64 `yaml:"override,omitempty"`   // gauge: set to this value
	RateScale float64 `yaml:"rate_scale,omitempty"` // counter: multiply rate by this
}
