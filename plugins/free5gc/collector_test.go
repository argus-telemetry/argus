package free5gc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// --- Scrape error classification tests ---

func collectErrors(t *testing.T, c *Collector, cfg collector.CollectorConfig, wait time.Duration) []collector.ScrapeError {
	t.Helper()
	var errors []collector.ScrapeError
	var mu sync.Mutex
	cfg.OnScrapeError = func(se collector.ScrapeError) {
		mu.Lock()
		errors = append(errors, se)
		mu.Unlock()
	}
	require.NoError(t, c.Connect(context.Background(), cfg))

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan collector.RawRecord, 16)
	go c.Collect(ctx, ch)
	time.Sleep(wait)
	cancel()

	mu.Lock()
	defer mu.Unlock()
	return errors
}

func TestCollect_ScrapeError_AuthForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	errs := collectErrors(t, &Collector{nfType: "AMF"}, collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	}, 100*time.Millisecond)

	require.NotEmpty(t, errs)
	assert.Equal(t, collector.ErrorClassAuth, errs[0].Class)
	assert.Equal(t, "free5gc", errs[0].Vendor)
	assert.Equal(t, "AMF", errs[0].NFType)
}

func TestCollect_ScrapeError_AuthUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	errs := collectErrors(t, &Collector{nfType: "AMF"}, collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	}, 100*time.Millisecond)

	require.NotEmpty(t, errs)
	assert.Equal(t, collector.ErrorClassAuth, errs[0].Class)
}

func TestCollect_ScrapeError_Network_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srvURL := srv.URL
	srv.Close()

	errs := collectErrors(t, &Collector{nfType: "SMF"}, collector.CollectorConfig{
		Endpoint: srvURL,
		Interval: 50 * time.Millisecond,
	}, 100*time.Millisecond)

	require.NotEmpty(t, errs)
	assert.Equal(t, collector.ErrorClassNetwork, errs[0].Class)
	assert.Equal(t, "SMF", errs[0].NFType)
}

func TestCollect_ScrapeError_Network_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	errs := collectErrors(t, &Collector{nfType: "AMF"}, collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	}, 100*time.Millisecond)

	require.NotEmpty(t, errs)
	assert.Equal(t, collector.ErrorClassNetwork, errs[0].Class)
}

func TestCollect_ScrapeError_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	errs := collectErrors(t, &Collector{nfType: "UPF"}, collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 200 * time.Millisecond, // timeout = interval/2 = 100ms
	}, 350*time.Millisecond)

	require.NotEmpty(t, errs)
	assert.Equal(t, collector.ErrorClassTimeout, errs[0].Class)
	assert.Equal(t, "UPF", errs[0].NFType)
}

func TestCollect_ScrapeError_NilCallback_NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Collector{nfType: "AMF"}
	cfg := collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	}
	require.NoError(t, c.Connect(context.Background(), cfg))

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan collector.RawRecord, 16)
	go c.Collect(ctx, ch)
	time.Sleep(100 * time.Millisecond)
	cancel()
}

func TestCollect_ScrapeSuccess_NoErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(fakeMetrics))
	}))
	defer srv.Close()

	errs := collectErrors(t, &Collector{nfType: "AMF"}, collector.CollectorConfig{
		Endpoint: srv.URL,
		Interval: 50 * time.Millisecond,
	}, 150*time.Millisecond)

	assert.Empty(t, errs, "no scrape errors expected on healthy endpoint")
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
