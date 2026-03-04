# Vendor Connectors: Schema Registry and DSL Reference

Argus normalizes heterogeneous vendor telemetry to 3GPP TS 28.552 canonical KPIs through an openly-editable schema registry. Each vendor connector is a YAML mapping that translates vendor-native metric names, labels, and path structures into the unified Argus namespace.

No Go code is required to add a new vendor. The entire connector is defined in YAML.

## How the Schema Registry Works

The schema registry lives in `schema/v1/`, with one YAML file per NF type:

```
schema/v1/
  amf.yaml      # AMF KPIs: registration, handover, UE counts
  smf.yaml      # SMF KPIs: PDU session management
  upf.yaml      # UPF KPIs: throughput, packet drop, GTP tunnel
  gnb.yaml      # gNB KPIs: PRB utilization, RRC, interference
  slice.yaml    # Slice KPIs: per-slice SLA metrics
```

### YAML Structure

Each schema file has three top-level sections:

```yaml
namespace: argus.5g.amf          # unique namespace, convention: argus.5g.<nf_type>
nf_type: AMF                     # NF type identifier
schema_version: v1               # schema version for pipeline compatibility
spec: "3GPP TS 28.552"           # grounding spec

kpis:                            # canonical KPI definitions
  - name: registration.attempt_count
    description: Total number of UE registration attempts received by the AMF
    unit: count
    spec_ref: "3GPP TS 28.552 §5.1.1.1"
    derived: false

  - name: registration.success_rate
    description: Ratio of successful registrations to total attempts
    unit: ratio
    spec_ref: "3GPP TS 28.552 §5.1.1.3"
    derived: true
    formula: "registration.attempt_count > 0 ? (registration.attempt_count - registration.failure_count) / registration.attempt_count : 0"
    depends_on:
      - registration.attempt_count
      - registration.failure_count

mappings:                        # vendor-specific metric translations
  free5gc:
    source_protocol: prometheus
    metrics:
      registration.attempt_count:
        prometheus_metric: free5gc_nas_msg_received_total
        labels:
          name: RegistrationRequest
        type: counter
        reset_aware: true
        label_match_strategy: sum_by
```

### KPI Definition Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Dotted KPI name (e.g. `registration.attempt_count`) |
| `description` | string | Human-readable description |
| `unit` | string | `count`, `ratio`, `bps`, `ms`, `dBm` |
| `spec_ref` | string | 3GPP spec section reference |
| `derived` | bool | `true` if computed from other KPIs |
| `formula` | string | Go expression for derived KPIs. Variables are other KPI names. Supports ternary `a > 0 ? b : c`. |
| `depends_on` | list | KPIs that must resolve before this formula can evaluate |

### Vendor Mapping Fields

| Field | Type | Description |
|-------|------|-------------|
| `source_protocol` | string | `prometheus` or `gnmi` |
| `prometheus_metric` | string | Vendor's Prometheus metric name |
| `gnmi_path` | string | Vendor's gNMI path (for `gnmi` protocol) |
| `labels` | map | Required label key-value pairs for series matching |
| `type` | string | `counter` or `gauge`. Counters get delta computation. |
| `reset_aware` | bool | Handle counter resets (e.g. NF restart) |
| `label_match_strategy` | string | `exact`, `sum_by`, or `any` |

Label match strategies:
- **`exact`**: Series labels must be a superset of the mapping labels. When no labels are specified, any series with that metric name matches.
- **`sum_by`**: Sum values across all series whose labels are a superset of the mapping labels. Use this when a vendor splits counters by sub-type labels.
- **`any`**: Take the first matching series.

## Adding a New Vendor: Step-by-Step

This walkthrough uses the `nokia_enm` mapping in `schema/v1/amf.yaml` as the reference pattern.

### Step 1: Identify Vendor Metrics

Catalog the vendor's exposed metrics for the target NF type. For each Argus KPI, find the corresponding vendor metric name, label set, and type. Document gaps -- not every vendor exposes every KPI.

Example mapping table for a hypothetical vendor:

| Argus KPI | Vendor Metric | Labels | Type |
|-----------|--------------|--------|------|
| `registration.attempt_count` | `myvendor_reg_attempts_total` | `{nf_type=amf}` | counter |
| `registration.failure_count` | `myvendor_reg_failures_total` | `{nf_type=amf}` | counter |
| `ue.connected_count` | `myvendor_ue_connected` | `{}` | gauge |

### Step 2: Add the Mapping to the Schema YAML

Open the appropriate `schema/v1/<nf_type>.yaml` and add a new entry under `mappings`:

