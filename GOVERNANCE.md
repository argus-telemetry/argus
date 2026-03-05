# Argus Project Governance

This document defines the governance model for the Argus project. It establishes roles,
decision-making processes, and conflict resolution mechanisms that apply to all Argus
subprojects.

## Subprojects

| Subproject | Repository | Description |
|-----------|-----------|-------------|
| argus | argus-telemetry/argus | Core normalization pipeline |
| argus-certify | argus-telemetry/argus (cmd/certify) | Certification CLI |
| argus-schema | argus-telemetry/argus (schema/v1/) | 5G KPI schema registry |

argus-schema is designed to be contributed to the OpenTelemetry semantic conventions
project as 5G domain conventions. See [docs/roadmap.md](docs/roadmap.md).

## Roles

### Contributor

Anyone who has had at least one PR merged into any Argus subproject.

**Responsibilities:**
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md)
- Sign off commits with DCO (`git commit -s`)

### Reviewer

Trusted contributors who review PRs and provide technical feedback.

**Requirements:**
- 5+ PRs reviewed with substantive feedback
- Nominated by a Maintainer
- Approved by lazy consensus among Maintainers (3 business days, no objection)

**Responsibilities:**
- Review PRs within 5 business days
- Approve PRs that meet project standards
- Mentor Contributors toward Reviewer status

### Maintainer

Core contributors with merge access and project direction authority.

**Requirements:**
- Active contributor for 3+ months
- 10+ PRs merged across any subproject
- Nominated by an existing Maintainer
- Approved by 2 Maintainer votes (no vetoes within 5 business days)

**Responsibilities:**
- Merge PRs after Reviewer approval
- Triage issues and assign labels
- Participate in release planning
- Vote on breaking changes and governance updates

### Owner

Founding maintainers who set project direction and handle security/legal escalations.

**Current Owners:**
- Raghav Potluri (@rpotluri)

**Responsibilities:**
- Veto authority on security and legal issues
- Final authority on project direction disputes
- External liaison and representation
- Release approval for major versions

## Decision Making

**Day-to-day decisions:** Lazy consensus. A Maintainer or Reviewer proposes a change;
if no objection within 3 business days, the proposal is accepted.

**Breaking changes:** Require a 2/3 majority vote among Maintainers. Breaking changes
include: removing a public API, changing schema format, dropping a supported vendor,
or modifying governance.

**Security and legal issues:** Owner veto applies. Owners may override any decision
that creates a security vulnerability or legal liability.

## Conflict Resolution

1. **GitHub Discussions:** Open a Discussion in the argus-telemetry/argus repository.
   Maintainers and the community discuss the issue openly.
2. **Maintainer vote:** If consensus is not reached within 10 business days,
   Maintainers vote. Simple majority wins; ties broken by Owner.
3. **Owner escalation:** If the Maintainer vote is disputed, either party may
   escalate to the project Owner for final resolution.

## Meetings

Monthly community call. Schedule and agenda published in GitHub Discussions.
Meeting notes archived in the repository wiki.

*Meeting cadence will be established once the project has 3+ active Contributors
outside the founding organization.*

## Changes to Governance

Changes to this document require a PR approved by 2/3 of Maintainers and at least
one Owner. The PR must be open for a minimum of 7 calendar days before merging.
