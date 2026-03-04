# Certification: argus-certify

`argus-certify` validates that Argus correlation rules produce correct events for a given scenario. It runs the full normalization + correlation pipeline in-process using the simulator engine as stimulus -- no running Argus deployment required.

This is the acceptance gate for vendor connectors, correlation rule changes, and schema registry modifications. If `argus-certify matrix` passes, the pipeline behaves correctly across all supported vendors and scenario types.

## Why Certification Matters

Vendor telemetry is heterogeneous. free5GC exposes `free5gc_nas_msg_received_total{name=RegistrationRequest}` as a NAS-level counter; Open5GS exposes `open5gs_amf_registration_total{status=attempted}` as an AMF aggregate. Both must normalize to the same `registration.attempt_count` KPI and trigger the same `RegistrationStorm` correlation rule under identical stimulus conditions.

`argus-certify` guarantees this property holds by:

1. Simulating vendor-specific metric streams via scenario YAML
2. Feeding them through the real normalizer engine with the real schema registry
3. Running the correlator against normalized output
4. Asserting that the expected correlation events fire (or don't fire) within time bounds

## Commands

### `argus-certify run`

Executes a single scenario and reports pass/fail per assertion.

```bash
argus-certify run --scenario test/scenarios/matrix/free5gc_alarm_storm.yaml \
  --timeout 60s \
  --verbose \
  --output text
```

Flags:
| Flag | Default | Description |
|------|---------|-------------|
| `--scenario` | (required) | Path to scenario YAML |
| `--timeout` | `60s` | Maximum wall-clock time for the scenario run |
| `--verbose` | `false` | Show per-field assertion checks |
| `--output` | `text` | Output format: `text` or `json` |

Text output:

```
argus-certify v0.3.0
Running scenario: free5gc_alarm_storm

[PASS] RegistrationStorm fired within 2.34s (limit: 15s)

Passed: 1/1 assertions
Duration: 3.01s
```

Verbose output adds field-level checks:

```
[PASS] RegistrationStorm fired within 2.34s (limit: 15s)
       severity eq ✓
       affected_nfs subset ✓
```

JSON output (`--output json`):

```json
{
  "passed": true,
  "duration": "3.01s",
  "scenario": "free5gc_alarm_storm",
  "total": 1,
  "pass_count": 1,
  "details": [
    {
      "rule": "RegistrationStorm",
      "passed": true,
      "elapsed": "2.34s",
      "limit": "15s",
      "checks": [
        {"field": "severity", "op": "eq", "passed": true},
        {"field": "affected_nfs", "op": "subset", "passed": true}
      ]
    }
  ]
}
```

### `argus-certify matrix`

Runs all scenarios in a directory, groups results by assertion rule, and reports a cross-vendor matrix.

```bash
argus-certify matrix --matrix-dir test/scenarios/matrix/ --timeout 60s
```

Flags:
| Flag | Default | Description |
|------|---------|-------------|
| `--matrix-dir` | (required) | Directory containing scenario YAMLs |
| `--timeout` | `60s` | Per-scenario timeout |

Output:

```
argus-certify matrix --matrix-dir test/scenarios/matrix/

Assertion: RegistrationStorm fires on alarm_storm
  free5gc    [PASS] 2.34s
  open5gs    [PASS] 2.51s

Assertion: No false positives on steady_state
  free5gc    [PASS] 5.01s
  open5gs    [PASS] 5.01s

Matrix: 4/4 passed across 2 vendors
```

Exit code 1 if any assertion fails. The grouping strips vendor prefixes (`free5gc_alarm_storm` -> `alarm_storm`) to align scenarios across vendors.

### `argus-certify list-scenarios`

Lists all valid scenario YAMLs in a directory.

```bash
argus-certify list-scenarios test/scenarios/matrix/
```

Output:

```
SCENARIO                  EXPECTED EVENTS   DESCRIPTION
free5gc_alarm_storm       1                 Registration storm scenario — free5GC vendor metrics
free5gc_steady_state      0                 Healthy 5G core — free5GC vendor metrics, zero false positives
open5gs_alarm_storm       1                 Registration storm scenario — Open5GS vendor metrics
open5gs_steady_state      0                 Healthy 5G core — Open5GS vendor metrics, zero false positives
```

## Writing a Custom Scenario

A scenario YAML defines simulated NF instances, their metric baselines, timed events (anomalies), and expected correlation events.

### Scenario Structure

```yaml
name: myvendor_alarm_storm
description: Registration storm with UE connectivity drop
duration: 300  # simulation duration in seconds (0 = run until timeout)

nfs:
  - type: AMF
    vendor: myvendor
    instance_id: amf-001
    protocol: prometheus
    port: 9090
    metrics:
      - name: myvendor_registration_attempts_total
        labels: {result: attempted}
        type: counter
        baseline: 10000          # initial counter value
        rate_per_second: 15      # steady-state increment rate
      - name: myvendor_ue_connected
        type: gauge
        baseline: 950
        jitter: 50               # +/- random noise for gauges
    events:
      - name: registration_storm
        start_sec: 60            # storm begins at T+60s
        duration_sec: 120        # lasts 2 minutes
        metric: myvendor_registration_attempts_total
        rate_scale: 50           # multiply counter rate by 50x
      - name: ue_drop
        start_sec: 60
        duration_sec: 120
        metric: myvendor_ue_connected
        override: 200            # set gauge to fixed value

expected_events:
  - rule: RegistrationStorm
    severity: critical
    within_seconds: 15
    affected_nfs: [AMF]
    evidence:
      failure_rate:
        gt: 0.1
      connected_ues:
        lt: 500
```

### NF Fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | NF type matching schema namespace: `AMF`, `SMF`, `UPF`, `GNB` |
| `vendor` | string | Vendor key matching a `mappings` entry in the schema YAML |
| `instance_id` | string | Unique NF instance identifier |
| `protocol` | string | `prometheus` or `gnmi` |
| `port` | int | Emitter listen port (used in live simulator mode) |
| `metrics` | list | Baseline metric definitions |
| `events` | list | Timed anomaly overrides |

### Metric Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Vendor-native metric name (must match schema mapping) |
| `labels` | map | Label set for the metric series |
| `type` | string | `counter` or `gauge` |
| `baseline` | float64 | Initial value |
| `rate_per_second` | float64 | Counter increment rate in steady state |
| `jitter` | float64 | Gauge noise range (+/-) |

### Event Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Human-readable event name |
| `start_sec` | int | Seconds after scenario start to begin the event |
| `duration_sec` | int | How long the event lasts |
| `metric` | string | Target metric name to override |
| `labels` | map | Optional: target specific label set within the metric |
| `rate_scale` | float64 | Counter: multiply steady-state rate by this factor |
| `override` | float64 | Gauge: set to this fixed value during the event |

## expected_events Schema Reference

`expected_events` is a list of assertions about correlation events that should (or should not) fire during the scenario.

### Empty List = Zero Events

```yaml
expected_events: []
```

Asserts that no correlation events are produced during the scenario's duration. The asserter waits 5 seconds after the simulation completes and fails if any event arrives. This is the steady-state false-positive check.

### Assertion Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `rule` | string | yes | Correlation rule name (e.g. `RegistrationStorm`, `SessionDrop`, `RANCoreDivergence`) |
| `severity` | string | no | Expected severity: `critical`, `warning`, `info`. If set, must match exactly. |
| `within_seconds` | int | no | Maximum sim-time seconds for the event to fire. Defaults to the scenario timeout. |
| `affected_nfs` | list[string] | no | Expected NF types in the event's `AffectedNFs` field. Asserted as subset (all listed must be present, extras allowed). |
| `evidence` | map | no | Field-level assertions on the event's `Evidence` map. |

### Evidence Assertion Operators

Evidence assertions are keyed by the evidence field name (e.g. `failure_rate`, `connected_ues`) and support these comparison operators:

| Operator | YAML Key | Semantics |
|----------|----------|-----------|
| Greater than | `gt` | `actual > expected` |
| Less than | `lt` | `actual < expected` |
| Greater than or equal | `gte` | `actual >= expected` |
| Less than or equal | `lte` | `actual <= expected` |
| Equal | `eq` | `actual == expected` |

Multiple operators on the same field are ANDed:

```yaml
evidence:
  failure_rate:
    gt: 0.1
    lt: 0.9
```

This asserts `0.1 < failure_rate < 0.9`.

### Built-in Assertion Checks

Beyond evidence operators, the asserter automatically checks:

| Check | Op | Semantics |
|-------|----|-----------|
| `severity` | `eq` | String equality with expected severity |
| `affected_nfs` | `subset` | All listed NFs present in the event's AffectedNFs |

## Interpreting AssertResult JSON in CI Pipelines

Use `--output json` for machine-readable results. The `AssertResult` schema:

```json
{
  "passed": false,
  "failures": [
    {
      "rule": "RegistrationStorm",
      "expected_within": "15s",
      "reason": "expected RegistrationStorm (severity=critical) within 15s, never observed"
    }
  ],
  "duration": "60.01s",
  "scenario": "free5gc_alarm_storm",
  "total": 1,
  "pass_count": 0,
  "details": [
    {
      "rule": "RegistrationStorm",
      "passed": false,
      "limit": "15s",
      "reason": "expected RegistrationStorm (severity=critical) within 15s, never observed",
      "checks": []
    }
  ]
}
```

Key fields for CI:
- **`passed`**: Top-level boolean. Gate your pipeline on this.
- **`failures`**: Non-empty when `passed` is false. Each failure has a `rule`, `reason`, and optional `expected_within`.
- **`details`**: Per-assertion breakdown with field-level `checks` (when the event was observed but field assertions failed).
- **Exit code**: `argus-certify` exits 1 on any failure, 0 on all-pass.

### jq Recipes

Extract failed rules:

```bash
argus-certify run --scenario s.yaml --output json | jq -r '.failures[].rule'
```

Check pass/fail in a shell conditional:

```bash
if argus-certify matrix --matrix-dir test/scenarios/matrix/ --timeout 30s; then
  echo "Certification passed"
else
  echo "Certification failed" >&2
  exit 1
fi
```

## Example: Integrating argus-certify into a GitOps Pipeline

### GitHub Actions

```yaml
name: argus-certify
on:
  pull_request:
    paths:
      - 'schema/v1/**'
      - 'internal/correlator/**'
      - 'internal/normalizer/**'
      - 'test/scenarios/**'

jobs:
  certify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build argus-certify
        run: go build -o bin/argus-certify ./cmd/certify

      - name: Run certification matrix
        run: |
          bin/argus-certify matrix \
            --matrix-dir test/scenarios/matrix/ \
            --timeout 60s

      - name: Upload results (on failure)
        if: failure()
        run: |
          for f in test/scenarios/matrix/*.yaml; do
            bin/argus-certify run --scenario "$f" --output json \
              > "results/$(basename "$f" .yaml).json" 2>&1 || true
          done

      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: certify-results
          path: results/
```

### Argo CD PreSync Hook

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: argus-certify-presync
  annotations:
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/hook-delete-policy: HookSucceeded
spec:
  template:
    spec:
      containers:
        - name: certify
          image: ghcr.io/argus-5g/argus-certify:latest
          command:
            - argus-certify
            - matrix
            - --matrix-dir
            - /scenarios/matrix/
            - --timeout
            - "60s"
      restartPolicy: Never
  backoffLimit: 0
```

The Job exits non-zero on certification failure, which blocks the Argo CD sync. The operator sees the failure in the Argo CD UI and can inspect the Job logs for the full assertion output.

### Tekton Task

```yaml
apiVersion: tekton.dev/v1beta1
kind: Task
metadata:
  name: argus-certify
spec:
  steps:
    - name: certify
      image: ghcr.io/argus-5g/argus-certify:latest
      script: |
        argus-certify matrix \
          --matrix-dir /workspace/source/test/scenarios/matrix/ \
          --timeout 60s
```

## Adding Scenarios for a New Vendor

When adding a new vendor connector (see [Vendor Connectors Guide](vendor-connectors.md)), you must add at minimum two certification scenarios:

1. **`<vendor>_steady_state.yaml`** -- zero expected events, validates no false positives
2. **`<vendor>_alarm_storm.yaml`** -- at least one expected correlation event under anomaly stimulus

Place both in `test/scenarios/matrix/`. The `matrix` command auto-discovers them and includes the vendor in the cross-vendor assertion matrix.
