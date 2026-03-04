package free5gc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/collector"
)

const fakeMetrics = `# HELP amf_registered_subscribers Number of registered subscribers
# TYPE amf_registered_subscribers gauge
amf_registered_subscribers 150
# HELP amf_registration_requests_total Total registration requests
# TYPE amf_registration_requests_total counter
amf_registration_requests_total 4200
`

func TestCollector_Name(t *testing.T) {
	tests := []struct {
		nfType string
		want   string
	}{
		{"AMF", "free5gc-amf"},
		{"SMF", "free5gc-smf"},
		{"UPF", "free5gc-upf"},
	}
	for _, tt := range tests {
		c := &Collector{nfType: tt.nfType}
		assert.Equal(t, tt.want, c.Name())
	}
}

func TestCollector_Connect_EmptyEndpoint(t *testing.T) {
	c := &Collector{nfType: "AMF"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Interval: time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint must not be empty")
}

func TestCollector_Connect_ZeroInterval(t *testing.T) {
	c := &Collector{nfType: "AMF"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "http://localhost:9090",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval must be positive")
}

func TestCollector_Connect_Valid(t *testing.T) {
	c := &Collector{nfType: "AMF"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "http://localhost:9090",
		Interval: time.Second,
	})
	require.NoError(t, err)
	assert.NotNil(t, c.client)
	assert.Equal(t, "http://localhost:9090", c.cfg.Endpoint)
}

func TestCollector_Collect_SingleScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/metrics", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeMetrics))
	}))
	defer srv.Close()

	c := &Collector{nfType: "AMF"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	ch := make(chan collector.RawRecord, 10)

	// Cancel after receiving at least one record.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		_ = c.Collect(ctx, ch)
	}()

	// Wait for at least one record.
	select {
	case rec := <-ch:
		assert.Equal(t, "free5gc", rec.Source.Vendor)
		assert.Equal(t, "AMF", rec.Source.NFType)
		assert.Equal(t, srv.URL, rec.Source.Endpoint)
		assert.Equal(t, collector.ProtocolPrometheus, rec.Protocol)
		assert.Equal(t, "v1", rec.SchemaVersion)
		assert.Contains(t, string(rec.Payload), "amf_registered_subscribers")
		assert.Contains(t, string(rec.Payload), "amf_registration_requests_total")
		assert.False(t, rec.Timestamp.IsZero())
	case <-ctx.Done():
		t.Fatal("timed out waiting for RawRecord")
	}
}

func TestCollector_Collect_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Collector{nfType: "SMF"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	ch := make(chan collector.RawRecord, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Should not panic or send garbage on 500s — just silently skip.
	_ = c.Collect(ctx, ch)

	// Channel should be empty (no valid records).
	assert.Empty(t, ch)
}

func TestCollector_Close(t *testing.T) {
	c := &Collector{nfType: "UPF"}
	_ = c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "http://localhost:9090",
		Interval: time.Second,
	})
	err := c.Close()
	assert.NoError(t, err)
}

func TestCollector_Close_NilClient(t *testing.T) {
	c := &Collector{nfType: "UPF"}
	err := c.Close()
	assert.NoError(t, err)
}

func TestRegister_DefaultRegistry(t *testing.T) {
	// The init() in register.go populates DefaultRegistry.
	names := collector.DefaultRegistry.List()
	assert.Contains(t, names, "free5gc-amf")
	assert.Contains(t, names, "free5gc-smf")
	assert.Contains(t, names, "free5gc-upf")

	// Verify factory produces correct collector types.
	for _, name := range []string{"free5gc-amf", "free5gc-smf", "free5gc-upf"} {
		c, err := collector.DefaultRegistry.Get(name)
		require.NoError(t, err)
		assert.Equal(t, name, c.Name())
	}
}
