# Governance

How decisions get made in Gravix, who can block what, and what is not up for a vote.

## Decision-making

Three tiers. The tier is set by what the change touches, not by how large the diff is.

| Tier | What it covers | Requires |
|---|---|---|
| **Routine** | Bug fix, docs, tests, implementing an approved spec | One maintainer approval |
| **Design** | New public API, schema change, new dependency, new extension point | An RFC in `docs/oss/rfcs/`, 7 days' comment, two maintainer approvals |
| **Charter** | Anything altering `docs/oss/00-open-core-charter.md` | The charter's own §6 procedure: an RFC, **14 days**' public comment, approval from the CPO **and** the License & Boundary Auditor, and a changelog entry |

Design and charter tiers both go through the RFC process in
[`docs/oss/rfcs/`](docs/oss/rfcs/): a written proposal with eight required sections, a comment window
whose length CI checks against the tier, and named approvals. Every RFC ever opened is in the
[decision log](docs/oss/rfcs/index.md), including the rejected and the withdrawn — a log that
records only what was accepted tells you nothing about what this project refuses.

Routine changes need no RFC. A process applied to a typo fix is a process people route around.

Charter §7.1 (the core is Apache-2.0), §7.3 Q4 (once open, always open) and §7.4 (no dark patterns)
are **entrenched**. They may be strengthened. They may never be weakened — not by majority, not by
maintainer decision, not by a review that finds them inconvenient.

## Who can block, and cannot be overruled in a sprint

Three roles hold a veto:

- **License & Boundary Auditor** — on placing anything in `ee/`, and on any release where
  `make build-oss` fails.
- **Security** — on releasing a known-exploitable vulnerability.
- **QA** — on shipping with an unproven acceptance criterion.

Overruling any of these requires the public amendment procedure in charter §6. Not a discussion,
not a deadline, not a customer commitment. The point of a veto that cannot be overruled quickly is
that it still works on the day it is expensive.

## Becoming a maintainer

Three levels: **contributor → reviewer → maintainer**. Promotion criteria are mechanical and
published — there is no discretionary path, because discretion is how a ladder becomes a clique.

The full criteria, the nomination process, and what happens on inactivity are in
[`docs/oss/contribution-ladder.md`](docs/oss/contribution-ladder.md): sustained, reviewed
contributions; demonstrated review judgement; and, for maintainer, a demonstrated willingness to
decline something on principle in public. Every criterion is evidenced by something public, and a
nomination cites the links.

Granting and revoking the access itself has its own checklists:
[`docs/oss/maintainer-onboarding.md`](docs/oss/maintainer-onboarding.md) and
[`docs/oss/maintainer-offboarding.md`](docs/oss/maintainer-offboarding.md). Merge access and custody
— organisation ownership, billing, DNS, publishing credentials, `ee/` signing keys — are held
separately on purpose, so that a compromised maintainer account cannot take the project's identity.
`scripts/audit_access.sh` runs weekly and flags any drift between who holds access and who is
recorded as holding it.

## Losing maintainer status

- Voluntarily, at any time, with no explanation owed.
- After six months of inactivity, moving to **Emeritus**: recognition retained, access removed.
  This is not a punishment. Access nobody is using is a security liability, and it is restored on
  request with no re-qualification.
- For cause, requiring the agreement of all remaining maintainers and a written, public reason.

## What is not up for a vote

The seven non-goals in [`docs/04-non-goals.md`](docs/04-non-goals.md), and the entrenched charter
clauses above.

> Popularity does not amend the constitution. A well-argued, widely-supported request for
> distributed tracing is still declined.

We will say so kindly, cite the section, and name the tool that does do it. What we will not do is
leave the request open to rot, which implies we might.

## Trademark

The Gravix name and logo are reserved. Forks are welcome and may not use the name. See
[`TRADEMARK.md`](TRADEMARK.md).

## Succession

Where the project's identity lives — the organisation, domains, registries, signing identity — and
who can recover each is recorded in `docs/oss/succession.md` (`GRVX-1503`).

## Transparency

Every governance decision is recorded in `docs/oss/rfcs/` or in the changelog. No decision affecting
the licence, the open-core boundary, or a non-goal happens in private.

When a spec turns out to be wrong, that is recorded too, in
[`docs/oss/spec-defects.md`](docs/oss/spec-defects.md), with what was assumed and what was actually
true. A project with no recorded mistakes is not a careful one; it is one that is not writing them
down.
