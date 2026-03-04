package pipeline

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// ChannelPipeline is a Pipeline backed by Go channels with fan-out semantics.
// Each subscriber on a topic gets its own buffered channel and receives a copy of every
// published message. Non-blocking sends mean slow subscribers drop messages rather than
// back-pressure the publisher — dropped messages are logged as warnings.
type ChannelPipeline struct {
	mu      sync.RWMutex
	topics  map[string][]chan []byte
	bufSize int
	closed  bool
}

// NewChannelPipeline creates a ChannelPipeline where each subscriber channel is buffered
// to bufSize. A bufSize of 0 creates unbuffered channels (useful for tests where
// synchronous delivery is desired).
func NewChannelPipeline(bufSize int) *ChannelPipeline {
	return &ChannelPipeline{
		topics:  make(map[string][]chan []byte),
		bufSize: bufSize,
	}
}

// Publish fans out data to every subscriber on topic. Sends are non-blocking: if a
// subscriber's channel is full the message is dropped and a warning is logged.
// Returns an error if the pipeline is closed or the context is cancelled.
func (p *ChannelPipeline) Publish(ctx context.Context, topic string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish to %q: %w", topic, err)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return fmt.Errorf("publish to %q: pipeline closed", topic)
	}

	for _, ch := range p.topics[topic] {
		select {
		case ch <- data:
		default:
			log.Printf("WARN: dropped message on topic %q — subscriber channel full (bufSize=%d)", topic, p.bufSize)
		}
	}
	return nil
}

// Subscribe creates a new buffered channel for topic and returns it. Every subsequent
// Publish on this topic delivers a copy to this channel. The caller owns the returned
// channel and should drain it until close.
// Returns an error if the pipeline is closed.
func (p *ChannelPipeline) Subscribe(_ context.Context, topic string) (<-chan []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("subscribe to %q: pipeline closed", topic)
	}

	ch := make(chan []byte, p.bufSize)
	p.topics[topic] = append(p.topics[topic], ch)
	return ch, nil
}

// Close shuts down the pipeline: marks it closed, closes every subscriber channel, and
// clears internal state. Safe to call multiple times — subsequent calls are no-ops.
func (p *ChannelPipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	for _, subs := range p.topics {
		for _, ch := range subs {
			close(ch)
		}
	}
	p.topics = nil
	return nil
}
