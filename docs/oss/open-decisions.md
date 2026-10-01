<!-- A navigation aid, not a register. The registers are findings.md, spec-defects.md and -->
<!-- correctness-defects.md, all append-only. This file points into them and may be rewritten. -->
# What needs a person

Three registers hold 76 entries between them, most of them resolved. Most are ordinary work an
implementer can pick up. This page lists only the ones that **cannot be closed by implementing
harder**, because they need a decision, a permission, or an external check.

**Decisions made under the owner's delegation of 2026-10-01 are logged in
[`delegated-decisions.md`](delegated-decisions.md)**, each with the options, the choice, and how to
reverse it. Rows below are removed as they are decided there.

Regenerate the underlying counts with:

```bash
grep -c '^## F-'  docs/oss/findings.md
grep -c '^## SD-' docs/oss/spec-defects.md
grep -c '^## CD-' docs/oss/correctness-defects.md
```

---

## Decisions

| Item | What must be decided | Why it is not an implementation detail |
|---|---|---|
| **SD-040** *(high)* | Whether `pkg/extpoint` gains a tenant-resolution extension point (RFC 0002), or `GRVX-1304` is rewritten to run multi-tenancy as a separate `ee/` process in front of the gateway | `GRVX-1304` §6 step 1 requires an extension point `GRVX-1302` §3 explicitly forbids, and an `Extension` cannot do it: it only sees requests under its own path prefix, while tenant resolution has to reach ingestion, the metrics API, the percentile endpoint and export — four core routes. `GOVERNANCE.md` puts a new extension point at design tier: an RFC, seven days' comment and **two maintainer approvals**, of which the project has one. Blocks `GRVX-1304`, `1305`, `1306`, `1308`, `1310`, `1312` and RFC 0002 is drafted and open. (`GRVX-1401` was listed here in error and is not blocked — see SD-040's correction.) |
| **SD-029 / F-054** *(high)* | How an export reaches a destination the user names: GRVX-1107 §5.4's on-demand endpoint takes `destination` from any role, and scheduled exports (stored, listed, never executed — F-054) would write to a customer's `s3://` bucket | The route collision is resolved (DD-011) and four of five route criteria pass. What remains is a security design: a server-chosen destination, an allow-list, or stored per-schedule credentials. Each lets a different principal write to a different place. |
| **SD-016** *(medium)* | Whether production ingestion runs one replica (exact, deterministic path templates; no horizontal scale) or many (scale; a multiplied bound and templates that can differ between pods) | Examined under the delegation and not decided: the options trade availability, determinism and path detail, and none dominates. Needs someone who operates a production deployment. The trade-off is now written beside `autoscaling` in both production values files, and group commit has made one replica much more capable. |
| **F-026** *(high)* | When to add `docker-smoke` and `docker-build` to `ci-summary`'s `needs` | One line, and it turns every pull request red immediately — correctly, but for a failure nobody has diagnosed and whose logs have expired. Sequencing, not code. |

## Spec text that contradicts what was measured

**None outstanding.** SD-019, SD-021 and SD-022 were amended on 2026-10-01 (DD-001 to DD-003). SD-021
was more than text: the mandated "roughly 10x" sentence was removed from both cost-model
implementations, and a guard test now fails on any fixed multiple in a caveat.

## Permissions and external checks

