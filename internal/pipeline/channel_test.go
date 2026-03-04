package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelPipeline_PublishSubscribe(t *testing.T) {
	p := NewChannelPipeline(10)
	defer p.Close()

	ch, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	msg := []byte(`{"test": true}`)
	require.NoError(t, p.Publish(context.Background(), "raw", msg))

	select {
	case received := <-ch:
		assert.Equal(t, msg, received)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestChannelPipeline_MultipleSubscribers(t *testing.T) {
	p := NewChannelPipeline(10)
	defer p.Close()

	ch1, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	ch2, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	require.NoError(t, p.Publish(context.Background(), "raw", []byte("hello")))

	msg1 := <-ch1
	msg2 := <-ch2
	assert.Equal(t, []byte("hello"), msg1)
	assert.Equal(t, []byte("hello"), msg2)
}

func TestChannelPipeline_DifferentTopics(t *testing.T) {
	p := NewChannelPipeline(10)
	defer p.Close()

	rawCh, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	normCh, err := p.Subscribe(context.Background(), "normalized")
	require.NoError(t, err)

	require.NoError(t, p.Publish(context.Background(), "raw", []byte("raw-data")))

	select {
	case received := <-rawCh:
		assert.Equal(t, []byte("raw-data"), received)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on raw topic")
	}

	select {
	case <-normCh:
		t.Fatal("normalized subscriber should not receive messages published to raw")
	case <-time.After(50 * time.Millisecond):
		// expected — no message on normalized topic
	}
}

func TestChannelPipeline_ContextCancellation(t *testing.T) {
	p := NewChannelPipeline(10)
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Publish(ctx, "raw", []byte("should-fail"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestChannelPipeline_CloseStopsPublish(t *testing.T) {
	p := NewChannelPipeline(10)
	require.NoError(t, p.Close())

	err := p.Publish(context.Background(), "raw", []byte("after-close"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pipeline closed")
}

func TestChannelPipeline_CloseStopsSubscribe(t *testing.T) {
	p := NewChannelPipeline(10)
	require.NoError(t, p.Close())

	ch, err := p.Subscribe(context.Background(), "raw")
	require.Error(t, err)
	assert.Nil(t, ch)
	assert.Contains(t, err.Error(), "pipeline closed")
}

func TestChannelPipeline_CloseClosesChannels(t *testing.T) {
	p := NewChannelPipeline(10)

	ch, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	require.NoError(t, p.Close())

	// Reading from a closed channel returns zero value with ok=false.
	msg, ok := <-ch
	assert.False(t, ok)
	assert.Nil(t, msg)
}

func TestChannelPipeline_CloseIdempotent(t *testing.T) {
	p := NewChannelPipeline(10)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close()) // second close is a no-op
}

func TestChannelPipeline_DropOnFullBuffer(t *testing.T) {
	// bufSize=1, publish twice — second message should be dropped without blocking.
	p := NewChannelPipeline(1)
	defer p.Close()

	ch, err := p.Subscribe(context.Background(), "raw")
	require.NoError(t, err)

	require.NoError(t, p.Publish(context.Background(), "raw", []byte("first")))
	require.NoError(t, p.Publish(context.Background(), "raw", []byte("second")))

	msg := <-ch
	assert.Equal(t, []byte("first"), msg)

	// Channel should be empty — second message was dropped.
	select {
	case <-ch:
		t.Fatal("expected second message to be dropped")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// Compile-time check that ChannelPipeline satisfies the Pipeline interface.
var _ Pipeline = (*ChannelPipeline)(nil)
