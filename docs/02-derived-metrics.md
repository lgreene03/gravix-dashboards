# Derived Metrics (MVP)

This document defines the mathematical formulas and processing rules for all derived metrics.
All metrics are computed from `RequestFact` or `ServiceEvent` tables.

## 1. Bucketing Strategy

- **Granularity**: Strictly **1-minute** buckets.
- **Alignment**: Buckets align to the start of the UTC minute (`SS=00`).
- **Timestamp**: The bucket timestamp is the **inclusive start time** of the bucket.
  - Example: A request at `10:00:59.999` belongs to the `10:00:00` bucket.
  - Example: A request at `10:01:00.000` belongs to the `10:01:00` bucket.

## 2. Metric Definitions

### `request_count`

- **Definition**: Total number of `RequestFact` rows in the bucket.
- **Filter**: None.
- **Formula**: `COUNT(*)`

### `error_count`

- **Definition**: Total number of `RequestFact` rows where the request failed.
- **Filter**: `status_code >= 500`.
- **Formula**: `COUNT(*) WHERE status_code >= 500`

### `error_rate`

- **Definition**: The proportion of requests that failed.
- **Precondition**: `request_count > 0`. If `request_count == 0`, `error_rate` is `NULL` (or `0` depending on visualization requirements, but logically undefined).
- **Formula**: `error_count / request_count`

### `p50_latency`

- **Definition**: The 50th percentile of `latency_ms`.
- **Method**: Exact set or T-Digest approximation (implementation dependent, but conceptually the median).
- **Formula**: `APPROX_PERCENTILE(latency_ms, 0.5)`

### `p95_latency`

- **Definition**: The 95th percentile of `latency_ms`.
- **Method**: Exact set or T-Digest approximation.
- **Formula**: `APPROX_PERCENTILE(latency_ms, 0.95)`

## 3. Late Arrival Handling

- **Facts are Immutable**: Late arriving facts are simply appended to the `RequestFact` table with their original `event_time`.
- **Metrics are Derived**: The metric table is **NOT** updated in real-time for late data.
- **Correction Mechanism**:
  - The system detects partitions (time windows) where the row count of Facts has changed since the last metric computation.
  - The metrics for that specific 1-minute bucket are **fully recomputed** and overwritten.
  - No "updates" or "increments" — only atomic replacement of the calculated values for that bucket.

## 4. Recomputation Strategy

- **Batch Oriented**: Metrics are computed in periodic batches (e.g., every 5-15 minutes).
- **Idempotency**: Computing metrics for a time window `[T1, T2]` is idempotent.
  - `Compute(T1, T2)` always produces the same result given the same set of Facts.
- **Full History**: To correct historical errors or logical bugs in metric definitions:
  1. Update the definition (e.g., change `error_count` to include 400s).
  2. Truncate the metrics table (or a specific time range).
  3. Re-run the batch computation over the entire history of Facts.

## 5. `gravix recompute`

Recomputation is not a manual procedure. `gravix recompute` rebuilds any historical
window from the raw facts and replaces the prior output in place.

```bash
# Rebuild one week
gravix recompute --from 2026-09-01 --to 2026-09-08

# See what would change, without writing
gravix recompute --from 2026-09-01 --to 2026-09-02 --dry-run

# Multi-tenant, four partitions at a time
gravix recompute --from 2026-09-01 --to 2026-09-08 --tenant acme --tenant globex --concurrency 4
```

| Flag | Default | Meaning |
|---|---|---|
| `--from` | *required* | Start of the window, RFC3339 or `YYYY-MM-DD` |
| `--to` | *required* | End of the window, **exclusive** |
| `--metric` | `request_metrics_minute` | Metric to rebuild |
| `--tenant` | *(none)* | Tenant id; repeatable. Omit for single-tenant mode |
| `--input` | `./data/raw` | Raw facts directory |
| `--output` | `./data/warehouse` | Warehouse output directory |
| `--dry-run` | `false` | Plan and report without writing |
| `--concurrency` | `1` | Partitions rebuilt in parallel |

Exit codes: `0` success, `1` a partition failed, `2` invalid flags, `3` the lock is
held by a running rollup.

### 5.1 What makes a rebuild safe

- **One partition, one key.** A tenant-day always writes to
  `warehouse/<metric>/event_day=YYYY-MM-DD/<metric>_YYYYMMDD.parquet`. The key is
  derived from the partition, never minted per run, so a rebuild **replaces** its
  prior output rather than adding a second file the query layer would double-count.
- **Byte-identical output.** The same facts always encode to the same bytes. Rows are
  sorted by the full aggregation key (bucket, service, method, path template), each
  bucket's latencies are sorted before its percentiles are taken, the compression
  level is pinned rather than inherited from the library default, and nothing
  run-scoped — no timestamp, hostname, or run id — is written into the file.
- **Unchanged partitions are left alone.** Before writing, the engine encodes the new
  file in memory and compares it with what is already stored. Identical bytes count as
  `unchanged` and no write happens, so re-running a window is a genuine no-op rather
  than a rewrite that merely lands on the same values.
- **Read order does not matter.** The object store gives no ordering guarantee. Fact
  files are read in a fixed order and every aggregation is order-independent, so two
  rebuilds of the same day agree bit for bit.
- **Facts are never touched.** Recompute reads facts and rewrites derivatives only,
  per `docs/00-system-truth.md` §2.
- **One writer at a time.** A recompute takes the same lock as the cron rollup, so the
  two can never write the same partition concurrently. A held lock exits `3` and
  writes nothing.

### 5.2 Why it matters

`docs/00-system-truth.md` §4 promises that every metric is recomputable. That promise
is only worth something if a rebuild is provably the same as the original — otherwise
"recomputable" means "will produce some other number, later". The determinism rules
above are what make the promise checkable, and `pkg/recompute` tests each of them by
name.