| Item | What is needed |
|---|---|
| **DCO** | **Done.** The repository owner signed off, and all 86 commits on `claude/gravix-opensource-roadmap-c8ija8` now carry `Signed-off-by: Luke Greene <luke.greene86@gmail.com>`. The trailer was added with `git rebase <base> --exec 'git commit --amend --no-edit --trailer …'` rather than `git rebase --signoff`, because `--signoff` derives the trailer from `git config user.*` and makes that identity the **committer**, which would have stripped the verified status from every commit — trading 86 signatures for one text trailer. Retained here as the reason not to reach for `--signoff` next time. |
| **GRVX-1002 CLAIM AUDIT** | A `market-analyst` pass over `bench/cardinality/competitor_units.yaml`, reading each vendor's own pricing page and recording the date. Every entry is `verified: false` and the demo withholds the comparison until that happens. The implementer must not self-verify. |
| **GRVX-1004 prices** | The same discipline for `pkg/costmodel/prices.yaml`. Every infrastructure rate is marked `estimate`, not `list_price`, because none has been read off a vendor page and dated. |
| **F-050** *(high)* | **A name decision, then two commands.** `go.mod` declares `github.com/lgreene/gravix-dashboards` and the repository is at `lgreene03` — a different, existing GitHub account. The published `go get` command therefore cannot work, and would serve that account's code under Gravix's name if they ever created a repository of that name. Three options in `findings.md` F-050; option 2 (move to an organisation first, then set the path once) is also item 1 of `succession.md`'s list, so the two decisions are really one. Not an implementer's call: it fixes the project's canonical import path for every future consumer. |
| **Succession: a second custodian** *(high)* | **The only item on this page that needs another human being.** Every live identity asset — the GitHub account, the signing identity, the images, npm, PyPI — has exactly one custodian, and the repository sits under a **personal** account that by construction cannot have a second owner. `./scripts/verify_custody.sh` fails today and is meant to; the 2026 drill in `succession-drill.md` is recorded as not completed. Two things the owner can do alone and now: move the repository to a GitHub organisation (an afternoon, breaks no URL), and add a second owner to the npm and PyPI packages once there is somebody to add. The rest waits on a second maintainer, which is a recruitment problem. Escalated to `cpo` per GRVX-1503 §10. |
| **GRVX-1506 comment window** | **Owner's call, outward-facing.** `docs/oss/foundation-evaluation.md` reaches a `NOT YET` verdict on donating Gravix to a foundation, and charter §6 gives a charter-tier proposal 14 days of public comment. The window is recorded in the document (opened 2026-09-16, closes 2026-09-30) with where to comment, and the document is marked not final until it closes. Announcing it more widely — a discussion post, a mailing list, anywhere outside this repository — was not done, for the same reason the sixteen good-first-issues were not opened: soliciting public comment is outward-facing and notifies people. Nothing is blocked meanwhile; the evaluation, its sources and its twelve tests all stand. Two candidate foundations (the Linux Foundation directly, and The Commons Conservancy) could not be read at source from this environment and are marked UNVERIFIED; somebody with working access should read them and revise the table. |
| **GRVX-1205 issues** | **Owner's call, one command away.** The sixteen entries in `docs/oss/good-first-issue-inventory.md` are real work, audited by `scripts/gfi_audit.sh` and by CI: every file exists, every verification command is runnable. Turning them into GitHub issues was not done here, because opening sixteen issues on a public repository is outward-facing and notifies watchers — the owner decides when the project is ready to receive first-time contributors, not the implementer. Nothing is blocked meanwhile: the inventory, the audit, the weekly workflow, the template and the label promise all work against the committed file. `./scripts/gfi_audit.sh` audits the live tracker the moment issues exist. |

## Blocked on one thing working

**`timed-onboarding` is GREEN.** It returned `PASS: time to populated dashboard 466s (budget: 600s)`
on 2026-09-14, twice independently, and `ci-summary` is green with it. This section's premise no
longer holds; the items below are unblocked and need re-checking rather than waiting.

Previously — retained because the items still need doing:

**Update.** F-037 — the blocker this list was waiting on — is fixed, and it did not need the
Docker daemon or the owner decision this page previously said it did. Cube's compiler is on npm;
reading it settled the question in an hour. Cube evaluates model files in a `vm` sandbox with no
`process`, so every environment conditional in `cube/model/schema/` was dead and both stacks
compiled to the Trino SQL — including the DuckDB one. The models now take their SQL from
`cube/model_flags.js`, which Cube loads through Node's own `require`, and compiling them with the
real `prepareCompiler` confirms each stack gets the right source. Fixing it exposed F-038: the
pre-aggregation gate could never have compiled either, and no stack here runs a Cube Store to build
a rollup in, so the models now declare none.

