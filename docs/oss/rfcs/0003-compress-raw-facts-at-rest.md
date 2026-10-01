---
rfc: 0003
title: Compress raw facts at rest, in compaction, as gzip JSONL
author: lgreene03
status: draft
tier: design
opened: 2026-10-01
comment_closes: 2026-10-08
decided:
approvals: []
supersedes: 0
touches_entrenched: false
---

# RFC 0003: Compress raw facts at rest, in compaction, as gzip JSONL

## Summary

Raw facts are stored as uncompressed JSONL, at about 204 bytes per event. That one term is why
Gravix misses its own storage goal (G4.4, ≤120 bytes per event) by 87 bytes, while everything
derived from the facts costs under 3. This proposes that compaction write the consolidated raw file
for each day as gzip-compressed JSONL (`consolidated_<uuid>.jsonl.gz`), and that every reader of raw
facts accept both forms through one shared function. Ingestion's write path does not change. Nothing
is deleted or lossily re-encoded, and gzip is readable by every tool that reads JSONL today.

## Motivation

GRVX-1003 measured the footprint on the standard bench run, 1,008,000 facts:

| Component | bytes/event | Budget |
|---|---:|---:|
| Raw JSONL, as stored today | 203.78 | ≤70 |
| Rolled-up Parquet, sketch included | 2.89 | ≤45 |
| Manifests | 0.01 | ≤1 |
| **Total** | **206.68** | **≤120** |

The same raw bytes under `gzip -6` are 32.68 bytes/event, which puts the total at 35.59. The budget
is not ambitious. It assumed raw facts were "compressed at rest" (GRVX-1003 §5.1), and nothing in the
repository compresses them.

GRVX-1003 could not make the change, because §4.3 forbids `services/ingestion/**` and no reader is in
its §4. The spec's own main task, per-column Parquet encodings, acts on the 2.89 and cannot move the
total. This is recorded as SD-055 in `docs/oss/spec-defects.md`, and the decision not to apply those
encodings is DD-014 in `docs/oss/delegated-decisions.md`.

For a self-hoster, raw facts are most of the bill. At a billion events a month and 30-day retention,
the difference is roughly 204 GB against 33 GB of object storage held at all times.

## Proposal

1. **Compaction writes gzip.** `compactJSONLGroup` in `transforms/compaction/main.go` writes its
   consolidated output through `compress/gzip` at `gzip.DefaultCompression`, to a key ending
   `.jsonl.gz`. Its input filter stays `.jsonl`, so an already-compressed file is never re-read or
   re-compacted.
2. **Ingestion is unchanged.** `current.jsonl` and the rotated `batch_*.jsonl` stay plain. The hot
   path, its fsync discipline and its crash-recovery story are not touched, which keeps GRVX-1003
   §4.3's reason intact.
3. **One reader for raw facts.** A new `pkg/storage` function, `IsRawFactKey(key string) bool`, is
   true for `.jsonl` and `.jsonl.gz`. A second, `OpenRawFacts(ctx, store, key)`, returns a reader
   that decompresses when the key ends `.gz`. Every reader of raw facts uses both:
   - `pkg/etl` (`ReadLines` and the `FileExtension` filter used by the rollup)
   - `pkg/recompute` (the fact scan)
   - `transforms/service_events_daily` and `transforms/service_events_detail`
   - `pkg/export` (`runDataset`), and the gateway's archive export in `pkg/gatewaycore`
   - `pkg/evolve/cardinality.go`
   - `cmd/cli/cmd_migrate_import.go`
   - `bench/storage/footprint.go`
4. **Retention covers both.** `scripts/cleanup_data.sh` matches `*.jsonl` only today. It must match
   `*.jsonl.gz` too, or compressed facts outlive the 30-day purge `CLAUDE.md` requires. `cmd/purge`
   selects by date prefix and needs no change; a test should say so.
5. **A guard like the Parquet-level one.** A governance test fails when any non-test Go file filters
   raw keys with a bare `HasSuffix(key, ".jsonl")` instead of `IsRawFactKey`. A reader that skipped
   compressed files would silently drop most of history from a recompute. That failure is quiet and
   total, so it needs a mechanical guard, not review.
6. **The claim follows the format.** Axis 3 in `01-competitive-thesis.md` says raw facts land "as plain
   JSONL". It becomes "as JSONL, gzip-compressed once compacted", with the readers named. That edit
   goes through the claim register, as every Axis 3 change does.

## Alternatives considered

- **Do nothing and restate G4.4 to about 210 bytes/event.** Honest, and costs nothing now. Rejected
  because it makes self-hosting six times more expensive in storage than it needs to be, to avoid a
  change that is lossless.
- **Compress at rotation, in ingestion.** Covers the current day as well, so steady state is a little
  smaller. Rejected because it puts compression and a new failure mode on the write path, which
  GRVX-1003 §4.3 protects for good reason. Compaction runs off the hot path and can be retried.
- **zstd instead of gzip.** Smaller and faster. The Parquet writers already link it. Rejected for raw
  facts because Axis 3 promises any tool can read them, and `zcat`, `jq`, pandas, Spark and DuckDB all
  read gzip out of the box. zstd support is common but not universal. This is the question most worth
  comment.
- **Leave it to the storage layer.** S3 has no transparent compression, and filesystem compression is
  the operator's choice, not Gravix's. A number Gravix publishes cannot rest on it.
- **Apply GRVX-1003 §5.2's Parquet encodings instead.** They act on 2.89 bytes/event. See DD-014.

## Non-goals crossed

None. The non-goal a reader might worry about is none of the seven, but `docs/00-system-truth.md` §4:
recomputability from raw facts. gzip is lossless, so every fact survives byte for byte after
decompression. `ContentDigest` covers row values, never containers, and `IdempotencyKey` does not
depend on file names, so recompute output is unchanged.

## Charter impact

None. No entrenched clause is touched. The change stays in the Apache-2.0 core, where storage cost
belongs under charter §7.3 Q1, as GRVX-1003's own placement says.

## Migration

Nothing for existing installs. Files already on disk stay `.jsonl` and remain readable. The next
compaction run writes new consolidated files as `.jsonl.gz`, and existing consolidated files are left
as they are until retention removes them.

Anyone who reads `data/raw/` directly with their own tools needs to read `.jsonl.gz` as well. That is
the change the release notes must lead with, because it is the one a user notices.

A downgrade to a release without this change would not read `.jsonl.gz` files, and a recompute on
that release would silently omit them. The upgrade notes must say so, and a downgrade must first run
a one-line decompression over `data/raw/`.

## Unresolved questions

- **The steady-state number.** 35.59 assumes every raw byte is compressed. With compaction it is every
  day except the ones still taking writes, so the true 30-day figure is somewhat higher. It needs
  `bench/storage` at 30-day steady state on the bench run's facts. Its own synthetic corpus cannot
  answer it: that corpus compresses about 108×, against about 6× for the bench run (SD-055).
- **gzip or zstd** for raw facts, per the alternatives above.
- **`SourceFactKeys` after compaction.** Manifests list the raw keys a partition was built from.
  Compaction already renames raw files today, so those keys can already go stale. That is a
  pre-existing issue, but this change touches the same names and should not make it worse.
- **Compaction's current-day behaviour.** Compaction acts on the last two days, and the current day
  keeps receiving hourly files after a pass. Each later pass would write a further `.jsonl.gz` for the
  same day. That is correct but untidy. Whether to merge them is an implementation detail for the
  spec that follows this RFC.
