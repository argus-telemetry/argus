# Argus

**Unified 5G telemetry normalization for multi-vendor networks.**

Argus ingests raw telemetry from heterogeneous 5G network functions — free5GC, Open5GS, OAI gNodeBs — and normalizes it into a single, 3GPP TS 28.552-grounded schema. One Prometheus endpoint, one Grafana dashboard, regardless of how many vendors are in your RAN/core.

## Problem

Every 5G vendor exposes different metric names, label conventions, and counter semantics. An `amf_n1_message_total` in free5GC is `open5gs_amf_registration_total` in Open5GS. Operators running multi-vendor deployments end up maintaining per-vendor dashboards, per-vendor alerting rules, and per-vendor SLA computation logic — all representing the same 3GPP-defined KPIs.

Argus eliminates this by sitting between your NFs and your observability stack. It scrapes vendor-native telemetry, maps it through a declarative schema grounded in 3GPP specs, computes derived KPIs (success rates, loss ratios), and exposes a unified Prometheus endpoint.

## Architecture

![Argus Architecture](docs/design.png)

## Quickstart

Spin up the full stack — simulator, Argus, Prometheus, Grafana — in one command:

```bash
cd examples/quickstart
docker compose up --build -d
```

Open Grafana at [http://localhost:3000](http://localhost:3000) (anonymous access enabled). The pre-provisioned **Argus 5G NOC** dashboard shows registration success rates, connected UEs, PDU sessions, UL/DL throughput, and collector health.

Scrape Argus directly:

```bash
curl -s localhost:8080/metrics | grep argus_5g
```

Tear down:

```bash
docker compose down
```

## Configuration

Argus reads a YAML config file (default: `argus.yaml`):

```yaml
schema_dir: schema/v1

collectors:
  - name: free5gc-amf
    endpoint: http://amf:9090
    interval: 15s
  - name: free5gc-smf
    endpoint: http://smf:9091
    interval: 15s
  - name: free5gc-upf
    endpoint: http://upf:9092
    interval: 15s

output:
  prometheus:
    listen: ":8080"
```

### Collector Names

| Name | Protocol | NF Type |
|------|----------|---------|
| `free5gc-amf` | Prometheus | AMF |
| `free5gc-smf` | Prometheus | SMF |
| `free5gc-upf` | Prometheus | UPF |
| `open5gs-amf` | Prometheus | AMF |
| `open5gs-smf` | Prometheus | SMF |
| `open5gs-upf` | Prometheus | UPF |
| `gnmi-gnb` | gNMI | gNodeB |

The gNMI collector requires additional config in the `extra` block:

```yaml
- name: gnmi-gnb
  endpoint: gnb:9339
  interval: 10s
  extra:
    gnmi_paths:
      - /gnb/cell/prb/utilization
      - /gnb/cell/throughput/downlink
    sample_interval: "5s"  # optional, defaults to interval
```

## Schema

Schemas live in `schema/v1/` as YAML files — one per NF type (AMF, SMF, UPF, gNB, Slice). Each schema defines:

- **KPIs**: normalized metric names, units, 3GPP spec references
- **Derived KPIs**: formulas computing success rates and ratios from base KPIs
- **Vendor mappings**: how each vendor's raw metrics map to the unified KPIs

Example mapping (AMF registration success rate):

```yaml
# schema/v1/amf.yaml
kpis:
  - name: registration.success_rate
    unit: ratio
    spec_ref: "3GPP TS 28.552 §5.1.1.3"
    derived: true
    formula: >-
      registration.attempt_count > 0
      ? (registration.attempt_count - registration.failure_count)
        / registration.attempt_count
      : 0
    depends_on:
      - registration.attempt_count
      - registration.failure_count

mappings:
  free5gc:
    metrics:
      registration.attempt_count:
        prometheus_metric: amf_n1_message_total
        labels: {msg_type: registration_request}
        type: counter
```

Full schema reference: [`schema/v1/`](schema/v1/)

## Adding a Connector

See [`docs/adding-a-connector.md`](docs/adding-a-connector.md) for how to write a new vendor collector plugin.

## Simulator

`argus-sim` generates protocol-identical telemetry for testing without real NFs:

```bash
go run ./cmd/argus-sim --scenario simulator/scenarios/steady_state.yaml
```

Available scenarios:

| Scenario | Description |
|----------|-------------|
| `steady_state.yaml` | Healthy 5G core — all KPIs nominal |
| `alarm_storm.yaml` | Registration storm + UE drop on AMF |
| `slice_sla_breach.yaml` | UPF throughput drop + latency spike |

## Building

```bash
# Build both binaries
make build

# Run tests
make test

# Run vet + test
make check

# Integration test (requires no external dependencies)
go test ./test/integration/... -tags=integration -v -race
```

## Roadmap

- **v0.1** (current): free5GC + Open5GS collectors, simulator (Prometheus + gNMI), Prometheus output, Grafana dashboard
- **v0.2**: OAI RAN collector, Kafka pipeline backend, OpenTelemetry export
- **v1.0**: Nokia + Ericsson vendor stubs, alerting rules, multi-cluster federation

## Contributing

Contributions welcome. Please open an issue first to discuss non-trivial changes.

## License

Apache 2.0
