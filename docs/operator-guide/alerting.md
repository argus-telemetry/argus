# Alerting

Argus ships Prometheus alerting rules derived from its self-observability metrics.
Rules are available as a standalone PrometheusRule manifest and as a Helm template.

## Installation

**With Prometheus Operator (recommended):**

```bash
# Via Helm (enabled by default)
helm install argus deploy/helm/argus --set alerting.enabled=true

# Standalone manifest
kubectl apply -f deploy/alerts/argus-rules.yaml
```

**Without Prometheus Operator:**

Set `alerting.enabled=false` in Helm values and load the rule expressions
directly into your Prometheus configuration. See `deploy/alerts/argus-rules.yaml`
for the raw PromQL expressions.

## Alert Definitions

### argus.collectors

| Alert | Severity | Trigger | Description |
| --- | --- | --- | --- |
| `ArgusCollectorScrapeFailure` | warning | `argus_collector_scrape_success == 0` for 2m | Collector endpoint unreachable or returning errors. [Runbook](runbook.md#arguscollectorscrapefailure) |
| `ArgusCollectorHighErrorRate` | warning | `rate(argus_collector_scrape_error_total[5m]) > 0.1` for 5m | Error rate exceeds 10% for a vendor/NF type. [Runbook](runbook.md#arguscollectorscrapefailure) |

### argus.normalizer

| Alert | Severity | Trigger | Description |
| --- | --- | --- | --- |
| `ArgusNormalizerQueueSaturated` | warning | `argus_normalizer_worker_queue_depth > 200` for 1m | Worker queue approaching capacity. [Runbook](runbook.md#argusnormalizerqueuesaturated) |
| `ArgusNormalizerHighSkew` | warning | `argus_normalizer_worker_dispatch_skew > 50` for 5m | Uneven work distribution across workers. [Runbook](runbook.md#argusnormalizerqueuesaturated) |
| `ArgusDeadLetterAccumulating` | warning | `rate(argus_dead_letter_total[10m]) > 0` for 10m | Dead letter queue growing. [Runbook](runbook.md#argusootelemetry) |

### argus.store

| Alert | Severity | Trigger | Description |
| --- | --- | --- | --- |
| `ArgusStoreErrors` | **critical** | `rate(argus_counter_store_errors_total[5m]) > 0` for 2m | Counter store errors — delta calculations unreliable. [Runbook](runbook.md#argusstoreerrors-critical) |
| `ArgusCounterResetStorm` | warning | `rate(argus_counter_reset_total[5m]) > 10` for 1m | Mass counter resets (expected for Nokia at 00:00 UTC). [Runbook](runbook.md#arguscounterresetstorm-nokia) |
| `ArgusOOOTelemetry` | warning | `rate(argus_counter_ooo_total[5m]) > 5` for 5m | Out-of-order telemetry from clock skew or jitter. [Runbook](runbook.md#argusootelemetry) |

### argus.correlation

| Alert | Severity | Trigger | Description |
| --- | --- | --- | --- |
| `ArgusRegistrationStorm` | **critical** | `increase(argus_correlator_events_total{rule_name="RegistrationStorm"}[5m]) > 0` | Registration storm detected. [Runbook](runbook.md#argusregistrationstorm) |
| `ArgusSessionDrop` | warning | `increase(argus_correlator_events_total{rule_name="SessionDrop"}[5m]) > 0` | SMF session drop detected. [Runbook](runbook.md#argussessiondrop) |
| `ArgusRANCoreDivergence` | warning | `increase(argus_correlator_events_total{rule_name="RANCoreDivergence"}[5m]) > 0` | RAN/Core fault divergence. [Runbook](runbook.md#argusrancoredivergence) |

## Tuning

- **Queue saturation threshold (200):** Lower to 150 for latency-sensitive deployments; raise to 240 if queue depth spikes are transient and resolve within seconds.
- **Counter reset rate (10/5m):** Nokia deployments reset all PM counters at 00:00 UTC — silence `ArgusCounterResetStorm` for the 00:00-00:05 UTC window in alertmanager.
- **OOO telemetry threshold (5/5m):** Raise for high-jitter WAN links between gNBs and the collector; lower for co-located deployments where OOO is unexpected.
- **Correlation event alerts (for: 0m):** These fire immediately on event detection. Add `for: 5m` if you prefer debouncing over fast detection.

## Custom Alerts

All `argus_*` metrics are available for custom alerting rules. Example:

```yaml
- alert: ArgusHighNormalizationFailureRate
  expr: rate(argus_normalizer_total_failures_total[5m]) / rate(argus_normalizer_records_total[5m]) > 0.05
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Normalization failure rate >5% for {{ $labels.vendor }}/{{ $labels.nf_type }}"
```

See [self.go](../../internal/telemetry/self.go) for the full metric inventory.
