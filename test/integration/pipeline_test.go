//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/normalizer"
	promwriter "github.com/argus-5g/argus/internal/output/prometheus"
	"github.com/argus-5g/argus/internal/pipeline"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/argus-5g/argus/internal/telemetry"
	"github.com/argus-5g/argus/simulator/emitter"
	"github.com/argus-5g/argus/simulator/engine"

	// Blank import registers free5gc collector factories on DefaultRegistry.
	_ "github.com/argus-5g/argus/plugins/free5gc"
)

const steadyStateScenario = `
name: integration_test
description: Minimal scenario for integration testing
duration: 0

nfs:
  - type: AMF
    vendor: free5gc
    instance_id: amf-test
    protocol: prometheus
    port: 0
    metrics:
      - name: amf_n1_message_total
        labels: {msg_type: registration_request}
        type: counter
        baseline: 10000
        rate_per_second: 15
      - name: amf_n1_message_total
        labels: {msg_type: registration_reject}
        type: counter
        baseline: 30
        rate_per_second: 0.05
      - name: amf_n1_message_total
        labels: {msg_type: deregistration_request}
        type: counter
        baseline: 500
        rate_per_second: 2
      - name: amf_connected_ue
        type: gauge
        baseline: 950
        jitter: 50
      - name: amf_handover_total
        labels: {result: attempt}
        type: counter
        baseline: 5000
        rate_per_second: 5
      - name: amf_handover_total
        labels: {result: success}
        type: counter
        baseline: 4900
        rate_per_second: 4.9

  - type: SMF
    vendor: free5gc
    instance_id: smf-test
    protocol: prometheus
    port: 0
    metrics:
      - name: smf_pdu_session_total
        labels: {status: requested}
        type: counter
        baseline: 8000
        rate_per_second: 10
      - name: smf_pdu_session_total
        labels: {status: failed}
        type: counter
        baseline: 20
        rate_per_second: 0.03
      - name: smf_pdu_session_total
        labels: {status: released}
        type: counter
        baseline: 7500
        rate_per_second: 9
      - name: smf_pdu_session_active
        type: gauge
        baseline: 480
        jitter: 30
      - name: smf_pdu_session_establishment_latency_ms
        type: gauge
        baseline: 12.5
        jitter: 3

  - type: UPF
    vendor: free5gc
    instance_id: upf-test
    protocol: prometheus
    port: 0
    metrics:
      - name: upf_throughput_bps
        labels: {direction: uplink}
        type: gauge
        baseline: 500000000
        jitter: 50000000
      - name: upf_throughput_bps
        labels: {direction: downlink}
        type: gauge
        baseline: 2000000000
        jitter: 200000000
      - name: upf_packet_total
        labels: {direction: sent}
        type: counter
        baseline: 1000000
        rate_per_second: 50000
      - name: upf_packet_total
        labels: {direction: received}
        type: counter
        baseline: 999500
        rate_per_second: 49990
      - name: upf_session_active
        type: gauge
        baseline: 480
        jitter: 30
      - name: upf_user_plane_latency_ms
        type: gauge
        baseline: 1.2
        jitter: 0.3
`

