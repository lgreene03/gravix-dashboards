# The Gravix benchmark

Every cost and performance number Gravix publishes comes from this directory. The point of that is
falsifiability: clone the repository, run one command, and hold your result against ours.

It needs no cloud account, no paid service, no Docker and no network. If it needs any of those on
your machine, that is a bug — see the last section.

## Run it

```bash
./bench/run.sh --scale small
```

| Scale | Facts | Roughly | What it is for |
|---|---|---|---|
| `small` | ~1e6 | 2 min | a laptop, or CI |
| `standard` | ~1e7 | 15 min | **the published figures come from this** |
| `large` | ~1e8 | 2 h | capacity planning |

The result lands in `bench/results/<timestamp>-<scale>.json`. Two other flags exist and are
deliberately not in the usage line, because they change what a number means rather than which number
is measured:

- `--runs <n>` — measurements per metric before taking the median. Defaults to 3. **A result
  produced with `--runs 1` is not comparable to a published one**, because a single run is noise.
- `--work-dir <path>` — keep the scratch directory instead of using a temporary one that is deleted.

Exit codes: `0` success, `1` a measurement failed, `2` invalid argument, `3` not enough free disk.
The disk check runs *before* any data is generated, because filling the disk halfway through leaves a
truncated dataset that every later stage measures as though it were complete.

## What each number means

| Field | What it measures | What it does not |
|---|---|---|
| `ingest_events_per_sec_per_core` | Facts decoded, schema-validated and appended to the durable buffer, per second, divided by `machine.num_cpu`. The raw total before the division is in `notes`. | **Not** an HTTP request rate. The ingestion HTTP handler lives in `package main` and cannot be imported, and the benchmark does not restructure production code to measure it. Over a real network this figure is an upper bound you will not reach. |
| `ingest_p99_latency_ms` | The 99th percentile of that per-fact work, computed from every observation — not a sketch, not a mean. | Not a client-observed latency; it excludes connection setup, TLS and queueing. |
| `rollup_events_per_sec` | Facts read per second by `pkg/recompute`, the same code path `gravix recompute` runs. Each repeat starts from an empty warehouse, because recompute is idempotent and would otherwise skip the work being timed. | |
| `bytes_per_event_raw` | Newline-delimited JSON on disk, divided by fact count. This is what Gravix stores that Prometheus does not. | |
| `bytes_per_event_rolled_up` | Parquet in the warehouse, divided by the fact count it was derived from. | Not a per-row figure. Rows are far fewer than facts; that is the compression. |
| `bytes_per_event_compacted` | The same after the compaction job runs. | If compaction finds nothing to merge — one file per partition, say — this equals the rolled-up figure, and `notes` says so rather than omitting it. |
| `query_p50_ms`, `query_p95_ms`, `query_p99_ms` | Wall-clock time to read every metric row back out of the warehouse, warm. The row count per call is in `notes`. | Not a Cube or dashboard latency; there is no HTTP, no semantic layer and no browser in this path. |
| `query_cold_p95_ms` | The first such read, by a process that has not touched those files. | **Not a cleared page cache.** Dropping caches needs root, and a benchmark needing root is not one a stranger runs. The true cold figure on a fresh machine is *worse* than this, and `notes` says so on every run. |
| `peak_resident_bytes` | High-water resident set of the benchmark process. | Not the memory a deployed Gravix uses; this process does every stage in one address space. |

`notes` carries every condition that qualifies the run: the individual measurements behind each
median, a container CPU quota, an undetectable disk type, a spread above 20% across runs. Unfavourable
conditions are recorded, never retried away.

## The reference machine

There is no reference machine yet. The `machine` block in each result records the one it ran on:

```json
"machine": {
  "os": "linux", "arch": "amd64", "num_cpu": 4,
  "cpu_model": "…", "mem_total_bytes": …,
  "disk_type": "ssd" | "hdd" | "unknown",
  "go_version": "…", "gravix_commit": "…", "container": false
}
```

Any field that cannot be determined is the literal string `unknown`. Nothing here is inferred from a
plausible default: a guessed CPU model is worse than a blank one, because a reader cannot tell it
from a measured one. `gravix_commit` gains a `-dirty` suffix when the tree has uncommitted changes,
since such a result cannot be reproduced from the commit alone.

When `container` is true, `num_cpu` may exceed the CPU quota the process actually gets, which makes
the per-core figure optimistic. That is noted on the run rather than corrected, because the size of
the correction is not knowable from inside the container.

## Why your numbers will differ

- **Cores.** The per-core figure divides by `num_cpu`, which is not the same as scaling linearly.
- **Disk.** `bytes_per_event_*` will match closely; the throughput figures will not. An NVMe SSD and
  a network block device differ by more than an order of magnitude on the rollup stage.
- **Page cache.** Every query figure here is warmer than a real first query. See the table above.
- **CPU frequency.** A laptop that thermally throttles on a 15-minute `standard` run produces a
  number a desktop will not reproduce. Check the spread note.
- **Go version.** Recorded in every result, because it moves these numbers.

A result that differs from ours by a factor of two on different hardware is expected and not
interesting. A result that differs in the *ordering* of the stages, or that contradicts a claim we
publish, is very interesting.

## Reporting a result that contradicts ours

A benchmark that does not invite falsification is advertising. If your numbers contradict something
Gravix publishes:

1. Run it again with `--runs 3` (the default) so the comparison is median against median.
2. Open an issue at https://github.com/lgreene03/gravix-dashboards/issues titled
   `benchmark: <the claim it contradicts>`.
3. Attach the whole result file, `machine` block and `notes` included. The `notes` are usually where
   the explanation is.

A contradiction that survives that gets recorded in
[`docs/oss/correctness-defects.md`](../docs/oss/correctness-defects.md) and the published claim is
corrected or withdrawn. That register exists because this process keeps finding things — which is the
point of having it.
