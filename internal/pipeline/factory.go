package pipeline

import "fmt"

// Config selects and configures the pipeline implementation.
type Config struct {
	Type  string      `yaml:"type"` // "channel" (default) | "kafka"
	Kafka KafkaConfig `yaml:"kafka"`
}

// New creates a Pipeline from configuration. Defaults to ChannelPipeline if
// type is empty or "channel".
func New(cfg Config) (Pipeline, error) {
	switch cfg.Type {
	case "", "channel":
		return NewChannelPipeline(256), nil
	case "kafka":
		return NewKafkaPipeline(cfg.Kafka)
	default:
		return nil, fmt.Errorf("unknown pipeline type %q", cfg.Type)
	}
}
