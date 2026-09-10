# Gravix Horizon 2 — Open Core

This directory is the planning layer for Gravix's second horizon: taking a working closed SaaS and
making it an open-source project that can also sustain a business, without the second goal
corrupting the first.

**Start here:** [`00-open-core-charter.md`](00-open-core-charter.md). It is the governing document
and it amends the constitution in `docs/00-system-truth.md`.

---

## What changed, and why there is a whole directory about it

Horizon 1 (Phases 0–6, in [`../../PRODUCT_ROADMAP.md`](../../PRODUCT_ROADMAP.md)) built a complete
multi-tenant SaaS: ingestion, rollups, DuckDB/Trino, Cube, dashboard, alerting, SDKs, RBAC, SSO,
billing, Terraform, Helm. It works.

Horizon 2 adds relatively little product. It does three things Horizon 1 skipped:

1. **Makes it credible.** The repository has no `LICENSE` file, which means it is legally
   all-rights-reserved and nobody may use it. Everything else is blocked on fixing that.
2. **Makes the differentiator real.** Horizon 1 *stored* facts. Horizon 2 turns recomputability into
   a capability a user can run — the one thing competitors cannot copy without rebuilding their
   storage layer, because they discarded the raw observation.
3. **Turns a product into a project.** Contributors, an ecosystem, and a paid tier that adds
   capability without ever subtracting from the free one.

---

## The documents

| File | What it decides |
|---|---|
| [`00-open-core-charter.md`](00-open-core-charter.md) | The licence boundary. Apache-2.0 core, BUSL-1.1 `ee/`. The five-question Crippleware Test. The rule that the core must build with `ee/` deleted. |
| [`01-competitive-thesis.md`](01-competitive-thesis.md) | The five axes we claim superiority on, each with its proof artefact, its scope limit, and the strongest counter-argument. Plus what we are worse at. |
| [`10-agent-roster.md`](10-agent-roster.md) | Eighteen roles, the three that hold vetoes, the handoff matrix, the escalation ladder. |
| [`11-agent-loops.md`](11-agent-loops.md) | Loops L0–L12. Each has a trigger, an owner, exit criteria, and a durable artefact. L2 defines the Spec Readiness Gate. |
| [`12-goal-tree.md`](12-goal-tree.md) | G1–G9, each key result with a numeric target and a named measurement source. |
| [`20-roadmap-horizon-2.md`](20-roadmap-horizon-2.md) | Phases 7–15, their deliverables and exit criteria. |
| [`specs/`](specs/SPEC-INDEX.md) | The executable specifications. One spec is one work order. |

---

## The four ideas that hold this together

**1. The core must build with `ee/` deleted.**
Charter §7.1. It is checked by `make build-oss` and `make test-oss` on every pull request. Most
open-core promises are prose; this one is a CI job that fails. Every other guarantee in the charter
depends on it being mechanically true rather than merely intended.

**2. "People would pay for it" is never a sufficient reason to gate a feature.**
The Crippleware Test (§7.3) asks five questions; a single YES forces the feature into the free core.
Question 4 — *was this capability previously open?* — is absolute: once shipped open, always open.
That rule is why `GRVX-710` returns five already-shipped paid features to the free tier. It is
uncomfortable and it is the point.

**3. No claim ships before its proof exists.**
The claim register in [`01-competitive-thesis.md`](01-competitive-thesis.md) §6 tracks every public
comparative statement and the artefact that substantiates it. A claim marked `Pending proof` may not
appear in the README, the docs site, or anywhere else. Loop L11 re-verifies each one monthly against
the vendor's own primary source, and anything that has become false is corrected or removed within
seven days.

**4. Implementers do not think about product.**
A spec names every file, every signature, every error string, and every test. The implementing agent
reads exactly one spec and the files it names — not the roadmap, not this README. If the spec is
insufficient, it returns `SPEC DEFECT` rather than improvising. Requiring product context at
implementation time is how scope creep and undocumented design decisions get into a codebase.

---

## What writing this found in the existing code

Three defects, each now specced:

| Finding | Where | Fixed by |
|---|---|---|
| The rollup names its output with a fresh UUID per run, so re-running a window **duplicates** data instead of replacing it. Recomputability has been in the constitution since day one and is currently unenforceable. | `transforms/request_metrics_minute/main.go` | `GRVX-801` |
| The Cube model takes `MAX` of pre-aggregated per-minute p95s. That is not a percentile and its error is unbounded. The file's own comment admits it. It is the same flaw the thesis criticises Prometheus for. | `cube/model/schema/RequestMetricsMinute.js` | `GRVX-803`, `GRVX-804`, `GRVX-808` |
| Five capabilities behind `requirePlan` fail the Crippleware Test: public metrics API, custom dashboards, scheduled exports, per-tenant rate limiting, audit log. | `services/gateway/` | `GRVX-710` |

The second one is worth dwelling on. Gravix currently ships the exact defect its competitive
positioning is built on criticising. That is not an argument against the positioning — it is the
reason Phase 8 exists, and it is why the claim register forbids publishing Axis 2 until
`scripts/prove_it.sh` runs green in CI.

---

## Sequence

Phases 7–12 are **entirely Apache-2.0**. The first line of paid code is written in month 12, after
the free product is complete, proven, and adopted.

That ordering is the charter expressed as a schedule. You cannot degrade a free tier you have not
finished building, and you cannot sell a Pro tier to a community you do not yet have.

```
7  Open the Core          → a stranger can trust the code
8  The Correctness Moat   → a stranger can trust the numbers      ⭐ the differentiator
9  Zero-Config Value      → a stranger reaches value in 10 minutes
10 Cost Proof             → a stranger can verify the cost claim
11 Interop                → Gravix fits the stack they already have
12 Community Machine      → strangers become contributors
─────────────────────────── everything above is Apache-2.0 ───────────────────────────
13 Pro Upgrade Path       → paying adds; it never subtracts
14 Gravix Cloud           → managed, not captured
15 Sustainability         → the project outlives its founder
```
