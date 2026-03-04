# Adding a Vendor Connector

This guide walks through adding a new collector plugin to Argus. A connector has two parts: KPI mappings in the schema YAML, and a Go collector that speaks the vendor's native protocol.

## 1. Define KPI Mappings

Add your vendor's mappings to each relevant `schema/v1/<nf_type>.yaml` file. You don't define new KPIs — those are 3GPP-grounded and already exist. You map your vendor's metric names to them.

```yaml
# schema/v1/amf.yaml — add under the mappings: key

mappings:
  your_vendor:
    source_protocol: prometheus   # or gnmi
    metrics:
      registration.attempt_count:
        prometheus_metric: your_vendor_amf_reg_attempts
        labels:
          status: attempted
        type: counter
        reset_aware: true
        label_match_strategy: exact

      registration.failure_count:
        prometheus_metric: your_vendor_amf_reg_attempts
        labels:
          status: failed
        type: counter
        reset_aware: true
        label_match_strategy: exact

      ue.connected_count:
        prometheus_metric: your_vendor_amf_connected_ues
        type: gauge
        reset_aware: false
        label_match_strategy: exact
```

Every mapping must include:
- `type`: counter or gauge
- `reset_aware`: true for all counters
- `label_match_strategy`: start with `exact`, use `sum_by` if the vendor adds high-cardinality labels

You do not need to map every KPI. Unmapped KPIs will be absent from output, not broken. Map what you can verify.

## 2. Implement the Collector

Create `plugins/your_vendor/collector.go`:

```go
package yourvendor

import (
    "context"
    "time"

    "github.com/argus-5g/argus/internal/collector"
)

type Collector struct {
    cfg  collector.CollectorConfig
    // your client (Prometheus scraper, gNMI client, etc.)
}

func (c *Collector) Name() string {
    return "your-vendor-amf"
}

func (c *Collector) Connect(ctx context.Context, cfg collector.CollectorConfig) error {
    c.cfg = cfg
    // initialize your protocol client
    // validate connectivity — fail fast if endpoint is unreachable
    return nil
}

func (c *Collector) Collect(ctx context.Context, ch chan<- collector.RawRecord) error {
    ticker := time.NewTicker(c.cfg.Interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            payload, err := c.scrape(ctx)
            if err != nil {
                // log error, continue — don't crash the pipeline
                // self-telemetry will record the scrape failure
                continue
            }

            ch <- collector.RawRecord{
                Source: collector.SourceInfo{
                    Vendor:     "your_vendor",
                    NFType:     "AMF",
                    InstanceID: c.cfg.Endpoint,
                    Endpoint:   c.cfg.Endpoint,
                },
                Payload:       payload,
                Protocol:      collector.ProtocolPrometheus,
                Timestamp:     time.Now(),
                SchemaVersion: "v1",
            }
        }
    }
}

func (c *Collector) Close() error {
    // clean up client connections
    return nil
}

func (c *Collector) scrape(ctx context.Context) ([]byte, error) {
    // fetch raw metrics from vendor endpoint
    // return the raw bytes — the normalizer handles parsing
    return nil, nil
}
```

That's ~50 lines. The collector's only job is to fetch raw bytes and tag them with source metadata. All parsing, mapping, and normalization happens in the engine using your schema YAML.

## 3. Register the Collector

Add to `plugins/your_vendor/register.go`:

```go
package yourvendor

import "github.com/argus-5g/argus/internal/collector"

func init() {
    collector.Register("your-vendor-amf", func() collector.Collector {
        return &Collector{}
    })
}
```

Import the plugin in `cmd/argus/plugins.go`:

```go
import _ "github.com/argus-5g/argus/plugins/your_vendor"
```

## 4. Test Against the Simulator

You don't need real hardware. Configure argus-sim to emit metrics in your vendor's format:

```yaml
# simulator/vendors/your_vendor.yaml
vendor: your_vendor
nf_type: AMF
protocol: prometheus
port: 9095
metrics:
  your_vendor_amf_reg_attempts:
    type: counter
    labels: [status]
    baseline:
      attempted: 100.0  # per scrape interval
      failed: 0.3
  your_vendor_amf_connected_ues:
    type: gauge
    baseline: 950.0
    jitter: 50.0  # +/- random variation
```

Run the simulator and point your collector at it:

```bash
make sim SCENARIO=steady_state VENDOR=your_vendor
make run CONNECTORS=your-vendor-amf
```

## 5. Validate Output

Check that your KPIs appear in Argus's `/metrics` output:

```bash
curl -s localhost:8080/metrics | grep argus_5g_amf
```

You should see normalized metrics with correct labels:

```
argus_5g_amf_registration_attempt_count{vendor="your_vendor",nf_instance_id="...",plmn_id="..."} 100
argus_5g_amf_ue_connected_count{vendor="your_vendor",...} 950
```

Also verify self-telemetry shows your collector is healthy:

```
argus_collector_scrape_success{collector="your-vendor-amf"} 1
```

## What You Don't Need to Do

- **Write a normalizer.** The normalization engine handles all vendors using your schema YAML mappings.
- **Define new KPIs.** The KPI catalog is 3GPP-grounded. If your vendor exposes a metric that doesn't map to an existing KPI, open an issue to discuss adding it to the schema.
- **Handle counter resets.** The normalizer handles this using `reset_aware` from your mapping.
- **Compute derived KPIs.** `registration.success_rate` is computed from `attempt_count` and `failure_count` automatically.
- **Touch the output layer.** Your normalized records flow to all configured outputs without any connector-specific code.

## Plugin-Specific Config

If your collector needs vendor-specific configuration (authentication tokens, API versions, etc.), use the `Extra` map in `CollectorConfig`:

```yaml
# argus config
collectors:
  - name: your-vendor-amf
    endpoint: "http://your-vendor-amf:8080"
    interval: 15s
    extra:
      api_version: "v2"
      auth_token_path: "/etc/argus/your-vendor-token"
```

Document every key your plugin reads from `Extra` in a comment at the top of your collector file.

## Checklist

- [ ] KPI mappings added to relevant `schema/v1/*.yaml` files
- [ ] Every mapping has `type`, `reset_aware`, and `label_match_strategy`
- [ ] Collector implements `collector.Collector` interface
- [ ] Collector registered via `init()` + imported in `cmd/argus/plugins.go`
- [ ] Simulator vendor config created for testing
- [ ] Normalized output verified via `/metrics`
- [ ] Self-telemetry shows scrape success
- [ ] `Extra` config keys documented in collector source
