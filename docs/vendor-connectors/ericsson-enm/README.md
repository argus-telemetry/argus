# Ericsson ENM Vendor Connector

Argus maps Ericsson ENM (Ericsson Network Manager) PM counters to 3GPP TS 28.552
canonical KPIs via the source_template DSL and LabelExtract rules.

## PM Counter Path Format

Ericsson ENM exposes PM data through a YANG-based model:

```
/ericsson-pm:pm/<job-name>/<measurement-reader>/<measurement-type>/<measObjLdn>
```

| Segment | Index | Description |
|---------|-------|-------------|
| `ericsson-pm:pm` | 0 | YANG module root |
| `<job-name>` | 1 | PM job name (e.g., `pm-job-1`) |
| `<measurement-reader>` | 2 | Reader identifier (e.g., `reader-1`) |
| `<measurement-type>` | 3 | 3GPP counter name (e.g., `pmNrRegInitAttSum`) |
| `<measObjLdn>` | 4 | Managed Object LDN — NF instance identifier |

## Mapped Counters by NF Type

### AMF

| Ericsson Counter | Argus KPI | Type |
|-----------------|-----------|------|
| `pmNrRegInitAttSum` | `registration.attempt_count` | counter |
| `pmNrRegInitFailSum` | `registration.failure_count` | counter |
| `pmNrRrcConnectedUeSum` | `ue.connected_count` | gauge |

### SMF

| Ericsson Counter | Argus KPI | Type |
|-----------------|-----------|------|
| `pmPdnConnectionReq` | `session.establishment_attempt_count` | counter |
| `pmPdnConnectionFailSum` | `session.establishment_failure_count` | counter |
| `pmPdnConnectionActNbr` | `session.active_count` | gauge |

### UPF

| Ericsson Counter | Argus KPI | Type |
|-----------------|-----------|------|
| `pmEpDataPktUlSum` | `throughput.uplink_bps` | gauge |
| `pmEpDataPktDlSum` | `throughput.downlink_bps` | gauge |
| `pmEpDataPktSentSum` | `packet.sent_count` | counter |
| `pmEpDataPktRecvSum` | `packet.received_count` | counter |

### gNB

| Ericsson Counter | Argus KPI | Type |
|-----------------|-----------|------|
| `pmMacRBSymUsedPdschTypeA` | `prb.utilization_ratio` | gauge |
| `pmRadioTxRankDistrDl` | `throughput.downlink_bps` | gauge |
| `pmRadioTxRankDistrUl` | `throughput.uplink_bps` | gauge |
| `pmRrcConnectedUeSum` | `rrc.connected_ue_count` | gauge |

### Slice

| Ericsson Counter | Argus KPI | Type |
|-----------------|-----------|------|
| `pmSliceLatencyCurrent` | `latency.current_ms` | gauge |
| `pmSliceThroughputCurrent` | `throughput.current_bps` | gauge |
| `pmSliceUeActive` | `ue.active_count` | gauge |

## Counter Reference

Counter names verified against Ericsson ENM R17+ Counter Reference Guide (CRG).
Earlier releases may use different counter names — check your CRG version.

| Ericsson PM Counter | CRG Section | Description | Argus KPI |
|---|---|---|---|
| `pmNrRegInitAttSum` | AMF PM Counters | Total initial registration attempts | `amf.registration.attempt_count` |
| `pmNrRegInitFailSum` | AMF PM Counters | Total initial registration failures | `amf.registration.failure_count` |
| `pmNrRrcConnectedUeSum` | AMF PM Counters | RRC-connected UE count | `amf.ue.connected_count` |
| `pmPdnConnectionReq` | SMF PM Counters | PDN connection establishment requests | `smf.session.establishment_attempt_count` |
| `pmPdnConnectionFailSum` | SMF PM Counters | PDN connection establishment failures | `smf.session.establishment_failure_count` |
| `pmPdnConnectionActNbr` | SMF PM Counters | Active PDN connections | `smf.session.active_count` |
| `pmEpDataPktUlSum` | UPF PM Counters | Uplink data packets (bytes) | `upf.throughput.uplink_bps` |
| `pmEpDataPktDlSum` | UPF PM Counters | Downlink data packets (bytes) | `upf.throughput.downlink_bps` |
| `pmEpDataPktSentSum` | UPF PM Counters | Total packets sent (N6 egress) | `upf.packet.sent_count` |
| `pmEpDataPktRecvSum` | UPF PM Counters | Total packets received (N3 ingress) | `upf.packet.received_count` |
| `pmMacRBSymUsedPdschTypeA` | gNB DU PM Counters | PRB utilization (PDSCH Type A) | `gnb.prb.utilization_ratio` |
| `pmRadioTxRankDistrDl` | gNB DU PM Counters | Downlink TX rank distribution | `gnb.throughput.downlink_bps` |
| `pmRadioTxRankDistrUl` | gNB DU PM Counters | Uplink TX rank distribution | `gnb.throughput.uplink_bps` |
| `pmRrcConnectedUeSum` | gNB DU PM Counters | RRC-connected UEs per cell | `gnb.rrc.connected_ue_count` |
| `pmSliceLatencyCurrent` | Slice PM Counters | Current slice E2E latency | `slice.latency.current_ms` |
| `pmSliceThroughputCurrent` | Slice PM Counters | Current slice throughput | `slice.throughput.current_bps` |
| `pmSliceUeActive` | Slice PM Counters | Active UEs per slice | `slice.ue.active_count` |

## LabelExtract

All Ericsson ENM mappings extract `instance_id` from path segment 4 (the measObjLdn).
This identifies the NF instance within the ENM-managed network.

## Verification

```bash
# Run the Ericsson AMF alarm storm scenario
argus-certify run --scenario test/scenarios/matrix/ericsson_amf_alarm_storm.yaml

# Run the full matrix (includes Ericsson)
argus-certify matrix --matrix-dir test/scenarios/matrix/
```

## Known Limitations

- **Measurement job configuration required:** ENM must have a PM job configured that
  exports the counters listed above. Job names vary by deployment.
- **Result paths vary by ENM version:** The YANG path structure is stable across
  ENM 23.x+, but earlier versions may use different module prefixes.
- **Derived counters:** Some 3GPP KPIs (handover success rate, packet loss rate) require
  base counters that may not be exposed in all ENM configurations. Argus computes
  derived KPIs only when all dependencies are present.
- **This connector is simulation-validated, not production-validated.** If you are
  running Ericsson ENM in production and can validate these mappings, please open a PR
  to mark this connector as production-validated and add your organization to ADOPTERS.md.
