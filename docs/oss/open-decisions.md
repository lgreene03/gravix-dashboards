<!-- A navigation aid, not a register. The registers are findings.md, spec-defects.md and -->
<!-- correctness-defects.md, all append-only. This file points into them and may be rewritten. -->
# What needs a person

Three registers hold 76 entries between them, most of them resolved. Most are ordinary work an
implementer can pick up. This page lists only the ones that **cannot be closed by implementing
harder**, because they need a decision, a permission, or an external check.

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
| **SD-013** *(high)* | Whether `dashboard_config.js` may carry a live, write-capable ingestion key that any dashboard visitor can read | A security posture. Half of it is already mitigated (the compose file mounts one file rather than the data directory); the other half is a real exposure with no obviously correct fix. |
| **CD-005** | Which zstd level every Parquet writer uses — `SpeedFastest`, as `pkg/recompute` pins, or `SpeedDefault`, as compaction and the two service-events transforms use | A size-versus-CPU trade, and GRVX-801 §5.3 forbids changing the constant without re-verifying determinism. Until it is one value, a compacted partition can never be byte-identical to a recomputed one. |
| **SD-024** *(high)* | Whether GRVX-1006's pre-aggregations get an external store (another container, contradicting F-035 and GRVX-1004's cost figure), get one via `CUBEJS_DEV_MODE=true` (a development flag made load-bearing for production performance), or are dropped so ≤400 ms must be met reading Parquet directly | §5.1 mandates four rollups; no shipped stack provides an `externalDriverFactory`, and Cube fails a query matching a rollup it cannot build rather than reading the source. The spec names Redis as the optional component, which is not where a rollup lives. Each resolution trades against a position already taken. |
| **F-039** *(high)* | Whether compaction preserves and merges `latency_sketch`, or whether compacted days are documented as scalar-only with percentiles dropped rather than averaged | Compaction rewrites `request_metrics_minute` through its own twelve-field `MetricRow`, silently deleting the five columns Phase 8 added — `latency_sketch` among them — and then sets each merged percentile to the arithmetic mean of the per-bucket percentiles. That is the exact error the sketch exists to prevent, committed by the job that deletes the sketch. It is a schema and retention trade, and it makes the correctness axis true for fresh data and false for compacted data. |
| **SD-029** *(high)* | Whether `/api/gateway/export` (exists today, streams a tar.gz of raw JSONL) and GRVX-1107 §5.4's new `/api/gateway/exports` are reconciled or kept as two endpoints one character apart | GRVX-1107 §2 names `services/gateway/enterprise.go` as holding the scheduled-export code; it holds none — the code is in `gateway_platform.go`, which §4 does not permit touching, so AC-7/8/9/10/12 are unreachable. §4 also omits `cmd/cli/main.go`, without which the new subcommand is dead code that fails `staticcheck`. The engine and CLI are built and tested; the gateway half needs the file list corrected and the endpoint collision decided. |
| **F-042** *(medium)* | Whether to add two mechanical checks to the Spec Readiness Gate: every §2/§4.2 path must resolve in the repository, and every file named in §6 or §7 must appear in §4 | Every Phase 11 spec records 12/12 PASS, and four of the five executed carried a §2 or §4 defect anyway. Check 1 tests that paths are well-formed, not that they are right or complete. Two of the defects would have produced a red build if followed literally. Both proposed checks are scriptable, in the spirit of `check-boundary`. |
| **SD-026** *(high)* | Whether `docs-site/docs/bare-parquet-access.md` publishes GRVX-1101 §6's `part-0.parquet` query, the `*.parquet` query that actually works on a warehouse, or both | §6 mandates a query whose glob matches only the test fixture; on real data DuckDB fails it with "No files found that match the pattern". Both are published for now, the wide one flagged as the one to use, and both are held under test. Which one a public page leads with is a product call. |
| **SD-023** *(medium)* | One `Batcher` per (tenant, topic) file, or one fronting all files and fsyncing each it touched | Per-file preserves the on-disk layout and loses most of the amortisation on a multi-tenant node; all-files keeps the throughput and is not what §5.1 describes. Measured curve: 1 fact/fsync ≈ 4,500/sec/core, 8 ≈ 43,000, 512 ≈ 500,000. |
| **SD-016** *(medium)* | Whether the cardinality budget stays per-process when the shipped Helm chart runs ingestion at 2–10 replicas | The bound is up to 10× looser than documented and segment collapsing is non-deterministic across pods. Either the budget is wrong or the chart is. |
| **SD-015** | Which key result owns Phase 9's "one alert rule armed" exit criterion | A CPO call. An exit criterion no KR measures cannot be said to have been met. |
| **F-026** *(high)* | When to add `docker-smoke` and `docker-build` to `ci-summary`'s `needs` | One line, and it turns every pull request red immediately — correctly, but for a failure nobody has diagnosed and whose logs have expired. Sequencing, not code. |

