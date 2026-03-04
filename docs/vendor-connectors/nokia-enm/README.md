# Nokia ENM Connector (Simulation Reference)

This connector demonstrates the Argus DSL features for vendors that expose hierarchical, path-based PM counter names. It maps simulated Nokia ENM-style metrics to 3GPP TS 28.552 AMF KPIs using `source_template`, `transform`, and `label_extract`.

**This is not a production connector.** It does not connect to a real Nokia ENM system. It exists as a reference pattern for contributors adding vendors with similar PM counter structures (Ericsson PM, Huawei iManager, ZTE NetNumen).

## Mapping Location

`schema/v1/amf.yaml` under `mappings.nokia_enm`.

## KPI Coverage

| KPI | Mapped | Metric Path | Notes |
|-----|--------|-------------|-------|
| `registration.attempt_count` | Yes | `/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts` | rate(30s) transform |
| `registration.failure_count` | Yes | `/pm/stats/{{.NF}}/reg/{{.Instance}}/failures` | rate(30s) transform |
| `registration.success_rate` | Yes (derived) | Computed from attempt_count and failure_count | Formula evaluation |
| `deregistration.count` | No | -- | Not modeled in simulation |
| `handover.attempt_count` | No | -- | Not modeled in simulation |
| `handover.success_count` | No | -- | Not modeled in simulation |
| `handover.success_rate` | No (derived) | -- | Depends on unmapped handover KPIs |
| `ue.connected_count` | Yes | `/pm/stats/{{.NF}}/ue/{{.Instance}}/connected` | Gauge, no transform |

## DSL Features Demonstrated

### source_template

Nokia ENM PM counters use hierarchical paths like `/pm/stats/<NF>/<category>/<instance>/<counter>`. The `source_template` field uses Go `text/template` syntax to match these paths with variable capture:

```yaml
source_template: "/pm/stats/{{.NF}}/reg/{{.Instance}}/attempts"
```

This matches `/pm/stats/AMF/reg/amf-001/attempts` and captures `NF=AMF`, `Instance=amf-001`.

### transform

Nokia PM counters are cumulative. The `rate(30s)` transform computes per-second rates from counter deltas:

```yaml
transform: "rate(30s)"
```

Given two consecutive samples `[prev, current]`, the output is `(current - prev) / 30`.

For the `ue.connected_count` gauge, no transform is applied (implicit `identity`).

### label_extract

The `label_extract` rules extract label values from specific path segments:

```yaml
label_extract:
  - segment: 4
    label: instance_id
```

For path `/pm/stats/AMF/reg/amf-001/attempts`, the segments are:

| Index | Segment |
|-------|---------|
| 0 | `pm` |
| 1 | `stats` |
| 2 | `AMF` |
| 3 | `reg` |
| 4 | `amf-001` |
| 5 | `attempts` |

Segment 4 extracts `amf-001` as the `instance_id` label on the normalized KPI.

## Vendor-Specific Notes

Real Nokia ENM deployments have characteristics this simulation does not model:

- **Midnight UTC counter reset**: Nokia PM counters reset at 00:00 UTC daily. The `reset_aware: true` flag handles this, but the simulation doesn't generate midnight resets.
- **15-minute PM file boundaries**: Nokia aggregates counters in 15-minute XML PM files. Argus scrapes Prometheus endpoints, so the integration assumes a Nokia-to-Prometheus adapter (e.g. pm-exporter) that exposes live counters.
- **ManagedElement hierarchy**: Real Nokia paths include `ManagedElement` and `GNBDUFunction` identifiers. The simulation uses simplified paths.
- **SNSSAI-scoped counters**: Nokia splits some counters by network slice. The simulation does not model per-slice metrics.

## Extending to Production

To build a production Nokia ENM connector:

1. Deploy a PM file exporter that converts Nokia XML PM files to Prometheus metrics with path-based names matching the `source_template` patterns.
2. Extend the mappings to cover all AMF KPIs, plus SMF, UPF, and gNB NF types in their respective schema files.
3. Add `label_extract` rules for ManagedElement and SNSSAI segments.
4. Write certification scenarios with realistic counter baselines and midnight reset events.
5. Test `reset_aware` behavior across simulated midnight boundaries.

## Related Documentation

- [Vendor Connectors Guide](../../operator-guide/vendor-connectors.md) -- full DSL reference and step-by-step connector authoring
- [Certification Guide](../../operator-guide/certification.md) -- how to validate connectors with argus-certify
- [Schema Registry](../../../schema/v1/amf.yaml) -- the actual mapping YAML
