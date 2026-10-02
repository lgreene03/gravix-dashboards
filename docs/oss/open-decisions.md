<!-- A navigation aid, not a register. The registers are findings.md, spec-defects.md and -->
<!-- correctness-defects.md, all append-only. This file points into them and may be rewritten. -->
# What needs a person

Three registers hold 134 entries between them, most of them resolved. Most are ordinary work an
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
| **SD-055 / RFC 0003** *(medium)* | Whether compaction writes raw facts as gzip JSONL, so G4.4's 120 bytes/event becomes reachable | Measured: raw JSONL is 203.78 bytes/event of a 206.68 total, and gzip takes it to 32.68. Changing the stored format of the recompute source of truth, and every reader of it, is design tier: RFC 0003 is drafted, and it needs **two maintainer approvals**, of which the project has one. Everything short of that is decided (DD-014). |
| **SD-029 / F-054** *(high)* | How an export reaches a destination the user names: GRVX-1107 §5.4's on-demand endpoint takes `destination` from any role, and scheduled exports (stored, listed, never executed — F-054) would write to a customer's `s3://` bucket | The route collision is resolved (DD-011) and four of five route criteria pass. What remains is a security design: a server-chosen destination, an allow-list, or stored per-schedule credentials. Each lets a different principal write to a different place. |
| **SD-016** *(medium)* | Whether production ingestion runs one replica (exact, deterministic path templates; no horizontal scale) or many (scale; a multiplied bound and templates that can differ between pods) | Examined under the delegation and not decided: the options trade availability, determinism and path detail, and none dominates. Needs someone who operates a production deployment. The trade-off is now written beside `autoscaling` in both production values files, and group commit has made one replica much more capable. |
| **F-026** *(high)* | **Partly done (DD-019).** `docker-build` and three jobs the summary awaited but never checked now fail `ci-summary`. `docker-smoke` passed 11 of 11 on PR #25, for the first time since at least May, after F-025, F-057 and F-058 were fixed, and now fails `ci-summary` too. `image-scan`'s action tag is repaired. What still needs a person: whether `image-scan` fails `main` on HIGH or CRITICAL findings in base images, which is the security engineer's call. |
| **F-018, across machines** *(medium)* | Whether the rollup lock should hold across machines that share an S3 bucket | Fixed for one machine (DD-022): the cron and `gravix recompute` now take the same lock, found through the store. Across machines it needs a lock object in the store, taken with a conditional write and given an expiry. That changes the storage interface and the locking semantics, so it wants a spec, not a patch. |
| **A pinned `protoc` in CI** *(low)* | Whether CI installs a fixed `protoc` so a job can fail when `gen/` drifts from `proto/` | `make proto` reproduces the tracked files (DD-027), but the generated header names the `protoc` version, so a drift check needs one version everywhere. That is a choice about CI images, owned by whoever owns CI. |
| **F-070** *(high)* | How the full stack's Trino reads a multi-tenant warehouse. **Recommended: Hive-style tenant partitions**, `warehouse/<metric>/tenant_id=<id>/event_day=<day>/`, with the Trino tables partitioned by `tenant_id` and `event_day` and refreshed with `system.sync_partition_metadata`. The alternatives: keep today's layout and register a Trino partition per tenant from a job that runs whenever a tenant is created; or run the full stack single-tenant, which undoes F-027 and F-058 | The rollup writes `warehouse/<tenant-id>/<metric>/`, which a Hive table cannot read and Trino cannot discover, and the tables have no `tenant_id` for Cube's tenant filter. So the full stack's dashboard has no data for a signed-in user. The recommended layout is one that DuckDB, Trino, Spark and a bare-Parquet reader all discover without help, which is the data-ownership claim. But it moves every object in the warehouse and changes the rollup, recompute, purge, export, compaction, Cube's globs and two published guides. A storage layout is a public data contract, so it is design tier: an RFC and **two maintainer approvals**. |
| **F-057, long term** *(medium)* | Whether the full stack keeps MinIO, now built from source (DD-030), or moves to a maintained S3-compatible server | MinIO no longer publishes community images. Building from source works and costs about two minutes on first boot. A replacement is a new dependency, which `GOVERNANCE.md` puts at design tier. |

## Spec text that contradicts what was measured

**None outstanding.** SD-019, SD-021 and SD-022 were amended on 2026-10-01 (DD-001 to DD-003). SD-021
was more than text: the mandated "roughly 10x" sentence was removed from both cost-model
implementations, and a guard test now fails on any fixed multiple in a caveat.

## Permissions and external checks