## Spec text that contradicts what was measured

These need an edit to the spec (and in one case the thesis), not to the code. The implementations
already work around them and say so.

| Item | The spec says | What was measured |
|---|---|---|
| **SD-021** *(medium)* | GRVX-1004 §5.2's mandatory caveat: the at-scale figure "is roughly 10x the bootstrap figure" | 43× at 1M events/month, 43× at 50M, 4× at 1B — wrong in the dangerous direction for the reader the sentence addresses. Also in `01-competitive-thesis.md` §2 Axis 4. |
| **SD-022** *(medium)* | GRVX-1005 §5.3: the SIGKILL test "is the only test that actually proves §6; a unit test asserting `fsync` was called does not" | The reverse. Against a batcher that acknowledges before fsyncing, `TestDurabilityUnderKill` passes three times out of three and the two unit tests fail. SIGKILL does not discard the page cache. |
| **SD-019** *(high)* | GRVX-910 §2: Cube's `load` endpoint is unauthenticated because no `CUBEJS_API_SECRET` is set | `checkAuth` tests `JWT_SECRET` first, and the bootstrap compose sets it inline rather than through `.env`. Cleared in implementation; §2's sentence is still wrong. |

## Permissions and external checks

| Item | What is needed |
|---|---|
| **DCO** | **Done.** The repository owner signed off, and all 86 commits on `claude/gravix-opensource-roadmap-c8ija8` now carry `Signed-off-by: Luke Greene <luke.greene86@gmail.com>`. The trailer was added with `git rebase <base> --exec 'git commit --amend --no-edit --trailer …'` rather than `git rebase --signoff`, because `--signoff` derives the trailer from `git config user.*` and makes that identity the **committer**, which would have stripped the verified status from every commit — trading 86 signatures for one text trailer. Retained here as the reason not to reach for `--signoff` next time. |
| **GRVX-1002 CLAIM AUDIT** | A `market-analyst` pass over `bench/cardinality/competitor_units.yaml`, reading each vendor's own pricing page and recording the date. Every entry is `verified: false` and the demo withholds the comparison until that happens. The implementer must not self-verify. |
| **GRVX-1004 prices** | The same discipline for `pkg/costmodel/prices.yaml`. Every infrastructure rate is marked `estimate`, not `list_price`, because none has been read off a vendor page and dated. |

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
- **GRVX-1003** — returned `SPEC DEFECT: §5.1`; the measured footprint is 206.68 bytes/event against
  a 120 budget, and 35.59 if raw JSONL were compressed at rest, which nothing does.
- **GRVX-1006** — nine of ten criteria need a running Cube. Note, now a tested fact rather than a
  warning: this stack declares no pre-aggregations, so every latency figure it produces is a **cold
  read from Parquet**. Quoting one as pre-aggregated repeats F-020 and F-022. See F-038.
- **GRVX-1007, GRVX-1008** — not startable; 1007 depends on 1002/1003/1005/1006, and 1008 on 1007.

Five defects were fixed to get this far — F-015, F-016, F-019, F-021, F-023 — and the gate found
three of them itself.
