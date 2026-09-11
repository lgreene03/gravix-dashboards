# Storage Layout (MVP)

This document defines the physical storage layout for Facts and Derived Metrics using Apache Iceberg.

## 1. File Format

- **Format**: Parquet.
- **Compression**: ZSTD (Level 3-6).
- **Target File Size**: 128MB - 512MB.
- **Table Format**: Apache Iceberg V2.

## 2. Partitioning Strategy

### Raw Facts Table (`request_facts`, `service_events`)

- **Partition Key**: `day(event_time)` (UTC).
- **Rationale**:
  - Optimizes for daily batch processing jobs.
  - Prevents "small file problem" by accumulating enough data per partition.
  - Sufficient pruning for most analytical queries (e.g., "Show me error rates for last 7 days").
  - **Hourly Partitioning explicitly REJECTED** for MVP to reduce metadata overhead and file counts.

### Derived Metrics Table (`metrics_1m`)

- **Partition Key**: `day(bucket_start)` (UTC).
- **Sort Order**: `service`, `bucket_start` (ascending).
- **Rationale**:
  - Daily partitioning aligns with Raw Facts.
  - Sorting by `service` optimizes for dashboard queries that filter by service.

## 3. Storage Hierarchy

The data lake is organized into two distinct layers:

### Layer A: Landing / Raw (Immutable)

- **Content**: Original `RequestFact` and `ServiceEvent` records.
- **Retention**: **30 Days**.
- **Access Pattern**:
  - Bulk ingestion (append-only).
  - Periodic metric computation jobs (scan/read).
  - Debugging deep-dives (scan/read).
- **Compaction**: Run daily to merge small files into target 128MB+ files.

### Layer B: Aggregated (Derived)

- **Content**: Pre-computed `metrics_1m` table.
- **Retention**: **13 Months** (allowing year-over-year comparison).
- **Access Pattern**:
  - Dashboard queries (sub-second latency expected).
  - Trend analysis.
- **Compaction**: Aggressive. Rewrite partitions to ensure 1-2 files per day max for optimal read performance.

## 4. Metric Manifests

Every derived metric file has a manifest beside it, named for the data file with `.parquet`
replaced by `.manifest.json`:

```
warehouse/request_metrics_minute/event_day=2026-09-09/
  request_metrics_minute_20260909.parquet        # the data
  request_metrics_minute_20260909.manifest.json  # what it is and where it came from
```

The suffix matters. Trino and Cube read the warehouse with `**/*.parquet`; a manifest must never be
matched by that glob and read as data, which is why it is not named `*.parquet`.

### 4.1 What a manifest says

```json
{
  "schema_version": 1,
  "metric": "request_metrics_minute",
  "metric_version": "v1",
  "idempotency_key": "request_metrics_minute:v1:_single:20260909",
  "content_digest": "sha256:8bd94b28...",
  "tenant_id": "",
  "event_day": "2026-09-09",
  "window_from": "2026-09-09T00:00:00Z",
  "window_to": "2026-09-10T00:00:00Z",
  "row_count": 2,
  "fact_count": 4,
  "source_fact_keys": ["raw/request_facts/2026-09-09/10/batch_a.jsonl"],
  "revision": 0,
  "data_file": "warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet"
}
```

Two fields carry the weight:

- **`idempotency_key`** — `<metric>:<version>:<tenant>:<YYYYMMDD>`, with `_single` standing in for an
  empty tenant so the key never has an empty segment. It depends only on what the partition *is*, so
  two files with the same key claim to describe the same window. Answering "are these the same
  window?" needs no read of either file.
- **`content_digest`** — `sha256` over the rows: each row as JSON with sorted keys and no
  whitespace, joined with newlines, in file order. It covers row values only, never the Parquet
  container. A compression change or a library upgrade rewrites the bytes of an unchanged partition;
  that must not look like a data change, and it does not.

`revision` starts at 0 and advances by one each time a partition's rows change — late facts, a
corrected fact stream, a metric-definition change. It stays put when a rebuild produces the same
rows.

`source_fact_keys` is the partition's lineage: every raw fact object actually read to produce it.

### 4.2 Ordering rules

- The data file is written **before** its manifest, always. A manifest without its data file is a
  detectable inconsistency; a data file without a manifest is not.
- On deletion the order reverses in the same spirit: the data file goes **first**, then its manifest.
- Compaction merges manifests as it merges data. The merged file keeps its own identity, row count
  and digest, and inherits the **sorted union** of its sources' `source_fact_keys`, the **sum** of
  their `fact_count`, and the **maximum** of their `revision`. If no source carried a manifest, the
  merged file gets none — an empty lineage would read as "derived from nothing" rather than
  "unknown".

### 4.3 Format stability

`schema_version` is checked on read. A manifest written by a newer binary is refused rather than
partly understood. The serialised form is pinned by a golden fixture in
`pkg/manifest/testdata/golden_manifest.json`, so renaming, reordering, adding or removing a field
fails a test rather than silently changing the format.

## 5. Constraint Checklist

- [ ] **NO** secondary indexes.
- [ ] **NO** streaming writes (File commit interval > 5 mins).
- [ ] **NO** update/delete patterns (Copy-on-Write / Merge-on-Read strictly for GDPR compliance only).
- [ ] **Single Store**: All data resides in one object store bucket (e.g., S3). No separate "hot" vs "cold" storage tiers beyond object lifecycle policies.
