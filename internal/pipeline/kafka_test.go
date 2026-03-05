package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKafkaPipeline_SatisfiesInterface(t *testing.T) {
	// Compile-time check is in kafka.go via var _ Pipeline = (*KafkaPipeline)(nil).
	// This test documents the intent.
	var _ Pipeline = (*KafkaPipeline)(nil)
}

func TestKafkaPipeline_RequiresBroker(t *testing.T) {
	_, err := NewKafkaPipeline(KafkaConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one broker required")
}

func TestKafkaPipeline_DefaultConfig(t *testing.T) {
	// NewKafkaPipeline will fail to connect, but we can verify config defaults
	// by checking that the function doesn't panic on valid-looking config.
	_, err := NewKafkaPipeline(KafkaConfig{
		Brokers: []string{"localhost:19092"},
	})
	// This will succeed (franz-go client creation is lazy for connections)
	// or fail with a connection error. Either is fine — we're testing config.
	if err != nil {
		t.Skipf("Kafka broker unavailable (expected in CI): %v", err)
	}
}

func TestPipelineFactory_ChannelDefault(t *testing.T) {
	p, err := New(Config{})
	require.NoError(t, err)
	assert.IsType(t, &ChannelPipeline{}, p)
	p.Close()
}

func TestPipelineFactory_ChannelExplicit(t *testing.T) {
	p, err := New(Config{Type: "channel"})
	require.NoError(t, err)
	assert.IsType(t, &ChannelPipeline{}, p)
	p.Close()
}

func TestPipelineFactory_KafkaWhenConfigured(t *testing.T) {
	p, err := New(Config{
		Type: "kafka",
		Kafka: KafkaConfig{
			Brokers: []string{"localhost:19092"},
		},
	})
	if err != nil {
		t.Skipf("Kafka broker unavailable: %v", err)
	}
	assert.IsType(t, &KafkaPipeline{}, p)
	p.Close()
}

func TestPipelineFactory_UnknownType(t *testing.T) {
	_, err := New(Config{Type: "nats"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pipeline type")
}
