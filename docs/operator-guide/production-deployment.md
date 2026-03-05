# Production Deployment

This guide covers deploying Argus to Kubernetes with the Helm chart, configuring Redis-backed counter persistence, scaling with the worker pool, and monitoring the pipeline.

## Prerequisites

- Kubernetes 1.27+ cluster
- Helm 3.12+
- Redis 7+ (standalone or Sentinel) -- required for multi-worker deployments
- Prometheus Operator (optional, for ServiceMonitor auto-discovery)
- KEDA (optional, for Prometheus-driven autoscaling)

## Helm Install

### Minimal Redis-Backed Deployment

```bash
helm install argus deploy/helm/argus/ \
  --set store.type=redis \
  --set store.redis.addr="redis.infra.svc:6379" \
  --set store.redis.password="$REDIS_PASSWORD" \
  --set normalizer.workerCount=4 \
  --set normalizer.queueDepth=512 \
  --set collectors[0].name=amf-free5gc \
  --set collectors[0].endpoint="http://amf.5g.svc:9091" \
  --set collectors[0].interval=15s \
  --set output.prometheus.listen=":8080" \
  --set output.otlp.enabled=true \
  --set output.otlp.endpoint="otel-collector.observability.svc:4317"
```

### From values.yaml Override

```bash
helm install argus deploy/helm/argus/ -f my-values.yaml
```

### Multi-Worker with Redis

The critical constraint: **multi-worker normalization (`workerCount > 1`) requires `store.type=redis`**. The bbolt and memory stores are single-writer only. Argus fails fast at startup if this invariant is violated.

Redis enables concurrent counter updates from multiple normalizer goroutines via an atomic Lua script that collapses Get+Put into a single EVALSHA round trip. This eliminates the race window between delta computation and persistence.

## values.yaml Reference

### Image

```yaml
image:
  repository: ghcr.io/argus-5g/argus
  tag: ""              # defaults to Chart.appVersion (0.2.1)
  pullPolicy: IfNotPresent
```

### Store Configuration

```yaml
store:
  type: redis            # memory | bbolt | redis (Redis is default since v0.5)

  redis:
    addr: "redis-master:6379"
    password: ""         # use store.redis.existingSecret for production
    db: 0
    keyTTL: 120s         # counter key expiry (2x scrape interval recommended)
    dialTimeout: 5s
    readTimeout: 3s
    writeTimeout: 3s
    poolSize: 10         # one connection per normalizer goroutine

  bbolt:
    # DEPRECATED since v0.5 — will be removed in v0.7.
    # Retained for single-instance dev deployments only.
    # Migrate to Redis: see docs/architecture/counter-store-evolution.md
    path: /data/counters.db
    size: 1Gi            # PVC size for StatefulSet volumeClaimTemplate
```

### Normalizer

```yaml
normalizer:
  workerCount: 1         # parallel normalizer goroutines
  queueDepth: 256        # per-worker input channel buffer
```

`workerCount` controls parallelism. Workers are dispatched via consistent hash on `hash(vendor + NFType + instanceID) % workerCount`, so the same NF instance always routes to the same worker. This preserves counter state locality and avoids cross-worker delta corruption.

Set `queueDepth` to at least `2 * number_of_collectors` to avoid back-pressure stalls during scrape bursts.

### Collectors

```yaml
collectors:
  - name: free5gc-amf
    endpoint: "http://amf.5g.svc:9091"
    interval: 15s
  - name: free5gc-smf
    endpoint: "http://smf.5g.svc:9091"
    interval: 15s
```

Each collector scrapes a single NF Prometheus endpoint at the configured interval. The collector auto-detects vendor and NF type from the schema registry mappings.

### Output

```yaml
output:
  prometheus:
    listen: ":8080"      # Prometheus exposition endpoint
  otlp:
    enabled: false
    endpoint: ""         # e.g. "otel-collector.observability.svc:4317"
    insecure: true       # disable TLS for in-cluster gRPC
```

### Correlator

```yaml
correlator:
  enabled: true
  windowSize: 30s        # sliding window for KPI sample aggregation
  evalInterval: 5s       # how often correlation rules are evaluated
```

### ServiceMonitor

```yaml
serviceMonitor:
  enabled: false
  interval: 15s
  labels: {}             # e.g. {release: prometheus-stack}
```

