package emitter_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/simulator/emitter"
	"github.com/argus-5g/argus/simulator/engine"
)

func testScenario() engine.Scenario {
	return engine.Scenario{
		Name:        "emitter-test",
		Description: "test scenario for prometheus emitter",
		NFs: []engine.SimulatedNF{
			{
				Type:       "AMF",
				Vendor:     "free5gc",
				InstanceID: "amf-001",
				Protocol:   "prometheus",
				Port:       0,
				Metrics: []engine.BaseMetric{
					{
						Name:          "amf_n1_message_total",
						Labels:        map[string]string{"msg_type": "registration_request"},
						Type:          "counter",
						Baseline:      1000,
						RatePerSecond: 10,
					},
					{
						Name:     "amf_connected_ue",
						Type:     "gauge",
						Baseline: 950,
					},
				},
			},
		},
	}
}

func startEmitter(t *testing.T, eng *engine.Engine) (*emitter.Prometheus, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	p := emitter.NewPrometheus(eng, "amf-001", ":0")

	started := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		// Signal once the listener is ready — we do this by starting in a goroutine
		// and waiting for Port() to return non-zero.
		close(started)
		errCh <- p.Start(ctx)
	}()

	<-started

	// Wait for the listener to bind.
	deadline := time.Now().Add(2 * time.Second)
	for p.Port() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.NotEqual(t, 0, p.Port(), "emitter failed to bind listener")

	t.Cleanup(func() {
		cancel()
		// Drain error channel.
		<-errCh
	})

	return p, cancel
}

func TestPromEmitter_ServesMetrics(t *testing.T) {
	eng := engine.New(testScenario())
	p, _ := startEmitter(t, eng)

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", p.Port())
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	text := string(body)

	// Verify Prometheus exposition format content.
	assert.Contains(t, text, "# TYPE amf_n1_message_total counter")
	assert.Contains(t, text, "# TYPE amf_connected_ue gauge")
	assert.Contains(t, text, `amf_n1_message_total{msg_type="registration_request"}`)
	assert.True(t, strings.Contains(text, "amf_connected_ue"), "should contain gauge metric")
}

func TestPromEmitter_AdvancesClock(t *testing.T) {
	eng := engine.New(testScenario())
	p, _ := startEmitter(t, eng)

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", p.Port())

	// First scrape — establishes baseline. The counter starts at 1000 and the
	// emitter advances by real elapsed since Start. Capture the initial value.
	resp1, err := http.Get(url)
	require.NoError(t, err)
	body1, err := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	require.NoError(t, err)

	// Extract counter value from first scrape.
	val1 := extractMetricValue(t, string(body1), "amf_n1_message_total")

	// Wait a bit so the clock advances on next scrape.
	time.Sleep(200 * time.Millisecond)

	// Second scrape — counter should have increased.
	resp2, err := http.Get(url)
	require.NoError(t, err)
	body2, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.NoError(t, err)

	val2 := extractMetricValue(t, string(body2), "amf_n1_message_total")
	assert.Greater(t, val2, val1, "counter should increase between scrapes")
}

func TestPromEmitter_Port(t *testing.T) {
	eng := engine.New(testScenario())
	p, _ := startEmitter(t, eng)

	port := p.Port()
	assert.NotEqual(t, 0, port, "Port() should return non-zero after Start")
	assert.Greater(t, port, 1024, "Port() should be an unprivileged port")
}

// extractMetricValue parses a float64 from a Prometheus exposition line matching
// the given metric name. Returns the first match.
func extractMetricValue(t *testing.T, body, metricName string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") || len(strings.TrimSpace(line)) == 0 {
			continue
		}
		if strings.HasPrefix(line, metricName) {
			// Format: "metric_name{labels} value" or "metric_name value"
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			var val float64
			_, err := fmt.Sscanf(parts[len(parts)-1], "%f", &val)
			require.NoError(t, err, "failed to parse metric value from line: %s", line)
			return val
		}
	}
	t.Fatalf("metric %s not found in output:\n%s", metricName, body)
	return 0
}
