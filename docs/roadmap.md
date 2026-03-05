# Argus Roadmap

This roadmap reflects current intent. Priorities shift based on community feedback
and operator deployments.

## Shipped

- **v0.1**: free5GC + Open5GS collectors, simulator (Prometheus + gNMI), Prometheus output, Grafana dashboard
- **v0.2**: OAI RAN collector, pipeline backend, OpenTelemetry export, cross-NF correlation
- **v0.3**: CI pipeline, argus-certify CLI, vendor DSL, certification matrix
- **v0.4**: Redis counter store, multi-worker normalization, LabelExtract wiring, operator docs, Grafana dashboard in Helm

## v0.5 (current)

CNCF Sandbox readiness release.

- CNCF governance files (GOVERNANCE.md, CONTRIBUTING.md, SECURITY.md, CODE_OF_CONDUCT.md)
- kind-based E2E integration test in GitHub Actions
- Redis as default Helm store (bbolt deprecated, retained for single-instance dev)
- Ericsson ENM vendor connector (second production vendor alongside Nokia ENM)
- Vendor scaffolding CLI (`argus-certify add-vendor`)

## v0.6 (planned)

Crash recovery and edge deployment.

- WAL-backed counter store: write-ahead log in front of Redis for crash recovery without data loss
- bbolt-to-Redis live migration tool
- Edge deployment patterns: single-node Argus with local WAL, no Redis dependency
- gNMI bidirectional streaming: subscribe to NF telemetry changes instead of polling

## v0.7 (planned)

Standards contribution.

- OTel 5G semantic conventions: contribute argus-schema KPI definitions to the OpenTelemetry semantic conventions project as 5G domain conventions
- argus-schema as standalone artifact: split the schema registry into its own module for independent versioning and community contribution
- 3GPP R18 KPI coverage: add remaining TS 28.552 counters for AMF, SMF, UPF

## Beyond

- GSMA Open Gateway reference architecture submission
- NVIDIA Nemotron integration guide for AI-driven anomaly detection on Argus telemetry
- Multi-cluster federation: aggregate normalized KPIs across geographically distributed cores
- eBPF-based collector for kernel-level UPF metrics (bypass Prometheus scrape)
