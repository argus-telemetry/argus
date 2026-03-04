package pipeline

import "context"

// Pipeline abstracts the internal message bus between collectors, the normalizer, and output writers.
// v0.1 uses Go channels (ChannelPipeline). Kafka plugs in via this interface in v0.2.
// Messages are []byte — serialization/deserialization happens at the boundary.
type Pipeline interface {
	// Publish sends data to all subscribers on the given topic.
	Publish(ctx context.Context, topic string, data []byte) error

	// Subscribe returns a channel that receives messages published to the given topic.
	// Multiple subscribers on the same topic each receive a copy of every message.
	Subscribe(ctx context.Context, topic string) (<-chan []byte, error)

	// Close shuts down the pipeline and closes all subscriber channels.
	Close() error
}
