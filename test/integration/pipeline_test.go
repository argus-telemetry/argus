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

	// Blank imports register collector factories on DefaultRegistry.
	_ "github.com/argus-5g/argus/plugins/free5gc"
	_ "github.com/argus-5g/argus/plugins/gnmi"
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
      - name: free5gc_nas_msg_received_total
        labels: {name: RegistrationRequest}
        type: counter
        baseline: 10000
        rate_per_second: 15
      - name: free5gc_nas_nas_msg_sent_total
        labels: {name: RegistrationReject}
        type: counter
        baseline: 30
        rate_per_second: 0.05
      - name: free5gc_nas_msg_received_total
        labels: {name: DeregistrationRequestUEOriginatingDeregistration}
        type: counter
        baseline: 500
        rate_per_second: 2
      - name: free5gc_amf_business_ue_connectivity
        type: gauge
        baseline: 950
        jitter: 50

  - type: SMF
    vendor: free5gc
    instance_id: smf-test
    protocol: prometheus
    port: 0
    metrics:
      - name: free5gc_amf_business_active_pdu_session_current_count
        type: gauge
        baseline: 480
        jitter: 30
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
	for _, nfType := range []string{"AMF", "SMF"} {
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

	// SMF KPIs — only active session count is available from free5gc v4.2.0.
	assert.Contains(t, output, "argus_5g_smf_session_active_count",
		"SMF active PDU sessions gauge missing")

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

const gnbScenario = `
name: gnb_integration_test
description: gNB gNMI integration scenario
duration: 0

nfs:
  - type: GNB
    vendor: oai
    instance_id: gnb-test
    protocol: gnmi
    port: 0
    metrics:
      - name: prb_utilization
        type: gauge
        baseline: 0.72
        jitter: 0.05
      - name: dl_throughput
        type: gauge
        baseline: 500000000
        jitter: 10000000
      - name: ul_throughput
        type: gauge
        baseline: 100000000
        jitter: 5000000
      - name: ho_attempts
        type: counter
        baseline: 100
        rate_per_second: 2
      - name: ho_successes
        type: counter
        baseline: 95
        rate_per_second: 1.9
      - name: rrc_ues
        type: gauge
        baseline: 150
        jitter: 10
      - name: cell_avail
        type: gauge
        baseline: 0.995
        jitter: 0.002
`

// TestEndToEnd_GNMIGnbToPrometheus exercises the full pipeline for gNMI gNB telemetry:
// simulator → gnmi-gnb collector → normalizer → prometheus writer.
// Asserts that normalized 5G RAN KPIs (including derived handover success rate)
// and self-telemetry metrics appear on /metrics.
func TestEndToEnd_GNMIGnbToPrometheus(t *testing.T) {
	var scenario engine.Scenario
	require.NoError(t, yaml.Unmarshal([]byte(gnbScenario), &scenario))

	eng := engine.New(scenario)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// pathToKey maps gNMI schema paths to engine metric keys (scenario name field).
	pathToKey := map[string]string{
		"/gnb/cell/prb/utilization":    "prb_utilization",
		"/gnb/cell/throughput/downlink": "dl_throughput",
		"/gnb/cell/throughput/uplink":   "ul_throughput",
		"/gnb/handover/attempts":        "ho_attempts",
		"/gnb/handover/successes":       "ho_successes",
		"/gnb/rrc/connected-ues":        "rrc_ues",
		"/gnb/cell/availability":        "cell_avail",
	}

	sampleInterval := 300 * time.Millisecond

	// Start gNMI emitter on an ephemeral port.
	gnmiEmitter := emitter.NewGNMI(eng, "gnb-test", ":0", pathToKey, sampleInterval)
	go gnmiEmitter.Start(ctx)

	// Wait for the gRPC listener to bind.
	time.Sleep(200 * time.Millisecond)
	gnmiPort := gnmiEmitter.Port()
	require.NotZero(t, gnmiPort, "gNMI emitter did not bind")

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

	// Wire gnmi-gnb collector. gNMI paths and sample interval go in Extra config.
	gnmiPaths := make([]any, 0, len(pathToKey))
	for p := range pathToKey {
		gnmiPaths = append(gnmiPaths, p)
	}

	c, err := collector.DefaultRegistry.Get("gnmi-gnb")
	require.NoError(t, err)

	require.NoError(t, c.Connect(ctx, collector.CollectorConfig{
		Endpoint: fmt.Sprintf("localhost:%d", gnmiPort),
		Interval: sampleInterval,
		Extra: map[string]any{
			"gnmi_paths":      gnmiPaths,
			"sample_interval": sampleInterval.String(),
		},
	}))

	collName := c.Name()
	ch := make(chan collector.RawRecord, 16)

	// Collect blocks on the gRPC stream — run in a goroutine.
	go func() {
		if err := c.Collect(ctx, ch); err != nil && ctx.Err() == nil {
			t.Logf("collector %s: %v", collName, err)
		}
	}()

	go func() {
		for rec := range ch {
			data, merr := json.Marshal(rec)
			if merr != nil {
				continue
			}
			metrics.RecordScrape(collName, 0, true)
			metrics.CollectorRecordsTotal.WithLabelValues(collName).Inc()
			_ = pipe.Publish(ctx, "raw", data)
			metrics.RecordPublish("raw", 1)
		}
	}()

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

	// Wait for 4 sample intervals: enough for the gNMI stream to deliver multiple
	// updates and the pipeline to propagate through collector → normalizer → writer.
	time.Sleep(4 * sampleInterval)

	// Scrape the output.
	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	output := string(body)

	// --- Assert normalized gNB KPIs ---

	assert.Contains(t, output, "argus_5g_gnb_prb_utilization_ratio",
		"gNB PRB utilization ratio missing")
	assert.Contains(t, output, "argus_5g_gnb_throughput_downlink_bps",
		"gNB downlink throughput missing")
	assert.Contains(t, output, "argus_5g_gnb_handover_success_rate",
		"gNB handover success rate (derived) missing")
	assert.Contains(t, output, "argus_5g_gnb_rrc_connected_ue_count",
		"gNB RRC connected UE count missing")

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