Enable if running Prometheus Operator. The ServiceMonitor targets port `metrics` (8080) on the Argus Deployment (or StatefulSet if using bbolt).

### HPA

```yaml
hpa:
  enabled: false
  minReplicas: 1
  maxReplicas: 4
  metrics:
    - type: Pods
      pods:
        metric:
          name: argus_normalizer_worker_queue_depth
        target:
          type: AverageValue
          averageValue: "200"
```

### KEDA (Alternative to HPA)

```yaml
keda:
  enabled: false
  # pollingInterval: 15
  # cooldownPeriod: 60
  # triggers:
  #   - type: prometheus
  #     metadata:
  #       serverAddress: http://prometheus:9090
  #       query: avg(argus_normalizer_worker_queue_depth)
  #       threshold: "200"
```

### Resources

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

For production with 4 workers and 10+ collectors, bump to `cpu: 1` / `memory: 512Mi` limits.

## Counter Store Durability Guarantees

| Store | Persistence | Multi-Writer | Recovery on Restart | Data Loss Window |
|-------|------------|--------------|---------------------|------------------|
| `memory` | None | No | Full state loss | All counter state |
| `bbolt` | PVC-backed | No (single writer) | Full recovery from bbolt file | None (sync on write) |
| `redis` | Redis persistence (AOF/RDB) | Yes (atomic Lua) | Depends on Redis config | Depends on Redis AOF policy |

### memory

Volatile. Counter state is lost on pod restart. First scrape after restart emits the raw counter value instead of a delta. Acceptable for dev/test, not for production.

### bbolt (DEPRECATED)

> **Deprecated since v0.5.** bbolt will be removed in v0.7. Migrate to Redis using the
> guide in [docs/architecture/counter-store-evolution.md](../architecture/counter-store-evolution.md).

Embedded key-value store backed by a PVC (`ReadWriteOnce`). Single-writer only -- the StatefulSet runs one pod per replica, and bbolt files are not safe for concurrent access across pods.

Durability: bbolt syncs to disk on every write. Data survives pod restarts. The Helm chart provisions a `volumeClaimTemplate` for the `/data` mount.

Limitation: cannot scale beyond `workerCount=1` per replica. For horizontal scaling, use Redis.

### redis

Shared counter store with atomic Lua-based updates. Supports multi-writer access from concurrent normalizer goroutines and multiple Argus pods.

The Lua script (`EVALSHA`) atomically reads the previous value, writes the new value with TTL, and detects counter resets -- all in a single Redis round trip. Latency is tracked via `argus_counter_lua_eval_duration_seconds`.

Key TTL (`keyTTL`, default 120s) auto-expires stale counter entries. Set to 2x your scrape interval to handle one missed scrape without state loss.

Durability depends on your Redis configuration:
- **AOF `always`**: every write persisted, zero data loss, higher latency
- **AOF `everysec`**: up to 1 second of data loss on Redis crash (recommended)
- **RDB only**: up to minutes of data loss depending on save interval

See [docs/architecture/counter-store-evolution.md](../architecture/counter-store-evolution.md) for the v0.5 roadmap: Redis+WAL hybrid that writes a local WAL before Redis round-trips, providing sub-millisecond writes with Redis-level durability.

## Monitoring: argus_* Metrics

All self-observability metrics use the `argus_` prefix. The critical ones to alert on:

### Collector Health

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_collector_scrape_success` | gauge | `== 0` for > 2 intervals | critical |
| `argus_collector_scrape_duration_seconds` | histogram | p99 > 10s | warning |
| `argus_collector_scrape_error_total` | counter | rate > 0 for > 5m | warning |

`argus_collector_scrape_success` is the primary liveness signal for each collector. A `0` value means the last scrape failed. Two consecutive failures (2x scrape interval) indicates the NF endpoint is down or unreachable.

### Normalizer Health

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_normalizer_records_total` | counter | rate == 0 for > 2 intervals | critical |
| `argus_normalizer_partial_failures_total` | counter | rate > 0 sustained | warning |
| `argus_normalizer_total_failures_total` | counter | any increment | critical |

`total_failures` indicates corrupt payloads, wrong protocol, or missing schemas. This should never increment in a healthy deployment. `partial_failures` indicates individual KPI resolution failures -- usually a gauge metric that temporarily disappears.

