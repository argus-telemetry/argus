# Argus Operator Runbook

On-call procedures for common Argus failure modes. Each section corresponds to
an alerting rule defined in [alerting.md](alerting.md).

## ArgusCollectorScrapeFailure

**Alert:** `argus_collector_scrape_success == 0` for 2m

**Symptoms:** No new normalized records for the affected collector. Downstream
dashboards go stale.

**Likely causes:**

- Vendor NF endpoint unreachable (network policy, pod restart, DNS failure)
- Authentication failure (expired Secret for vendor API)
- Prometheus parse error (vendor changed metric exposition format)

**Diagnosis:**

1. Check the error class:
   ```bash
   kubectl exec -it deployment/argus -- wget -qO- http://localhost:8080/metrics \
     | grep argus_collector_scrape_error_total
   ```
2. Check argus logs for scrape errors:
   ```bash
   kubectl logs deployment/argus | grep "scrape error"
   ```
3. Curl the vendor endpoint directly from the argus pod:
   ```bash
   kubectl exec -it deployment/argus -- wget -qO- http://{nf-endpoint}/metrics | head -20
   ```
4. If authentication is suspected, check Secret expiry:
   ```bash
   kubectl get secret argus-vendor-creds -o yaml
   ```

**Resolution:**

| error_class | Action |
| --- | --- |
| `connection_refused` | Vendor NF is down — check vendor pod/process status |
| `timeout` | Network policy or firewall blocking the scrape — check NetworkPolicy resources |
| `parse_error` | Vendor changed metric format — update the schema mapping YAML |
| `auth_failure` | Rotate the vendor credentials Secret and restart argus |

---

## ArgusNormalizerQueueSaturated

**Alert:** `argus_normalizer_worker_queue_depth > 200` for 1m

**Symptoms:** Normalization latency increases. Metrics arrive late in dashboards.

**Likely causes:**

- Scrape interval too aggressive for the current `worker_count`
- A single large NF emitting very high cardinality metrics overwhelms one worker
- Redis store latency spike (check `argus_counter_lua_eval_duration_seconds` p99)

**Diagnosis:**

Check dispatch skew to distinguish uniform overload from hot-spot:

```bash
kubectl exec -it deployment/argus -- wget -qO- http://localhost:8080/metrics \
  | grep argus_normalizer_worker_dispatch_skew
```

- **High skew (>50):** One NF instance is overwhelming one worker. The hash
  function is correct, but that NF emits disproportionately many metrics.
- **Low skew (<10):** All workers are busy — uniform overload.

**Resolution:**

| Condition | Action |
| --- | --- |
| Uniform overload | Increase `normalizer.workerCount` (requires Redis store) |
| Hot-spot (high skew) | Increase `workerCount` to redistribute; or reduce scrape frequency for the hot NF |
| Redis latency spike | Check Redis memory (`redis_memory_used_bytes`), connection pool (`redis.poolSize`), and Lua script cache |

---

## ArgusStoreErrors (CRITICAL)

**Alert:** `rate(argus_counter_store_errors_total[5m]) > 0` for 2m

**Symptoms:** Delta calculations are incorrect — rates and derived KPIs
are unreliable until resolved.

**Likely causes:**

- Redis unreachable (pod restart, OOM kill, network partition)
- Redis memory pressure (`maxmemory` exceeded, eviction policy triggered)
- Lua script evicted from Redis script cache

**Diagnosis:**

1. Check Redis connectivity:
   ```bash
   kubectl exec -it deployment/argus -- redis-cli -h {redis-addr} ping
   ```
2. Check error type:
   ```bash
   kubectl exec -it deployment/argus -- wget -qO- http://localhost:8080/metrics \
     | grep argus_counter_store_errors_total
   ```
3. If `error_type=lua_eval`: the Lua script was evicted from Redis script cache.
   Restart argus to re-register the script via `SCRIPT LOAD`.
4. Check Redis memory:
   ```bash
   kubectl exec -it redis-master-0 -- redis-cli info memory | grep used_memory_human
   ```

**Resolution:**

Restore Redis connectivity. Argus automatically recovers counter state from
Redis on reconnect. If Redis was completely flushed, counter deltas will
show the raw value on the next scrape (same as first-scrape behavior) and
self-correct on the subsequent scrape.

---

## ArgusCounterResetStorm (Nokia)

**Alert:** `rate(argus_counter_reset_total[5m]) > 10` for 1m

**Symptoms:** `argus_counter_reset_total` spikes, usually at 00:00 UTC for
Nokia ENM deployments.

**Cause:** Nokia ENM resets all PM counters at midnight UTC. This is expected
vendor behavior, not a fault.

**Impact:** Rates show zero for one collection interval after the reset. The
normalizer detects the reset (`reset_aware: true`) and emits the new counter
value as the delta, so recovery is automatic.

**Resolution:**

This is expected behavior. Silence the `ArgusCounterResetStorm` alert for the
00:00-00:05 UTC window in your Alertmanager configuration:

```yaml
inhibit_rules: []

route:
  receiver: default
  routes:
    - matchers:
        - alertname="ArgusCounterResetStorm"
        - vendor="nokia_enm"
      active_time_intervals:
        - nokia-midnight-reset

time_intervals:
  - name: nokia-midnight-reset
    time_intervals:
      - times:
          - start_time: "00:00"
            end_time: "00:05"
```

For non-Nokia deployments, investigate the cause — unexpected counter resets
indicate NF restarts, crashes, or misconfigured `reset_aware` flags.

---

## ArgusOOOTelemetry

**Alert:** `rate(argus_counter_ooo_total[5m]) > 5` for 5m

**Symptoms:** `argus_counter_ooo_total` elevated. Affected records go to the
dead-letter queue.

**Cause:** gNMI subscription or Prometheus scrape delivering metrics out of
order. Typically caused by clock skew between the gNB and the collector, or
network buffering on high-latency WAN links.

**Diagnosis:**

Compare gNB system clock with collector:
```bash
kubectl exec -it deployment/argus -- date
# vs. gNB management interface time
```

Check which vendor/NF type is affected:
```bash
kubectl exec -it deployment/argus -- wget -qO- http://localhost:8080/metrics \
  | grep argus_counter_ooo_total
```

**Resolution:**

- Sync NTP on the gNB management interface
- Reduce gNMI sample interval to decrease the window for reordering
- If the source is a high-latency WAN link, raise the OOO threshold in
  alerting rules to reduce noise

---

## argus-certify Fails in Production

**Alert:** N/A (manual operation)

**Symptoms:** `argus-certify run` exits 1 against a live deployment.

**Diagnosis:**

1. Run with `--verbose` to see which assertion failed:
   ```bash
   argus-certify run --scenario /path/to/scenario.yaml --verbose
   ```

2. Run with `--output json` and inspect structured output:
   ```bash
   argus-certify run --scenario /path/to/scenario.yaml --output json | jq .
   ```

3. Check if `within_seconds` in the scenario YAML is too tight for your scrape
   interval. The assertion window must be at least:
   ```
   within_seconds >= scrape_interval + correlator.eval_interval + network_latency
   ```

4. Check the correlator `eval_interval` — if set high, correlation events may
   fire after the assertion timeout.

5. Verify the simulator or NF is actively emitting metrics:
   ```bash
   curl http://{nf-endpoint}/metrics | head -20
   ```

**Resolution:**

- Tune `within_seconds` in the scenario YAML to match your deployment's timing
- Ensure the correlator `eval_interval` is shorter than the scenario's
  `within_seconds`
- If the scenario works locally but fails in-cluster, check for network latency
  between argus and the NF endpoint