```yaml
mappings:
  # ... existing vendors ...

  myvendor:
    source_protocol: prometheus
    metrics:
      registration.attempt_count:
        prometheus_metric: myvendor_reg_attempts_total
        labels:
          nf_type: amf
        type: counter
        reset_aware: true
        label_match_strategy: exact
      registration.failure_count:
        prometheus_metric: myvendor_reg_failures_total
        labels:
          nf_type: amf
        type: counter
        reset_aware: true
        label_match_strategy: exact
      ue.connected_count:
        prometheus_metric: myvendor_ue_connected
        labels: {}
        type: gauge
        reset_aware: false
        label_match_strategy: exact
```

Only map KPIs that the vendor actually exposes. The normalizer handles missing mappings gracefully -- unmapped base KPIs are marked as `Unsupported` (not failures), and derived KPIs that depend on them propagate the skip.

### Step 3: Use DSL Features for Complex Mappings

If the vendor uses hierarchical path-based metric names (common in Nokia ENM, Ericsson PM), use the DSL extensions instead of `prometheus_metric`.

```yaml
myvendor:
  source_protocol: prometheus
  metrics:
    registration.attempt_count:
      source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts"
      transform: "rate(30s)"
      type: counter
      reset_aware: true
      label_match_strategy: exact
      label_extract:
        - segment: 4
          label: instance_id
```

