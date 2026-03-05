# Quickstart: Zero to Dashboard in 5 Commands

This guide gets Argus running with simulated 5G traffic, a Prometheus backend, and a pre-built Grafana NOC dashboard. Total time: under 3 minutes on a warm Docker cache.

## Prerequisites

- Docker Engine 24+ and Docker Compose v2
- 4 GB free RAM (the full stack runs 6 containers)
- Ports 3000, 8080, 9090-9093 available

## Step 1: Clone and Enter

```bash
git clone https://github.com/argus-5g/argus.git && cd argus
```

## Step 2: Build Images

```bash
docker compose -f examples/quickstart/docker-compose.yaml build
```

Expected output:

```
[+] Building 2/2
 => [argus-sim] ...
 => [argus] ...
```

This builds two images: `argus` (the normalizer pipeline) and `argus-sim` (the 5G traffic simulator running the `steady_state` scenario).

The quickstart uses Redis for counter state (default since v0.5). Redis is included in the docker-compose stack.

## Step 3: Start the Stack

```bash
docker compose -f examples/quickstart/docker-compose.yaml up -d
```

Expected output:

```
[+] Running 6/6
 ✔ Container quickstart-redis-1          Started
 ✔ Container quickstart-argus-sim-1      Started
 ✔ Container quickstart-argus-1          Started
 ✔ Container quickstart-otel-collector-1 Started
 ✔ Container quickstart-prometheus-1     Started
 ✔ Container quickstart-grafana-1        Started
```

The stack topology:

```
argus-sim (ports 9090-9092)    # simulates free5GC AMF, SMF, UPF
    ↓ scrape
argus (:8080/metrics)          # normalizes to 3GPP TS 28.552 KPIs
    ↓ push (OTLP)     ↓ pull (/metrics)
otel-collector         prometheus (:9093)
                           ↓
                       grafana (:3000)
```

## Step 4: Verify KPI Output

```bash
curl -s http://localhost:8080/metrics | grep argus_5g
```

Expected output (subset):

```
argus_5g_amf_registration_attempt_count{vendor="free5gc",nf_instance_id="amf-001"} 15
argus_5g_amf_registration_failure_count{vendor="free5gc",nf_instance_id="amf-001"} 0.05
argus_5g_amf_registration_success_rate{vendor="free5gc",nf_instance_id="amf-001"} 0.9967
argus_5g_amf_ue_connected_count{vendor="free5gc",nf_instance_id="amf-001"} 948
argus_5g_smf_session_active_count{vendor="free5gc",nf_instance_id="smf-001"} 482
```

These are normalized 3GPP TS 28.552 KPIs computed from the raw `free5gc_nas_*` and `free5gc_amf_business_*` metrics emitted by the simulator. Counter-type KPIs (`attempt_count`, `failure_count`) show per-interval deltas; gauge-type KPIs (`ue_connected_count`, `session_active_count`) show current values.

## Step 5: Open the Dashboard

```bash
open http://localhost:3000  # or: xdg-open on Linux
```

Navigate to **Dashboards > 5G NOC**. The pre-provisioned dashboard shows:

- Registration success rate over time
- Connected UE count
- Active PDU session gauge
- Per-collector scrape latency (from `argus_collector_scrape_duration_seconds`)

Anonymous access is enabled (`GF_AUTH_ANONYMOUS_ENABLED=true`), so no login is needed.

## Stack Configuration

The quickstart uses `examples/quickstart/argus.yaml`:

```yaml
schema_dir: "/etc/argus/schema/v1"

store:
  type: redis
  redis:
    addr: "redis:6379"
    key_ttl: 120s

collectors:
  - name: free5gc-amf
    endpoint: "http://argus-sim:9090"
    interval: 15s
  - name: free5gc-smf
    endpoint: "http://argus-sim:9091"
    interval: 15s
  - name: free5gc-upf
    endpoint: "http://argus-sim:9092"
    interval: 15s

output:
  prometheus:
    listen: ":8080"
  otlp:
    endpoint: "otel-collector:4317"
    insecure: true
```

Key points:
- **RedisStore** backs the counter store for delta computation across scrapes. The quickstart uses Redis for parity with production; swap to `type: memory` if you want zero external dependencies.
- **Three collectors** scrape the simulator's per-NF Prometheus endpoints at 15-second intervals.
- **Dual output**: Prometheus exposition at `:8080` for Grafana, OTLP push to the collector for downstream pipelines.

## Teardown

```bash
docker compose -f examples/quickstart/docker-compose.yaml down -v
```

## Next Steps

**Realistic 5G traffic**: The `examples/free5gc/` directory contains a full free5GC core network stack (AMF, SMF, UPF, NRF, AUSF, UDM, UDR, NSSF, PCF) with a simulated UE and gNB. This generates real NAS and PFCP signaling instead of synthetic metrics. See `examples/free5gc/docker-compose.yaml`.

**Validate your deployment**: Run the certification matrix to verify all correlation rules fire correctly across vendors:

```bash
go run ./cmd/certify matrix --matrix-dir test/scenarios/matrix/
```

Run `argus-certify matrix` to validate your deployment.
