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

| Subsystem | Owners | Bus factor |
|---|---|---|
| Everything | @lgreene03 | 1 |

This table is expanded by `GRVX-1507` once there are enough maintainers for it to say anything
useful.
