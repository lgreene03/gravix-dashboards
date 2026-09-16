# Subsystems, criticality, and who can review them

Which parts of Gravix would hurt if they broke, who is accountable for each, and — honestly — how
many people could actually review a change to one.

**The current answer is one person, everywhere.** That is a real risk, it is recorded rather than
softened, and [`MAINTAINERS.md`](../../MAINTAINERS.md) says the same thing.

## What makes a subsystem critical

A subsystem is **critical** when a defect in it, shipped unnoticed, causes one of exactly four
things:

1. **Data loss** — a fact accepted and then lost.
2. **A wrong number** — a metric that misreports and is believed.
3. **A security breach** — unauthorised access to data or credentials.
4. **An unrecoverable upgrade** — a user unable to roll back.

Anything else is important but not critical. That distinction is the whole value of the list:
declaring everything critical means nothing is prioritised, and a register where every row is red is
a register nobody reads.

`pkg/busfactor` refuses a row marked critical for any reason outside those four, so the test cannot
be widened by whoever is editing the table.

## The register

| Subsystem | Critical | Failure mode |
|---|---|---|
| `/schemas/` | ✅ | wrong number — an invalid fact accepted is a number nobody can trust |
| `/services/ingestion/` | ✅ | data loss — the one path whose job is not losing facts |
| `/transforms/` | ✅ | wrong number |
| `/pkg/recompute/` | ✅ | wrong number — recomputation is the correctness claim |
| `/pkg/sketch/` | ✅ | wrong number — a percentile that is quietly wrong |
| `/pkg/manifest/` | ✅ | wrong number — lineage decides which numbers are current |
| `/pkg/storage/` | ✅ | data loss |
| `/services/gateway/` | ✅ | security breach — it holds the auth paths |
| `/pkg/gatewaycore/` | ✅ | security breach — the gateway's implementation lives here |
| `/cube/` | ✅ | wrong number — the semantic layer defines what a metric means |
| `/deploy/` | ✅ | unrecoverable upgrade — migrations run from here |
| `/ee/` | ✅ | security breach — multi-tenant isolation |
| `/dashboards/` | — | important, not critical: it renders what it is given |
| `/cmd/cli/` | — | a defect is visible immediately to the person running it |
| `/sdk/` | — | same |
| `/docs/oss/` | — | important, not critical |

`/ee/` is on this list on the same terms as everything else. Its single owner is a **recorded gap,
not an exemption** — a paid tier is not a reason to audit something less.

## Declared and effective

```bash
./scripts/bus_factor.sh
```

- **Declared** is how many owners `.github/CODEOWNERS` lists.
- **Effective** is how many of them have actually worked in that subsystem in the last **180 days**.

Only the second number means anything. A declared owner who has not touched a subsystem in six
months is not a bus factor of one more; they are a name in a file, and a file that makes the risk
look smaller than it is, is worse than an honest one.

Activity is measured from **commits**, not reviews. Review history needs the GitHub API, a token and
a rate limit — and F-049 is what happens when a check quietly depends on one: it reported success
because it could not ask. Commits are local and always readable in a real checkout. The audit prints
which question it answered, and says so plainly when there was no history to read at all.

## What fails, and what does not

| | |
|---|---|
| An unowned path | **fails** |
| A team alias resolving to one person | **fails** — it makes a bus factor of one look like two |
| A critical subsystem below effective 2, **not** recorded in `MAINTAINERS.md` | **fails** |
| A critical subsystem below effective 2, **recorded** | passes, with a warning |
| A declared owner who is not effective | warning, and named |

A recorded gap passes on purpose. This project has one maintainer; an audit that went red every
month for a fact nobody can change this quarter is an audit people stop reading, and then the
failures that *are* actionable stop being seen too. That is the same reasoning
`scripts/audit_access.sh` applies to the same fact.

## Adding an owner

Not by editing `CODEOWNERS`. The ladder in [`contribution-ladder.md`](contribution-ladder.md) is
the route, `GRVX-1210`'s onboarding checklist is the procedure, and a name added here without
either is a rubber stamp that makes this page lie.

A listed owner must be able to genuinely review that subsystem. Today there is one owner, who wrote
all of it, so the confirmation is trivially true — the mechanism exists for when it is not.
