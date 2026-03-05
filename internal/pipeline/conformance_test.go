package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipelineFactory returns a fresh Pipeline instance for conformance testing.
type pipelineFactory struct {
	name    string
	create  func(t *testing.T) Pipeline
	cleanup func(Pipeline)
}

func channelFactory() pipelineFactory {
	return pipelineFactory{
		name: "ChannelPipeline",
		create: func(t *testing.T) Pipeline {
			return NewChannelPipeline(64)
		},
		cleanup: func(p Pipeline) { p.Close() },
	}
}

func kafkaFactory() pipelineFactory {
	return pipelineFactory{
		name: "KafkaPipeline",
		create: func(t *testing.T) Pipeline {
			p, err := NewKafkaPipeline(KafkaConfig{
				Brokers: []string{"localhost:19092"},
			})
			if err != nil {
				t.Skipf("Kafka broker unavailable: %v", err)
			}
			// Verify connectivity with a quick produce attempt.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := p.Publish(ctx, "conformance-probe", []byte("ping")); err != nil {
				p.Close()
				t.Skipf("Kafka broker not responding: %v", err)
			}
			return p
		},
		cleanup: func(p Pipeline) { p.Close() },
	}
}

func conformanceFactories() []pipelineFactory {
	return []pipelineFactory{channelFactory(), kafkaFactory()}
}

func TestConformance_PublishAndReceive(t *testing.T) {
	for _, f := range conformanceFactories() {
		t.Run(f.name, func(t *testing.T) {
			p := f.create(t)
			defer f.cleanup(p)

			ch, err := p.Subscribe(context.Background(), "test-topic")
			require.NoError(t, err)

			msg := []byte(`{"conformance": true}`)
			require.NoError(t, p.Publish(context.Background(), "test-topic", msg))

			select {
			case received := <-ch:
				assert.Equal(t, msg, received)
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for message")
			}
		})
	}
}

func TestConformance_MultipleSubscribers(t *testing.T) {
	for _, f := range conformanceFactories() {
		t.Run(f.name, func(t *testing.T) {
			p := f.create(t)
			defer f.cleanup(p)

			ch1, err := p.Subscribe(context.Background(), "fanout-topic")
			require.NoError(t, err)
			ch2, err := p.Subscribe(context.Background(), "fanout-topic")
			require.NoError(t, err)

			msg := []byte("fanout")
			require.NoError(t, p.Publish(context.Background(), "fanout-topic", msg))

			for i, ch := range []<-chan []byte{ch1, ch2} {
				select {
				case received := <-ch:
					assert.Equal(t, msg, received, "subscriber %d", i)
				case <-time.After(5 * time.Second):
					t.Fatalf("subscriber %d: timeout", i)
				}
			}
		})
	}
}

func TestConformance_TopicIsolation(t *testing.T) {
	for _, f := range conformanceFactories() {
		t.Run(f.name, func(t *testing.T) {
			p := f.create(t)
			defer f.cleanup(p)

			chA, err := p.Subscribe(context.Background(), "topic-a")
			require.NoError(t, err)
			chB, err := p.Subscribe(context.Background(), "topic-b")
			require.NoError(t, err)

			require.NoError(t, p.Publish(context.Background(), "topic-a", []byte("for-a")))

			select {
			case received := <-chA:
				assert.Equal(t, []byte("for-a"), received)
			case <-time.After(5 * time.Second):
				t.Fatal("timeout on topic-a")
			}

			select {
			case <-chB:
				t.Fatal("topic-b should not receive messages from topic-a")
			case <-time.After(200 * time.Millisecond):
				// expected
			}
		})
	}
}

func TestConformance_CloseBehavior(t *testing.T) {
	for _, f := range conformanceFactories() {
		t.Run(f.name, func(t *testing.T) {
			p := f.create(t)

			_, err := p.Subscribe(context.Background(), "close-topic")
			require.NoError(t, err)

			require.NoError(t, p.Close())

			err = p.Publish(context.Background(), "close-topic", []byte("after-close"))
			assert.Error(t, err)

			_, err = p.Subscribe(context.Background(), "close-topic")
			assert.Error(t, err)
		})
	}
}
