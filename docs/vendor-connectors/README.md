# Vendor Connectors

This directory contains documentation for community-contributed vendor connectors. Each subdirectory corresponds to one vendor and documents the mapping implementation, tested NF versions, KPI coverage, and vendor-specific quirks.

## Directory Structure

```
docs/vendor-connectors/
  README.md              # this file
  nokia-enm/
    README.md            # Nokia ENM connector documentation (DSL reference pattern)
  <vendor>/
    README.md            # connector documentation
```

## Contributing a Connector

To add a new vendor connector:

1. **Add mappings** to the appropriate `schema/v1/<nf_type>.yaml` files. No Go code required -- the entire connector is YAML.
2. **Write certification scenarios** in `test/scenarios/matrix/` (at minimum: `<vendor>_steady_state.yaml` and `<vendor>_alarm_storm.yaml`).
3. **Run `argus-certify matrix`** and confirm all assertions pass with no regressions.
4. **Create a directory** here at `docs/vendor-connectors/<vendor>/` with a `README.md` documenting:
   - Vendor NF software versions tested against
   - KPI coverage table (mapped vs. gaps per NF type)
   - Counter reset behavior (e.g. Nokia midnight UTC reset, Ericsson PM file boundaries)
   - Label cardinality notes
   - DSL usage if the vendor requires `source_template` / `label_extract`
5. **Submit a PR** following the checklist in the [Vendor Connectors Guide](../operator-guide/vendor-connectors.md#pr-checklist).

## Existing Connectors

| Vendor | Protocol | NF Types | Location | Status |
|--------|----------|----------|----------|--------|
| free5GC | Prometheus | AMF, SMF, UPF | `schema/v1/*.yaml` (inline) | Production |
| Open5GS | Prometheus | AMF, SMF | `schema/v1/*.yaml` (inline) | Production |
| OAI (gNB) | gNMI | gNB | `schema/v1/gnb.yaml` (inline) | Production |
| Nokia ENM | Prometheus (DSL) | AMF | `schema/v1/amf.yaml` (inline) | Simulation only |

The Nokia ENM connector is a simulation-only reference pattern demonstrating the DSL features (`source_template`, `transform`, `label_extract`). It does not connect to a real Nokia system. See [nokia-enm/README.md](nokia-enm/README.md).

## Schema Files vs. Connector Docs

The YAML mappings live in `schema/v1/` (the runtime schema registry). Documentation lives here in `docs/vendor-connectors/`. Both must be updated together -- the PR checklist enforces this.
