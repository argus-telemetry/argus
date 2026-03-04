package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level Argus configuration.
type Config struct {
	SchemaDir        string            `yaml:"schema_dir"`
	CounterStorePath string            `yaml:"counter_store_path,omitempty"`
	Collectors       []CollectorEntry  `yaml:"collectors"`
	Output           OutputConfig      `yaml:"output"`
	Correlator       *CorrelatorConfig `yaml:"correlator,omitempty"`
}

// CorrelatorConfig configures the cross-NF correlation engine.
type CorrelatorConfig struct {
	WindowSize   Duration `yaml:"window_size"`
	EvalInterval Duration `yaml:"eval_interval"`
}

// CollectorEntry configures a single collector instance.
type CollectorEntry struct {
	Name     string         `yaml:"name"`
	Endpoint string         `yaml:"endpoint"`
	Interval Duration       `yaml:"interval"`
	Extra    map[string]any `yaml:"extra,omitempty"`
}

// OutputConfig configures output backends.
type OutputConfig struct {
	Prometheus PrometheusOutputConfig `yaml:"prometheus"`
	OTLP       *OTLPOutputConfig     `yaml:"otlp,omitempty"`
}

// OTLPOutputConfig configures the OTLP/gRPC metric exporter.
type OTLPOutputConfig struct {
	Endpoint      string            `yaml:"endpoint"`
	Insecure      bool              `yaml:"insecure"`
	Headers       map[string]string `yaml:"headers,omitempty"`
	BatchInterval Duration          `yaml:"batch_interval,omitempty"`
	BatchSize     int               `yaml:"batch_size,omitempty"`
}

// PrometheusOutputConfig configures the Prometheus pull output.
type PrometheusOutputConfig struct {
	Listen string `yaml:"listen"`
}

// Duration wraps time.Duration for YAML unmarshalling (e.g. "15s", "1m").
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

// LoadConfig reads and parses the YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.SchemaDir == "" {
		return nil, fmt.Errorf("schema_dir is required")
	}
	if len(cfg.Collectors) == 0 {
		return nil, fmt.Errorf("at least one collector must be configured")
	}
	if cfg.Output.Prometheus.Listen == "" {
		cfg.Output.Prometheus.Listen = ":8080"
	}
	if cfg.Output.OTLP != nil && cfg.Output.OTLP.Endpoint == "" {
		return nil, fmt.Errorf("otlp output configured but endpoint is empty")
	}
	return &cfg, nil
}
