# Contributing to Argus

Contributions are welcome from anyone. This guide covers the process for submitting
changes, development setup, and project conventions.

## Developer Certificate of Origin (DCO)

All commits must be signed off to certify you have the right to submit them under
the project's open source license. This is the standard DCO requirement.

```bash
git commit -s -m "feat: add Ericsson ENM connector"
```

This adds a `Signed-off-by: Your Name <email@example.com>` trailer. Unsigned
commits will be rejected by CI.

## PR Process

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with DCO sign-off on every commit
4. Push to your fork and open a PR against `main`
5. CI must pass (lint, test, integration, helm-lint, certify, matrix)
6. One Reviewer approval required
7. A Maintainer merges after approval

## Commit Message Format

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add Huawei U2020 vendor connector
fix: handle nil pointer in gNMI parser on empty path
docs: update quickstart for Redis default
test: add alarm storm scenario for Ericsson ENM
chore: update go-redis to v9.19
refactor: extract counter delta computation into helper
```

Scope is optional but encouraged for targeted changes:

```
feat(schema): add slice SLA KPIs for 3GPP R18
fix(normalizer): prevent panic on empty LabelExtract segment
```

## Development Setup

### Prerequisites

- Go 1.25+ (match `go.mod`)
- Docker and Docker Compose (for quickstart and integration tests)
- Helm 3.x (for chart linting)
- golangci-lint (for `make lint`)

### Clone and Build

```bash
git clone https://github.com/argus-telemetry/argus.git
cd argus
make build
```

### Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build all binaries (argus, argus-sim, argus-certify) |
| `make test` | Run unit tests with race detector |
| `make integration` | Run integration tests (`-tags integration`) |
| `make certify` | Run argus-certify against all scenarios |
| `make lint` | Run golangci-lint |
| `make helm-lint` | Run `helm lint --strict` on the Helm chart |
| `make matrix` | Run argus-certify multi-vendor scenario matrix |
| `make check` | Run vet + lint + test |
| `make clean` | Remove build artifacts |

### Running Tests

```bash
# Unit tests
make test

# Integration tests (no external dependencies required)
make integration

# Full CI gate (what GitHub Actions runs)
make check && make helm-lint && make matrix
```

## How to Add a Vendor Connector

The fastest way to contribute is adding support for your vendor's 5G core.

1. Scaffold placeholder mappings and a certification scenario:

   ```bash
   # All NFs (amf, smf, upf, gnb, slice):
   make add-vendor VENDOR=acme_5g

   # Specific NFs only:
   ./bin/argus-certify add-vendor --vendor acme_5g --nfs amf,smf
   ```

   This appends vendor stubs to each schema YAML without altering existing content,
   and generates a skeleton alarm storm scenario in `test/scenarios/matrix/`.

2. Fill in `source_template` values from your vendor's PM documentation — replace
   every `REPLACE_WITH_COUNTER_NAME` with the actual counter identifier
3. Add a certification scenario to `test/scenarios/matrix/`
4. Verify with `argus-certify run --scenario <your-scenario>`
5. Add documentation to `docs/vendor-connectors/<your-vendor>/`
6. Submit a PR

See [Vendor Connectors Guide](docs/operator-guide/vendor-connectors.md) for
the full walkthrough, including LabelExtract rules and DSL transforms.

## How to Add a Schema KPI

KPI schemas live in `schema/v1/` with one file per NF type:

- `amf.yaml` — Access and Mobility Management
- `smf.yaml` — Session Management
- `upf.yaml` — User Plane Function
- `gnb.yaml` — gNodeB / RAN
- `slice.yaml` — Network Slicing

Each KPI needs:
- A canonical name following the `{nf}.{domain}.{metric}` convention
- A unit (`counter`, `gauge`, `ratio`)
- A 3GPP TS 28.552 spec reference
- Vendor mappings for at least one vendor

## Issue Labels

| Label | Description |
|-------|-------------|
| `good-first-issue` | Suitable for first-time contributors |
| `help-wanted` | Maintainers welcome external contributions |
| `vendor-connector` | Adding or improving a vendor connector |
| `schema` | Schema registry changes |
| `bug` | Something is broken |
| `enhancement` | New feature or improvement |

## First-Time Contributors

Start with issues labeled `good-first-issue`. The most impactful first contribution
is adding a vendor connector for your deployment — it requires no Go code changes,
only YAML schema mappings and a certification scenario.

## Code Standards

- Every new `.go` file gets a corresponding `_test.go`
- Zero TODO/FIXME markers in merged code — file an issue instead
- `go vet`, `golangci-lint`, and `go test -race` must pass
- `CGO_ENABLED=0 go build ./...` must succeed (multi-arch compatibility)
- `helm lint --strict deploy/helm/argus` must pass

## License

By contributing, you agree that your contributions will be licensed under the
Apache 2.0 License.
