package pipeline

// KafkaPipeline implements Pipeline backed by Apache Kafka via franz-go.
//
// Behavioral differences from ChannelPipeline:
//   - ChannelPipeline: in-process, zero deps, drops on full buffer,
//     no persistence, single-process only
//   - KafkaPipeline: network dep (Kafka), never drops (blocks/errors),
//     persistent (survives pod restart), multi-process safe
//   - Both satisfy Pipeline interface — swap via config only

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// KafkaConfig holds configuration for a KafkaPipeline.
type KafkaConfig struct {
	Brokers             []string      `yaml:"brokers"`
	TopicPrefix         string        `yaml:"topic_prefix"`
	ProducerTimeout     time.Duration `yaml:"producer_timeout"`
	ConsumerGroupPrefix string        `yaml:"consumer_group_prefix"`
}

// KafkaPipeline is a Pipeline backed by Kafka with per-subscriber consumer groups.
type KafkaPipeline struct {
	mu       sync.Mutex
	cfg      KafkaConfig
	producer *kgo.Client
	subs     []*kafkaSub
	closed   bool
	subSeq   int
}

type kafkaSub struct {
	client *kgo.Client
	ch     chan []byte
	cancel context.CancelFunc
	done   chan struct{}
}

// NewKafkaPipeline creates a Kafka-backed pipeline. The producer is established
// immediately; consumer groups are created lazily per Subscribe call.
func NewKafkaPipeline(cfg KafkaConfig) (*KafkaPipeline, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: at least one broker required")
	}
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "argus"
	}
	if cfg.ProducerTimeout == 0 {
		cfg.ProducerTimeout = 5 * time.Second
	}
	if cfg.ConsumerGroupPrefix == "" {
		cfg.ConsumerGroupPrefix = "argus"
	}

	producer, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ProduceRequestTimeout(cfg.ProducerTimeout),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}

	return &KafkaPipeline{
		cfg:      cfg,
		producer: producer,
	}, nil
}

func (k *KafkaPipeline) topicName(topic string) string {
	return k.cfg.TopicPrefix + "." + topic
}

// Publish sends data to the Kafka topic synchronously. Blocks until the broker
// acknowledges the produce or context is cancelled. Never silently drops.
func (k *KafkaPipeline) Publish(ctx context.Context, topic string, data []byte) error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return fmt.Errorf("publish to %q: pipeline closed", topic)
	}
	k.mu.Unlock()

	record := &kgo.Record{
		Topic: k.topicName(topic),
		Value: data,
	}

	result := k.producer.ProduceSync(ctx, record)
	return result.FirstErr()
}

// Subscribe creates a consumer group that delivers messages on the returned channel.
// Each subscriber gets an independent consumer group so all subscribers receive
// every message (fan-out semantics matching ChannelPipeline).
func (k *KafkaPipeline) Subscribe(ctx context.Context, topic string) (<-chan []byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.closed {
		return nil, fmt.Errorf("subscribe to %q: pipeline closed", topic)
	}

	k.subSeq++
	groupID := fmt.Sprintf("%s-sub-%d", k.cfg.ConsumerGroupPrefix, k.subSeq)
	kafkaTopic := k.topicName(topic)

	client, err := kgo.NewClient(
		kgo.SeedBrokers(k.cfg.Brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(kafkaTopic),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer group %q: %w", groupID, err)
	}

	ch := make(chan []byte, 256)
	subCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	sub := &kafkaSub{
		client: client,
		ch:     ch,
		cancel: cancel,
		done:   done,
	}
	k.subs = append(k.subs, sub)

	go func() {
		defer close(done)
		defer close(ch)
		for {
			fetches := client.PollFetches(subCtx)
			if subCtx.Err() != nil {
				return
			}
			fetches.EachRecord(func(r *kgo.Record) {
				// Copy value to decouple from fetch buffer lifecycle.
				val := make([]byte, len(r.Value))
				copy(val, r.Value)
				select {
				case ch <- val:
				case <-subCtx.Done():
				}
			})
		}
	}()

	return ch, nil
}

// Close flushes the producer and shuts down all consumer groups.
func (k *KafkaPipeline) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.closed {
		return nil
	}
	k.closed = true

	for _, sub := range k.subs {
		sub.cancel()
		<-sub.done
		sub.client.Close()
	}
	k.subs = nil

	k.producer.Close()
	return nil
}

// Compile-time interface check.
var _ Pipeline = (*KafkaPipeline)(nil)
