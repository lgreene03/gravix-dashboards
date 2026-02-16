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

## 4. Constraint Checklist

- [ ] **NO** secondary indexes.
- [ ] **NO** streaming writes (File commit interval > 5 mins).
- [ ] **NO** update/delete patterns (Copy-on-Write / Merge-on-Read strictly for GDPR compliance only).
- [ ] **Single Store**: All data resides in one object store bucket (e.g., S3). No separate "hot" vs "cold" storage tiers beyond object lifecycle policies.