| Item | What is needed |
|---|---|
| **DCO** | **Done.** The repository owner signed off, and all 86 commits on `claude/gravix-opensource-roadmap-c8ija8` now carry `Signed-off-by: Luke Greene <luke.greene86@gmail.com>`. The trailer was added with `git rebase <base> --exec 'git commit --amend --no-edit --trailer …'` rather than `git rebase --signoff`, because `--signoff` derives the trailer from `git config user.*` and makes that identity the **committer**, which would have stripped the verified status from every commit — trading 86 signatures for one text trailer. Retained here as the reason not to reach for `--signoff` next time. |
| **F-076 advisory** *(critical)* | **The owner's call, and the first thing on this page to do.** Cube applied no tenant filter on any stack, so on an install with more than one tenant every signed-in dashboard read every tenant's data. It is fixed on `main` with tests that hold it, including a live check in the onboarding gate. 1.0.0 is affected. `SECURITY.md` promises coordinated disclosure, and the fix is public once merged, so what remains is whether and when to publish a GitHub security advisory and a patch release. Publishing is outward-facing, and the implementer did not do it. |
| **GRVX-1002 CLAIM AUDIT** | A `market-analyst` pass over `bench/cardinality/competitor_units.yaml`, reading each vendor's own pricing page and recording the date. Every entry is `verified: false` and the demo withholds the comparison until that happens. The implementer must not self-verify. |
| **GRVX-1004 prices** | The same discipline for `pkg/costmodel/prices.yaml`. Every infrastructure rate is marked `estimate`, not `list_price`, because none has been read off a vendor page and dated. |
| **F-050** *(high)* | **A name decision, then two commands.** `go.mod` declares `github.com/lgreene/gravix-dashboards`, the repository is at `lgreene03` (a different, existing GitHub account), and the SDK declares `github.com/gravix-io/gravix-go`, which nobody has confirmed the project holds. The published pages now install through `replace` directives that never fetch either path (DD-013), so nothing printed today can resolve to someone else's code. What remains is the canonical name. **Recommended: option 2** in `findings.md` F-050, move the repository to an organisation and set both paths to it once. That is also item 1 of `succession.md`'s list, so the two decisions are one. Only the account owner can create the organisation. |
| **Succession: a second custodian** *(high)* | **The only item on this page that needs another human being.** Every live identity asset — the GitHub account, the signing identity, the images, npm, PyPI — has exactly one custodian, and the repository sits under a **personal** account that by construction cannot have a second owner. `./scripts/verify_custody.sh` fails today and is meant to; the 2026 drill in `succession-drill.md` is recorded as not completed. Two things the owner can do alone and now: move the repository to a GitHub organisation (an afternoon, breaks no URL), and add a second owner to the npm and PyPI packages once there is somebody to add. The rest waits on a second maintainer, which is a recruitment problem. Escalated to `cpo` per GRVX-1503 §10. |
| **GRVX-1506 comment window** | **Closed and final (DD-016).** The window closed on 2026-09-30 with no comments, and the `NOT YET` verdict stands. It was never announced outside this repository. That is acceptable only because `NOT YET` commits to nothing. Any donation RFC will need its own announced 14-day window, which is the owner's call. Two candidate foundations (the Linux Foundation directly, and The Commons Conservancy) could not be read at source from this environment and are still marked UNVERIFIED. Somebody with working access should read them and revise the table. |
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

- **F-025** *(high)* — *fixed and confirmed (DD-020).* The bootstrap stack's init container is now in the full stack, which also seeds its API key (F-058, DD-031). `docker-smoke` boots the full stack on every pull request and passed 11 of 11 on PR #25.
- **GRVX-1003** — *`partial`; decided as far as one maintainer can (DD-014).* The 120 bytes/event target stands, because it is reachable: gzip on raw facts gives 35.59. That change is RFC 0003, design tier, listed under Decisions above. The §5.2 Parquet encodings stay pinned in `pkg/encoding` and unapplied, because they act on 2.89 bytes/event and cannot move the total. See SD-055.
- **GRVX-1006** — *no longer blocked; now `partial`.* SD-024 was decided on 2026-10-01 (DD-008)
  after measuring with a real Cube on the bootstrap stack's limits: no pre-aggregations, warm p95
  51–100 ms from Cube's result cache, cold p95 193–593 ms for one day and up to 3.7 s for a week.
  The cold figure is published, not hidden. Measuring also found and fixed F-053, which broke every
  date-ranged query on the DuckDB stack. The warmer is built and measured against Cube (DD-032):
  one cycle made the default view's queries cache hits, 5 to 95 ms against up to 3 s cold. Doing it
  found F-059, now fixed, and F-060, open. The CI query driver and the percentile endpoint's figure
  remain.

### Every spec has now been audited against its own criteria

All 88 were read individually rather than trusted by label, and the labels were wrong four times:
**GRVX-1103** was not blocked at all, **GRVX-1312**'s core half was blocked behind its own `ee/`
half, **GRVX-1004** was blocked with its entire implementation already in the tree, and **GRVX-1003**
was closed by a defect its own §6 tells you how to handle. Each is now `partial` and each found a
real defect on the way — including a Grafana plugin that could not load and a published SQL guide
wrong by 144×.

What remains blocked is blocked for a named reason that an implementer cannot clear: a second
maintainer (SD-040), an external auditor (GRVX-1407), a pricing audit the implementer is forbidden to
self-verify (GRVX-1002), or a dependency on one of those (GRVX-1007, GRVX-1008). GRVX-1005's AC-1
waited on a reference machine until DD-018 named one, a GitHub-hosted `ubuntu-24.04` runner. Choosing
it found F-056: the benchmark's per-core ingest figure was a one-core rate divided by every core.
Measured correctly at standard scale, it is 73,868 events/sec/core against a target of 20,000, with
HTTP framing excluded, and AC-1 passes (GRVX-1005 §11.2).

**An unexplained status is indistinguishable from a spec nobody looked at.** That is what hid four of
these, and it is why every entry on this page now names its cause.
- **GRVX-1007, GRVX-1008** — not startable; 1007 depends on 1002/1003/1005/1006, and 1008 on 1007.
- **GRVX-1103** — *no longer blocked, and now done.* It was marked blocked with no reason recorded
  here, and turned out to be two documentation pages and a script. Its §5.1 table was wrong twice
  (SD-052), its connection steps could not reach Trino 435 (SD-060), and its rate queries rounded to
  0.1 per second (CD-006). All four criteria now pass, two of them in `docker-smoke` on the full
  stack. The lesson is the one below.
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