See the [DSL Reference](#dsl-reference) section below for full details.

### Step 4: Write Certification Scenarios

Create two scenario files in `test/scenarios/matrix/`:

**`myvendor_steady_state.yaml`** -- validates zero false positives:

```yaml
name: myvendor_steady_state
description: Healthy 5G core — MyVendor metrics, zero false positives
duration: 0

nfs:
  - type: AMF
    vendor: myvendor
    instance_id: amf-001
    protocol: prometheus
    port: 9090
    metrics:
      - name: myvendor_reg_attempts_total
        labels: {nf_type: amf}
        type: counter
        baseline: 10000
        rate_per_second: 15
      - name: myvendor_reg_failures_total
        labels: {nf_type: amf}
        type: counter
        baseline: 30
        rate_per_second: 0.05
      - name: myvendor_ue_connected
        type: gauge
        baseline: 950
        jitter: 50

expected_events: []
```

**`myvendor_alarm_storm.yaml`** -- validates RegistrationStorm fires:

```yaml
name: myvendor_alarm_storm
description: Registration storm — MyVendor metrics
duration: 300

nfs:
  - type: AMF
    vendor: myvendor
    instance_id: amf-001
    protocol: prometheus
    port: 9090
    metrics:
      - name: myvendor_reg_attempts_total
        labels: {nf_type: amf}
        type: counter
        baseline: 10000
        rate_per_second: 15
      - name: myvendor_reg_failures_total
        labels: {nf_type: amf}
        type: counter
        baseline: 30
        rate_per_second: 0.05
      - name: myvendor_ue_connected
        type: gauge
        baseline: 950
        jitter: 50
    events:
      - name: registration_storm
        start_sec: 60
        duration_sec: 120
        metric: myvendor_reg_attempts_total
        rate_scale: 50
      - name: rejection_spike
        start_sec: 60
        duration_sec: 120
        metric: myvendor_reg_failures_total
        rate_scale: 10000
      - name: ue_drop
        start_sec: 60
        duration_sec: 120
        metric: myvendor_ue_connected
        override: 200

expected_events:
  - rule: RegistrationStorm
    severity: critical
    within_seconds: 15
    affected_nfs: [AMF]
```

### Step 5: Run Certification

```bash
# Run your vendor's scenarios
argus-certify run --scenario test/scenarios/matrix/myvendor_steady_state.yaml --verbose
argus-certify run --scenario test/scenarios/matrix/myvendor_alarm_storm.yaml --verbose

# Run the full matrix to confirm no regressions
argus-certify matrix --matrix-dir test/scenarios/matrix/
```

All assertions must pass before submitting.

### Step 6: Submit to the Community

See [PR Checklist](#pr-checklist) below.

## DSL Reference

The DSL extends the basic `prometheus_metric` / `gnmi_path` mapping with three features: template-based path matching, value transforms, and label extraction.

### SourceTemplate Syntax

`source_template` uses Go `text/template` syntax. Available variables:

| Variable | Type | Description |
|----------|------|-------------|
| `{{.NF}}` | string | NF type identifier |
| `{{.Vendor}}` | string | Vendor name |
| `{{.Metric}}` | string | Metric name |
| `{{.Instance}}` | string | NF instance identifier |
| `{{.PLMN}}` | string | PLMN ID |
| `{{.Slice}}` | string | Slice ID (SST:SD) |

Templates are matched segment-by-segment against incoming metric paths. Template segments (those containing `{{`) become wildcards that capture the corresponding path segment value. Literal segments must match exactly.

Example:

```yaml
source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts"
```

Matches: `/pm/stats/AMF/reg/amf-001/attempts`
Captures: `NF=AMF`, `Instance=amf-001`

Does not match: `/pm/stats/AMF/session/amf-001/attempts` (literal `reg` != `session`)

### Transform Options

| Transform | Syntax | Description |
|-----------|--------|-------------|
| Identity | `""` or `"identity"` | Return latest value unchanged |
| Rate | `"rate(30s)"` | Delta of last two values divided by window in seconds |
| Delta | `"delta"` | Difference between last two values |
| Ratio | `"ratio(0,1)"` | `values[0] / values[1]` (integer index into sample buffer) |

Rate example: two samples `[100 @ T=0, 130 @ T=30]` with `rate(30s)` yields `(130-100)/30 = 1.0`.

Delta example: two samples `[100, 130]` with `delta` yields `30`.

Ratio handles division-by-zero by returning `0`.

### LabelExtract Rules

`label_extract` extracts label values from path segments of the incoming metric name. Paths are split by `/` for gNMI-style paths, or `_` for Prometheus-style metric names.

| Field | Type | Description |
|-------|------|-------------|
| `segment` | int | 0-indexed path segment to extract |
| `label` | string | Label name to assign the extracted value |

Example:

```yaml
source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts"
label_extract:
  - segment: 4
    label: instance_id
```

For path `/pm/stats/AMF/reg/amf-001/attempts`, segments are `[pm, stats, AMF, reg, amf-001, attempts]`, so `segment: 4` extracts `amf-001` as `instance_id`.

Out-of-bounds segment indices are silently skipped.

### Full DSL Example: Nokia ENM

From `schema/v1/amf.yaml`:

```yaml
nokia_enm:
  source_protocol: prometheus
  metrics:
    registration.attempt_count:
      source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts"
      transform: "rate(30s)"
      type: counter
      reset_aware: true
      label_match_strategy: exact
      label_extract:
        - segment: 4
          label: instance_id
    registration.failure_count:
      source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/failures"
      transform: "rate(30s)"
      type: counter
      reset_aware: true
      label_match_strategy: exact
      label_extract:
        - segment: 4
          label: instance_id
    ue.connected_count:
      source_template: "/pm/stats/{{.NF}}/ue/{{.Instance}}/connected"
      type: gauge
      reset_aware: false
      label_match_strategy: exact
      label_extract:
        - segment: 4
          label: instance_id
```

This mapping demonstrates:
- Template variables (`{{.NF}}`, `{{.Instance}}`) for hierarchical path matching
- `rate(30s)` transform for counter-type KPIs
- Label extraction from path segments
- No `prometheus_metric` field -- `source_template` replaces it entirely

## Testing Your Connector with argus-certify

The certification flow exercises the real normalizer engine with the real schema registry. This means your mapping YAML is loaded and executed exactly as it would be in production.

Common issues caught by certification:

| Symptom | Cause |
|---------|-------|
| `no matching series for metric` | Wrong `prometheus_metric` name or missing labels |
| Derived KPI fails with `dependency not resolved` | Base KPI mapping is missing or wrong type |
| `RegistrationStorm` never fires | Counter delta too low -- check `rate_per_second` and `rate_scale` in scenario |
| False positive on `steady_state` | Gauge jitter too high, crossing correlation threshold |
| `no mapping for vendor` | Vendor name in scenario doesn't match `mappings` key in schema YAML |

Run with `--verbose` to see per-field assertion results and diagnose which specific check failed.

## PR Checklist

When submitting a vendor connector to the Argus community:

- [ ] **Schema mappings** added to appropriate `schema/v1/<nf_type>.yaml` files
- [ ] **Comment gaps**: document which KPIs the vendor doesn't expose, with version context (e.g. `# free5gc v4.2.0 has no pdu_session_events_total counter`)
- [ ] **Two certification scenarios** in `test/scenarios/matrix/`:
  - `<vendor>_steady_state.yaml` with `expected_events: []`
  - `<vendor>_alarm_storm.yaml` with at least one expected correlation event
- [ ] **`argus-certify matrix` passes** with the new scenarios included
- [ ] **No regressions**: existing vendor scenarios still pass
- [ ] **Connector README** in `docs/vendor-connectors/<vendor>/README.md` documenting:
  - Vendor NF versions tested
  - Which KPIs are mapped vs. gaps
  - Any vendor-specific quirks (counter reset behavior, label cardinality)
  - DSL usage if applicable
- [ ] **Label cardinality bounded**: no unbounded label values (e.g. don't extract UE IMSI as a label)
- [ ] **counter vs. gauge types correct**: wrong type silently produces garbage deltas
