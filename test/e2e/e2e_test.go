//go:build e2e

// Package e2e runs end-to-end tests against Argus deployed in a kind cluster.
// These tests assume: kind cluster running, Redis installed, Argus installed via
// Helm with test/e2e/values-e2e.yaml, and argus-sim pod deployed.
package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kubectlRun executes a kubectl command and returns stdout.
func kubectlRun(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "kubectl %v failed: %s", args, string(out))
	return string(out)
}

// scrapeMetrics port-forwards to the argus service and fetches /metrics.
func scrapeMetrics(t *testing.T) string {
	t.Helper()

	// Find a free local port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	localPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "svc/argus", fmt.Sprintf("%d:8080", localPort))
	require.NoError(t, cmd.Start())
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	// Wait for port-forward to be ready.
	var body string
	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", localPort))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return false
		}
		body = string(b)
		return resp.StatusCode == 200
	}, 15*time.Second, 500*time.Millisecond, "port-forward to argus /metrics never became ready")

	return body
}

func TestE2E_ArgusInstalled(t *testing.T) {
	out := kubectlRun(t, "get", "pods", "-l", "app.kubernetes.io/name=argus", "-o", "jsonpath={.items[*].status.phase}")
	assert.Contains(t, out, "Running", "argus pod should be Running")
}

func TestE2E_SimulatorIngestsMetrics(t *testing.T) {
	body := scrapeMetrics(t)

	assert.Contains(t, body, "argus_5g_amf_", "should contain normalized AMF metrics")
	assert.Contains(t, body, "argus_normalizer_records_total", "should expose normalizer records counter")
	assert.Contains(t, body, "argus_collector_scrape_success", "should expose collector scrape success")
}

func TestE2E_MultiWorkerRunning(t *testing.T) {
	body := scrapeMetrics(t)

	// With worker_count=2, expect two worker_id label values in queue_depth
	// (initialized at pool creation for all workers, regardless of dispatch).
	workerLines := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "argus_normalizer_worker_queue_depth") && !strings.HasPrefix(line, "#") {
			workerLines++
		}
	}
	assert.GreaterOrEqual(t, workerLines, 2, "should have at least 2 worker queue_depth series (worker_count=2)")
}

func TestE2E_RedisStoreActive(t *testing.T) {
	body := scrapeMetrics(t)

	assert.Contains(t, body, "argus_counter_state_recovered_total",
		"Redis store should expose state recovery metric")

	// Counter store errors should be zero on a healthy deployment.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "argus_counter_store_errors_total") && !strings.HasPrefix(line, "#") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				assert.Equal(t, "0", parts[len(parts)-1],
					"counter store errors should be zero")
			}
		}
	}
}
