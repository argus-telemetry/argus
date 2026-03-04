package schema

// NFSchema defines all KPIs and vendor mappings for a single network function type.
// Each YAML file in the schema directory corresponds to one NFSchema instance,
// keyed by a dotted namespace (e.g. "argus.5g.amf").
type NFSchema struct {
	Namespace     string                   `yaml:"namespace"`      // e.g. "argus.5g.amf"
	NFType        string                   `yaml:"nf_type"`        // "AMF" | "SMF" | "UPF" | "gNB"
	SchemaVersion string                   `yaml:"schema_version"` // "v1"
	Spec          string                   `yaml:"spec"`           // e.g. "3GPP TS 28.552"
	KPIs          []KPIDefinition          `yaml:"kpis"`
	Mappings      map[string]VendorMapping `yaml:"mappings"` // keyed by vendor name
}

// KPIDefinition describes a single KPI in the 5G telemetry schema.
// Base KPIs are collected directly from NF telemetry; derived KPIs are
// computed from other KPIs using the Formula expression.
type KPIDefinition struct {
	Name        string   `yaml:"name"`                  // e.g. "registration.success_rate"
	Description string   `yaml:"description"`
	Unit        string   `yaml:"unit"`                  // "ratio" | "count" | "bps" | "ms" | "dBm"
	SpecRef     string   `yaml:"spec_ref"`              // 3GPP reference
	Derived     bool     `yaml:"derived"`               // true if computed from other KPIs
	Formula     string   `yaml:"formula,omitempty"`
	DependsOn   []string `yaml:"depends_on,omitempty"`
}

// VendorMapping maps a vendor's native telemetry to Argus KPI names.
// Each vendor exposes metrics through a specific protocol; the Metrics map
// translates from Argus KPI names to vendor-native metric identifiers.
type VendorMapping struct {
	SourceProtocol string                   `yaml:"source_protocol"` // "prometheus" | "gnmi"
	Metrics        map[string]MetricMapping `yaml:"metrics"`         // keyed by KPI name
}

// MetricMapping maps a single vendor metric to an Argus KPI.
// Supports both Prometheus and gNMI as source protocols, with
// label matching and counter-reset semantics.
type MetricMapping struct {
	PrometheusMetric   string            `yaml:"prometheus_metric"`
	Labels             map[string]string `yaml:"labels,omitempty"`
	Type               string            `yaml:"type"`                 // "counter" | "gauge"
	ResetAware         bool              `yaml:"reset_aware"`          // handle counter resets
	LabelMatchStrategy string            `yaml:"label_match_strategy"` // "exact" | "sum_by" | "any"
	GNMIPath           string            `yaml:"gnmi_path,omitempty"`
}
