package gnmi

import (
	"context"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/collector"
)

func TestCollector_Name(t *testing.T) {
	tests := []struct {
		nfType string
		want   string
	}{
		{"gNB", "gnmi-gnb"},
		{"GNB", "gnmi-gnb"},
	}
	for _, tt := range tests {
		c := &Collector{nfType: tt.nfType}
		assert.Equal(t, tt.want, c.Name())
	}
}

func TestCollector_Connect_EmptyEndpoint(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Interval: time.Second,
		Extra: map[string]any{
			"gnmi_paths": []any{"/gnb/cell/prb/utilization"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint must not be empty")
}

func TestCollector_Connect_ZeroInterval(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Extra: map[string]any{
			"gnmi_paths": []any{"/gnb/cell/prb/utilization"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval must be positive")
}

func TestCollector_Connect_MissingPaths(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Interval: time.Second,
		Extra:    map[string]any{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gnmi_paths must be configured")
}

func TestCollector_Connect_MissingPathsNilExtra(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Interval: time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gnmi_paths must be configured")
}

func TestCollector_Connect_InvalidSampleInterval(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Interval: time.Second,
		Extra: map[string]any{
			"gnmi_paths":      []any{"/gnb/cell/prb/utilization"},
			"sample_interval": "not-a-duration",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sample_interval")
}

func TestCollector_Connect_Valid(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Interval: 10 * time.Second,
		Extra: map[string]any{
			"gnmi_paths": []any{
				"/gnb/cell/prb/utilization",
				"/gnb/cell/throughput/downlink",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "localhost:9339", c.cfg.Endpoint)
	assert.Equal(t, 10*time.Second, c.interval)
	assert.Equal(t, []string{"/gnb/cell/prb/utilization", "/gnb/cell/throughput/downlink"}, c.paths)
	assert.NotNil(t, c.conn)

	// Cleanup.
	require.NoError(t, c.Close())
}

func TestCollector_Connect_CustomSampleInterval(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Connect(context.Background(), collector.CollectorConfig{
		Endpoint: "localhost:9339",
		Interval: 10 * time.Second,
		Extra: map[string]any{
			"gnmi_paths":      []any{"/gnb/cell/prb/utilization"},
			"sample_interval": "5s",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, c.interval, "sample_interval should override cfg.Interval")

	require.NoError(t, c.Close())
}

func TestCollector_Close_NilConn(t *testing.T) {
	c := &Collector{nfType: "gNB"}
	err := c.Close()
	assert.NoError(t, err)
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantElems []string
	}{
		{
			name:      "standard gNMI path with leading slash",
			input:     "/gnb/cell/prb/utilization",
			wantElems: []string{"gnb", "cell", "prb", "utilization"},
		},
		{
			name:      "path without leading slash",
			input:     "gnb/cell/prb/utilization",
			wantElems: []string{"gnb", "cell", "prb", "utilization"},
		},
		{
			name:      "single element path",
			input:     "/gnb",
			wantElems: []string{"gnb"},
		},
		{
			name:      "empty path",
			input:     "",
			wantElems: nil,
		},
		{
			name:      "root path",
			input:     "/",
			wantElems: nil,
		},
		{
			name:      "path with hyphenated element",
			input:     "/gnb/rrc/connected-ues",
			wantElems: []string{"gnb", "rrc", "connected-ues"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePath(tt.input)
			require.NotNil(t, got)

			if tt.wantElems == nil {
				assert.Empty(t, got.Elem, "expected no path elements")
				return
			}

			require.Len(t, got.Elem, len(tt.wantElems))
			for i, want := range tt.wantElems {
				assert.Equal(t, want, got.Elem[i].Name, "elem[%d]", i)
			}
		})
	}
}

func TestParsePath_ProducesValidGNMIPath(t *testing.T) {
	// Verify the returned proto is well-formed and can be used in a Subscription.
	path := parsePath("/gnb/cell/throughput/downlink")
	sub := &gpb.Subscription{
		Path:           path,
		Mode:           gpb.SubscriptionMode_SAMPLE,
		SampleInterval: uint64(10 * time.Second),
	}
	assert.Equal(t, gpb.SubscriptionMode_SAMPLE, sub.Mode)
	assert.Len(t, sub.Path.Elem, 4)
}

func TestRegister_DefaultRegistry(t *testing.T) {
	// The init() in register.go populates DefaultRegistry with "gnmi-gnb".
	names := collector.DefaultRegistry.List()
	assert.Contains(t, names, "gnmi-gnb")

	// Verify factory produces a collector with the correct name.
	c, err := collector.DefaultRegistry.Get("gnmi-gnb")
	require.NoError(t, err)
	assert.Equal(t, "gnmi-gnb", c.Name())
}

func TestCollector_Collect_IntegrationSkip(t *testing.T) {
	t.Skip("requires gNMI server — integration test for Task 19 simulator")
}