### Counter Store Health

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_counter_store_errors_total` | counter | any increment | critical |
| `argus_counter_lua_eval_duration_seconds` | histogram | p99 > 50ms | warning |
| `argus_counter_reset_total` | counter | unexpected burst | warning |
| `argus_counter_ooo_total` | counter | any increment | warning |

`counter_store_errors_total` is labeled by `{store_type, error_type}`. Error types include `open`, `read`, `write`, `script_load`. Any store error means counter deltas may be inaccurate.

`counter_reset_total` is expected during NF restarts and Nokia midnight UTC resets. Unexpected bursts indicate a vendor-side issue or misconfigured `reset_aware` flag.

`counter_ooo_total` indicates out-of-order counter updates. Should be zero in normal operation; non-zero suggests clock skew or overlapping scrape windows.

### Correlator Health

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_correlator_events_total` | counter | labeled by `{rule_name, severity}` | informational |
| `argus_correlator_window_evaluations_total` | counter | rate == 0 for > 2x eval_interval | warning |

### Output Health

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_output_write_errors_total` | counter | any increment | critical |
| `argus_output_write_total` | counter | rate == 0 for > 2 intervals | warning |

Write errors typically indicate Prometheus endpoint binding failures or OTLP gRPC connection issues.

### Worker Pool Metrics

| Metric | Type | Alert Condition | Severity |
|--------|------|-----------------|----------|
| `argus_normalizer_worker_queue_depth` | gauge | sustained > 80% of `queueDepth` | warning |
| `argus_normalizer_worker_records_total` | counter | skew > 3x across workers | warning |
| `argus_normalizer_worker_dispatch_skew` | gauge | sustained > 50 | warning |

`queue_depth` is the primary scaling signal (used by HPA). When queue depth approaches `queueDepth`, workers are saturated and normalization latency increases.

`dispatch_skew` measures max queue depth minus min queue depth across workers. High sustained values indicate poor hash distribution -- too many NF instances hashing to the same worker. Increase `workerCount` to rebalance.

## Scaling

### Vertical: Worker Pool

Increase `normalizer.workerCount` to parallelize normalization within a single pod. Workers are dispatched via consistent hash, so adding workers redistributes NF-to-worker assignments.

```yaml
normalizer:
  workerCount: 4    # 4 parallel normalizer goroutines
  queueDepth: 512   # buffer per worker
```

Requires `store.type=redis`. Size `redis.poolSize` to match `workerCount`.

### Horizontal: HPA

The Helm chart includes an HPA template that scales the Deployment (or StatefulSet for bbolt) based on `argus_normalizer_worker_queue_depth`:

```yaml
hpa:
  enabled: true
  minReplicas: 1
  maxReplicas: 4
  metrics:
    - type: Pods
      pods:
        metric:
          name: argus_normalizer_worker_queue_depth
        target:
          type: AverageValue
          averageValue: "200"
```

Requirements:
- Prometheus Adapter or KEDA for custom metrics in the HPA API
- `store.type=redis` (multiple pods share the counter store)
- ServiceMonitor enabled to feed metrics into Prometheus

### Queue Depth as Scaling Signal

`argus_normalizer_worker_queue_depth` is the recommended scaling signal because it directly measures backlog. Alternative signals:

| Signal | Metric | When to Use |
|--------|--------|-------------|
| Queue depth | `argus_normalizer_worker_queue_depth` | Default. Scales on normalization backlog. |
| Scrape latency | `argus_collector_scrape_duration_seconds` | Endpoint latency is the bottleneck (rare). |
| Records/sec | `rate(argus_normalizer_records_total[5m])` | Throughput-based scaling. |

### Sizing Guidelines

| Deployment | Collectors | Worker Count | Redis Pool | CPU/Memory |
|------------|-----------|--------------|------------|------------|
| Dev/Test | 1-5 | 1 | N/A (bbolt) | 100m / 128Mi |
| Small prod | 5-20 | 2 | 4 | 250m / 256Mi |
| Medium prod | 20-50 | 4 | 8 | 500m / 512Mi |
| Large prod | 50+ | 8+ | 16 | 1000m / 1Gi |

Scale `workerCount` proportionally to the number of distinct NF instances being scraped. The hash distribution is uniform when `workerCount` is significantly smaller than the number of NF instances.

## Architecture Reference

For the counter store evolution roadmap (Redis+WAL hybrid in v0.5), see [docs/architecture/counter-store-evolution.md](../architecture/counter-store-evolution.md).
