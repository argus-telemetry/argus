# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| v0.5.x | Yes |
| < v0.5 | No |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Email **argus.telemetry@gmail.com** with subject line:
`SECURITY: [brief description]`

Include:
- Description of the vulnerability
- Steps to reproduce
- Affected version(s)
- Impact assessment if known

You may also use GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability) feature on this repository.

## Response SLA

| Severity | Acknowledgment | Patch Target |
|----------|---------------|-------------|
| Critical | 72 hours | 30 days |
| Moderate | 72 hours | 90 days |
| Low | 5 business days | Next release |

## Disclosure Policy

We follow coordinated disclosure with a 90-day embargo:

1. Reporter submits vulnerability privately
2. Maintainers acknowledge within 72 hours
3. Maintainers develop and test a fix
4. Fix is released and CVE assigned (if applicable)
5. Public disclosure after 90 days or when fix ships, whichever comes first

## Scope

**In scope:**
- Argus binary (`cmd/argus`)
- argus-certify CLI (`cmd/certify`)
- Schema registry (`schema/v1/`)
- Helm chart (`deploy/helm/argus`)
- Docker images published by the project

**Out of scope:**
- Vendor NF systems that Argus connects to (report to vendor)
- Third-party dependencies (report upstream; notify us if it affects Argus)
- Prometheus, Grafana, or Redis deployments (report to those projects)
