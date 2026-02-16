# Architecture: Durable Batch with Time-Bounded Deduplication

## 1. Overview

This updated architecture prioritizes **System of Record** integrity with minimal operational overhead.

- **Strict Durability**: fsync before ACK.
- **Deduplication**: Bounded by time partitions (`event_time`).
- **No Streaming**: Pure batch processing.

## 2. Updated Schemas

Both `RequestFact` and `ServiceEvent` now require:

1. `event_id`: ULID or UUIDv7 (Primary Key for dedupe).
2. `event_time`: Timestamp (RFC3339) (Partition Key).

```go
type RequestFact struct {
    EventID         string    `json:"event_id"`   // PK: Deduplication (Must be UUIDv7)
    EventTime       time.Time `json:"event_time"` // PK: Partitioning
    // ... other fields
}
```

## 3. Ingestion Service (Stateless + Durable)

1. **Receive**: `POST /api/v1/facts`
2. **Validate**: Schema + `event_id` presence.
3. **Persist**: Append to local rotating file, **fsync**, then ACK 201.
4. **Upload**: Background rotation (every 60s) -> S3 Upload -> Delete Local.
    - S3 Path: `s3://bucket/raw/request_facts/YYYY-MM-DD/HH/<uuid>.jsonl.gz` (Based on *Arrival Time*).

## 4. Rollup Job (Deduplication Engine)

- **Constraint**: The job processes a specific **Time Window** (e.g., one hour: `2023-10-27T10:00:00Z` to `10:59:59Z`).
- **Input**: Reads raw files that *might* contain data for this window.
  - Since ingestion stores by arrival time, strictly speaking, late data for 10:00 might arrive at 12:00.
  - **Optimization**: For MVP, we scan the raw directories for `[WindowStart - Buffer, WindowEnd + Buffer]` (e.g., +/- 1 hour).
  - **Filter**: Start by scanning; discard records where `event_time` is outside the target window.

### Deduplication Logic (Window-Bounded)

1. **Load**: Stream records from S3 where `event_time` is in `[T, T+1h)`.
2. **Dedupe**:
    - Maintain `map[string]bool` for `event_id` ONLY for this 1-hour window.
    - If `seen[id]`, skip.
    - Complexity: 1 Hour of data < RAM. (Even 1M requests/hour * 16 bytes ID = ~16MB RAM).
    - Safe for MVP scale.
3. **Aggregate**: Compute metrics.
4. **Write**: Overwrite `s3://bucket/warehouse/metrics/.../hour=10/...` partition.

## 5. Handling Late Data

- If data arrives > 1 hour late?
- **Option A (Simple)**: Rollup job for "Hour 10" runs at Hour 12. Late data is ignored.
- **Option B (Recompute)**: We allow re-running the job for past windows. If we find late data, we just re-run "Hour 10" job.
- **Decision**: **Manual/Scheduled Recompute**. The "Backfill CLI" already supports this. The architecture allows re-running any historical window safely because it's idempotent.

## 6. Failure Modes

| Failure | Impact | Mitigation |
| :--- | :--- | :--- |
| **Crash Before Flush** | 500 Error. | Client Retries. |
| **Crash After Flush (Before ACK)** | Duplicate on Disk. | **Downstream Dedupe** handles this seamlessly. |
| **Duplicate Upload** | Same file twice in S3. | **Downstream Dedupe** ignores second copy. |
| **Memory Pressure** | High volume hour. | Scale up Job RAM, or split window to 15m. |

## 7. Next Steps

1. **Update Schemas**: `event_id` imperative.
2. **Update Ingestion**: Implement `fsync` persistence.
3. **Update Rollup**: Implement time-window filtering and deduplication.
4. **Cleanup**: Remove Kafka/Sink-Writer.
