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
