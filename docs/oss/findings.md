<!-- Findings about the CODEBASE, discovered while executing specs. -->
<!-- Defects in the SPECS themselves go in spec-defects.md. Append; do not rewrite. -->
# Repository findings

Things noticed while implementing Horizon 2 that are real problems but outside the scope of the
spec that found them. Each names what is wrong, why it matters, and who should fix it.

A finding is recorded rather than fixed in place because a spec that quietly widens to fix
everything it notices becomes unreviewable. See `11-agent-loops.md` §L4, scope gate.

---

## F-001 — compiled binaries are committed to version control

**Found by:** `senior-engineer` executing GRVX-709
**Owner:** `security-engineer`
**Severity:** medium — supply-chain hygiene

Two compiled binaries are tracked in git at the repository root:

| Path | Size | What it actually is |
|---|---|---|
| `cli` | 8.7 MB | **Mach-O 64-bit arm64 executable** — a macOS binary |
| `service_events_detail` | 27.5 MB | compiled binary |

Three problems:

1. **They cannot be verified against source.** GRVX-709 makes every released binary reproducible
   and signed. These are neither. A user who runs `./cli` from a checkout is executing something
   nobody can attest to.
2. **`cli` is a macOS ARM64 binary in a project whose CI, containers and deployment target Linux.**
   It is not usable by most of the people who will clone the repository, so it is not even serving
   the convenience it presumably existed for.
3. **36 MB of the repository is build output.** Every clone pays for it, forever, including the
   history.

**Recommendation:** delete both, add them to `.gitignore`, and let `make build` produce them into
`bin/` as it already does. `make build` already builds `bin/gravix`, so `cli` is redundant as well
as unverifiable.

**Why this was not fixed here:** GRVX-709 §3 explicitly says to report rather than delete —
removing tracked files is its own change with its own review, and bundling it into a signing
change would hide it.

---

## F-002 — 29 Go files are not gofmt-formatted

**Found by:** `senior-engineer` executing GRVX-701
**Owner:** `senior-engineer`
**Severity:** low — consistency

29 files under `services/`, `transforms/` and `pkg/` do not match `gofmt` output. Verified against a
stashed baseline that **all 29 were already unformatted before Horizon 2 began**, and that adding
licence headers introduced none of them.

**Recommendation:** one mechanical `gofmt -w` change, reviewed as a formatting-only diff so it is
easy to verify by eye, ideally followed by a CI check so it cannot recur.

**Why this was not fixed here:** GRVX-701 §4.2 limited that spec to prepending headers. A
formatting pass touching 29 files inside a licensing change would have been a scope violation and
would have made the licence diff unreadable.

---

## F-003 — compaction cannot see Hive-partitioned warehouse output

**Found by:** `senior-engineer` executing GRVX-802
**Owner:** `senior-engineering-lead`
**Severity:** medium — a scheduled job that silently does nothing

`transforms/compaction/main.go` finds files to merge with `parseWarehouseKey`, which accepts only
the **flat** warehouse layout:

| Key | Parsed? |
|---|---|
| `warehouse/request_metrics_minute/metrics_abc_2026-05-21.parquet` | yes |
| `warehouse/t1/request_metrics_minute/metrics_abc_2026-05-21.parquet` | yes |
| `warehouse/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet` | **no** |
| `warehouse/t1/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet` | **no** |

The four-segment Hive key is read as multi-tenant, giving `tenantID="request_metrics_minute"` and
`topic="event_day=2026-05-21"`. The topic then fails the allow-list and the key is skipped. The
five-segment multi-tenant Hive key has no branch at all.

The rollup has written Hive-partitioned output since Phase 5, and `storage/trino/init.sql:32` and
`cube/model/schema/RequestMetricsMinute.js` both read that layout. So **compaction currently
processes none of the metric files the system actually produces.** It runs, logs
"Found 0 Parquet compaction groups", and reports success.

This was verified directly against `parseWarehouseKey`, and the behaviour is now pinned by
`TestWarehouseKeysAreFlatLayoutOnly` so it cannot regress unnoticed while someone believes it is
fixed.

### Why it was not fixed here

GRVX-802 is a manifest spec. Teaching compaction a new key layout changes which files get merged,
deleted and renamed in a live warehouse — that is a data-movement change and needs its own spec,
its own dry-run evidence and its own rollback story. GRVX-802 §5.4's manifest rules **are**
implemented in `compactParquetGroup`, and tested directly, so they are correct the moment
compaction can reach these files.

### What this means for the manifest work

Merged-manifest handling is implemented and proven at the function level, not end to end, because
no production path reaches it. GRVX-810's correctness suite should treat "compaction merges a real
Hive partition and its manifest survives" as an open, unproven criterion until a spec fixes
`parseWarehouseKey`.

### Related

The same function is why GRVX-801's deterministic key is safe from compaction today: compaction
would otherwise rename merged output to `metrics_<uuid>_<date>.parquet`, reintroducing a
non-deterministic key. A spec that fixes `parseWarehouseKey` must fix that naming in the same
change, or it will undo GRVX-801.
