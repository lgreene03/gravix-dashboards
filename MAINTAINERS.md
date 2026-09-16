# Maintainers

| Name | GitHub | Level | Subsystems | Since |
|---|---|---|---|---|
| Luke Greene | [@lgreene03](https://github.com/lgreene03) | Maintainer | all | 2026 |

Contact a maintainer through GitHub — an issue, a discussion, or a private vulnerability report.
Personal contact details are deliberately not published here.

## Bus factor

**The current bus factor is 1.**

One person can merge, release, and administer this project. If they became unavailable, nobody else
could ship a security fix.

This is a known risk, tracked as goal **G9.3** in
[`docs/oss/12-goal-tree.md`](docs/oss/12-goal-tree.md), with a target of **≥2 owners on every
critical subsystem** by Phase 15. `GRVX-1210` grants merge rights to non-founder maintainers and
`GRVX-1507` enumerates subsystems and their owners.

Stating this plainly matters more than it may appear. An adopter deciding whether to depend on
Gravix is taking on this risk whether or not we mention it, and a maintainers file that implies more
depth than exists misleads them about what they are taking on.

## Per-subsystem bus factor

Generated from [`.github/CODEOWNERS`](.github/CODEOWNERS), which is the file GitHub actually
enforces. `scripts/audit_access.sh` runs weekly and flags any path here that disagrees with it, any
account holding merge rights that is not listed above, and anyone listed above holding none.

| Subsystem | Path | Owners | Bus factor |
|---|---|---|---|
| Schema validation | `/schemas/` | @lgreene03 | 1 |
| Recompute | `/pkg/recompute/` | @lgreene03 | 1 |
| Sketches | `/pkg/sketch/` | @lgreene03 | 1 |
| Lineage | `/pkg/lineage/` | @lgreene03 | 1 |
| Metric contracts | `/contracts/` | @lgreene03 | 1 |
| Correctness suite | `/tests/correctness/` | @lgreene03 | 1 |
| Ingestion | `/services/ingestion/` | @lgreene03 | 1 |
| Transforms | `/transforms/` | @lgreene03 | 1 |
| Wire contracts | `/proto/` | @lgreene03 | 1 |
| Semantic layer | `/cube/` | @lgreene03 | 1 |
| Dashboard | `/dashboards/` | @lgreene03 | 1 |
| Gateway entrypoint | `/services/gateway/` | @lgreene03 | 1 |
| Gateway implementation | `/pkg/gatewaycore/` | @lgreene03 | 1 |
| Deployment | `/deploy/` | @lgreene03 | 1 |
| Tooling | `/scripts/` | @lgreene03 | 1 |
| CI and templates | `/.github/` | @lgreene03 | 1 |
| Governance | `/docs/oss/` | @lgreene03 | 1 |
| Commercial tier | `/ee/` | @lgreene03 | 1 |

**Every row is 1.** That is the real number, not a placeholder, and the table lists the subsystems
separately anyway — because "everything: 1" and eighteen rows of 1 look the same in a summary and
different to somebody deciding whether to depend on this. Eighteen places where one person's
absence stops the work is a more useful thing to know.

`/ee/` deliberately keeps a single owner while the paid tier is small. A second owner there is a
licence and revenue question as much as a code one, and it is recorded here rather than papered
over.

Nothing is fixed by adding a name. `GRVX-1210` §6 step 1 is explicit: granting rights to somebody
who has not met the [ladder criteria](docs/oss/contribution-ladder.md), in order to improve this
table, produces maintainers who do not maintain. The criteria are mechanical and public; when
somebody meets them, [`docs/oss/maintainer-onboarding.md`](docs/oss/maintainer-onboarding.md) is the
checklist.

## Granting and revoking

- [`docs/oss/maintainer-onboarding.md`](docs/oss/maintainer-onboarding.md) — what a maintainer receives, what they do not, and the ten-item checklist. Merge access and **custody** are deliberately separate: no maintainer holds organisation ownership, billing, DNS, publishing credentials or `ee/` signing keys, so that a compromised account cannot take the project's identity.
- [`docs/oss/maintainer-offboarding.md`](docs/oss/maintainer-offboarding.md) — revocation within 24 hours, verified by the audit rather than assumed. Read before accepting, not after.