That removes the F-037 decision from this page. It does not make the job green — that still needs a
Docker daemon, and nothing below should be assumed cleared until CI says so.

- **F-025** *(high)* — the full stack has F-021 too, and the fix is the chown init container that has
  not yet been observed working. Porting it now would be guessing twice.
- **GRVX-1003** — *no longer blocked; now `partial`.* The defect was returned before §6 step 7, which
  says what to do when the budget is missed: report the shortfall per component. `bench/storage` now
  does, and the report is decisive — raw JSONL is 220.40 bytes/event against a **total** budget of
  120, so no Parquet encoding can close it, and the "compressed at rest" premise §5.1 budgets for
  needs `services/ingestion/**`, which §4.3 forbids touching. What is left is a spec amendment. See
  SD-055.
- **GRVX-1006** — *no longer blocked; now `partial`.* SD-024 was decided on 2026-10-01 (DD-008)
  after measuring with a real Cube on the bootstrap stack's limits: no pre-aggregations, warm p95
  51–100 ms from Cube's result cache, cold p95 193–593 ms for one day and up to 3.7 s for a week.
  The cold figure is published, not hidden. Measuring also found and fixed F-053, which broke every
  date-ranged query on the DuckDB stack. The warmer, the CI query driver and the percentile
  endpoint's figure remain.

### Every spec has now been audited against its own criteria

All 88 were read individually rather than trusted by label, and the labels were wrong four times:
**GRVX-1103** was not blocked at all, **GRVX-1312**'s core half was blocked behind its own `ee/`
half, **GRVX-1004** was blocked with its entire implementation already in the tree, and **GRVX-1003**
was closed by a defect its own §6 tells you how to handle. Each is now `partial` and each found a
real defect on the way — including a Grafana plugin that could not load and a published SQL guide
wrong by 144×.

What remains blocked is blocked for a named reason that an implementer cannot clear: a second
maintainer (SD-040), an external auditor (GRVX-1407), a pricing audit the implementer is forbidden to
self-verify (GRVX-1002), a reference machine
(GRVX-1005's AC-1), or a dependency on one of those (GRVX-1007, GRVX-1008).

**An unexplained status is indistinguishable from a spec nobody looked at.** That is what hid four of
these, and it is why every entry on this page now names its cause.
- **GRVX-1007, GRVX-1008** — not startable; 1007 depends on 1002/1003/1005/1006, and 1008 on 1007.
- **GRVX-1103** — *no longer blocked.* It was marked blocked with no reason recorded here, and turned
  out to be two documentation pages and a script: AC-3 and AC-4 are proven, only AC-1 and AC-2 need
  Docker and a live Trino. Now `partial`. Its §5.1 table was wrong twice — see SD-052. The lesson is
  the one below.
- **GRVX-1407** — blocked for two independent reasons, neither of which was written down until now.
  It `Depends on GRVX-1308`, and 1308 is one of the six specs SD-040 holds, so 1407 is
  **transitively governance-blocked**: it cannot start until a second maintainer exists to approve
  RFC 0002. Separately, a SOC 2 Type 2 opinion requires an **external auditor** and an observation
  window over a **running** cloud, and §3 of the spec is explicit that this produces evidence while
  "an auditor forms the opinion". Neither is something an implementer can supply. The executable
  part — gathering the evidence — still waits on 1308.

**On "blocked" as a label.** Two specs carried it with no recorded cause. One of them, GRVX-1103, was
not actually blocked at all, and stayed untouched longer than it needed to. Every entry above now
names a decision, a defect, a dependency or a person. An unexplained `blocked` is indistinguishable
from a spec nobody looked at, and it hides work that could be done today.

Five defects were fixed to get this far — F-015, F-016, F-019, F-021, F-023 — and the gate found
three of them itself.