// TestEndToEnd_Free5GCToPrometheus exercises the full pipeline:
// simulator → free5gc collector → normalizer → prometheus writer.
// Asserts that normalized 5G KPIs and self-telemetry metrics appear on /metrics.
func TestEndToEnd_Free5GCToPrometheus(t *testing.T) {
	var scenario engine.Scenario
	require.NoError(t, yaml.Unmarshal([]byte(steadyStateScenario), &scenario))

	eng := engine.New(scenario)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Prometheus emitters for each simulated NF on ephemeral ports.
	type nfInfo struct {
		nfType   string
		emitter  *emitter.Prometheus
	}
	var nfs []nfInfo
	for _, nf := range scenario.NFs {
		p := emitter.NewPrometheus(eng, nf.InstanceID, ":0")
		go p.Start(ctx)
		nfs = append(nfs, nfInfo{nfType: nf.Type, emitter: p})
	}

	// Wait for emitters to bind.
	time.Sleep(200 * time.Millisecond)

	nfPorts := make(map[string]int)
	for _, nf := range nfs {
		port := nf.emitter.Port()
		require.NotZero(t, port, "emitter for %s did not bind", nf.nfType)
		nfPorts[nf.nfType] = port
	}

	// Load schema registry.
	reg, err := schema.LoadFromDir("../../schema/v1")
	require.NoError(t, err)

	// Build pipeline.
	pipe := pipeline.NewChannelPipeline(64)
	defer pipe.Close()

	normEngine := normalizer.NewEngine(reg)

	writer := promwriter.NewWriter(":0")
	defer writer.Close()

	metrics := telemetry.NewMetrics()
	metrics.Register(writer.Registry())

	ts := httptest.NewServer(writer.Handler())
	defer ts.Close()

	// Wire collectors for each NF type.
	scrapeInterval := 300 * time.Millisecond
	for _, nfType := range []string{"AMF", "SMF", "UPF"} {
		port := nfPorts[nfType]
		collName := "free5gc-" + strings.ToLower(nfType)

		c, err := collector.DefaultRegistry.Get(collName)
		require.NoError(t, err)

		require.NoError(t, c.Connect(ctx, collector.CollectorConfig{
			Endpoint: fmt.Sprintf("http://localhost:%d", port),
			Interval: scrapeInterval,
		}))

		ch := make(chan collector.RawRecord, 16)
		name := c.Name()

		go func() {
			if err := c.Collect(ctx, ch); err != nil && ctx.Err() == nil {
				t.Logf("collector %s: %v", name, err)
			}
		}()

		go func() {
			for rec := range ch {
				data, merr := json.Marshal(rec)
				if merr != nil {
					continue
				}
				metrics.RecordScrape(name, 0, true)
				metrics.CollectorRecordsTotal.WithLabelValues(name).Inc()
				_ = pipe.Publish(ctx, "raw", data)
				metrics.RecordPublish("raw", 1)
			}
		}()
	}

	// Normalizer goroutine.
	rawCh, err := pipe.Subscribe(ctx, "raw")
	require.NoError(t, err)

	go func() {
		for data := range rawCh {
			var rec collector.RawRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				continue
			}
			if !normEngine.CanHandle(rec) {
				metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
				continue
			}
			result, err := normEngine.Normalize(rec)
			if err != nil {
				metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
				continue
			}
			metrics.RecordNormalize(rec.Source.Vendor, rec.Source.NFType, len(result.Records), len(result.Partial))
			if len(result.Records) > 0 {
				ndata, err := json.Marshal(result.Records)
				if err != nil {
					continue
				}
				_ = pipe.Publish(ctx, "normalized", ndata)
				metrics.RecordPublish("normalized", 1)
			}
		}
	}()

	// Output goroutine.
	normCh, err := pipe.Subscribe(ctx, "normalized")
	require.NoError(t, err)

	go func() {
		for data := range normCh {
			var records []normalizer.NormalizedRecord
			if err := json.Unmarshal(data, &records); err != nil {
				continue
			}
			werr := writer.Write(ctx, records)
			metrics.RecordWrite(writer.Name(), len(records), werr)
		}
	}()

	// Wait for at least 3 scrape intervals: first scrape (baseline), second scrape (delta),
	// plus processing time for the pipeline to propagate records through all stages.
	time.Sleep(4 * scrapeInterval)

	// Scrape the output.
	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	output := string(body)

	// --- Assert normalized 5G KPIs ---

	// AMF KPIs.
	assert.Contains(t, output, "argus_5g_amf_registration_attempt_count",
		"AMF registration attempt count missing")
	assert.Contains(t, output, "argus_5g_amf_registration_success_rate",
		"AMF registration success rate (derived) missing")
	assert.Contains(t, output, "argus_5g_amf_ue_connected_count",
		"AMF connected UE gauge missing")
	assert.Contains(t, output, "argus_5g_amf_handover_attempt_count",
		"AMF handover attempt count missing")

	// SMF KPIs.
	assert.Contains(t, output, "argus_5g_smf_session_active_count",
		"SMF active PDU sessions gauge missing")
	assert.Contains(t, output, "argus_5g_smf_session_establishment_success_rate",
		"SMF session establishment success rate (derived) missing")

	// UPF KPIs.
	assert.Contains(t, output, "argus_5g_upf_throughput_uplink_bps",
		"UPF uplink throughput gauge missing")
	assert.Contains(t, output, "argus_5g_upf_throughput_downlink_bps",
		"UPF downlink throughput gauge missing")
	assert.Contains(t, output, "argus_5g_upf_session_active_count",
		"UPF active sessions gauge missing")

	// --- Assert self-telemetry ---

	assert.Contains(t, output, "argus_collector_scrape_success",
		"self-telemetry: collector scrape success missing")
	assert.Contains(t, output, "argus_normalizer_records_total",
		"self-telemetry: normalizer records total missing")
	assert.Contains(t, output, "argus_collector_records_total",
		"self-telemetry: collector records total missing")
	assert.Contains(t, output, "argus_pipeline_publish_total",
		"self-telemetry: pipeline publish total missing")

	t.Logf("Output contains %d bytes of metrics", len(body))
}
