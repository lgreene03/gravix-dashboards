-- Protocol: Iceberg V2
-- Table: request_facts
-- Description: Raw, immutable facts representing completed HTTP requests.
-- Storage: Parquet / ZSTD
-- Partitioning: Daily (UTC) based on event_time
CREATE TABLE IF NOT EXISTS gravix.request_facts (
    event_time TIMESTAMP(6) WITH TIME ZONE NOT NULL,
    service VARCHAR NOT NULL,
    method VARCHAR NOT NULL,
    path_template VARCHAR NOT NULL,
    status_code INTEGER NOT NULL,
    latency_ms INTEGER NOT NULL,
    user_agent_family VARCHAR
) WITH (
    format = 'PARQUET',
    partitioning = ARRAY ['day(event_time)'],
    sorted_by = ARRAY ['event_time', 'service'],
    -- Optimize for batch writes & queries
    'write.format.default' = 'parquet',
    'write.parquet.compression-codec' = 'zstd',
    'write.target-file-size-bytes' = '134217728',
    -- 128 MB target
    -- Strict Append-Only (Iceberg V2 specific properties might vary by catalog, these are hints)
    'gc.enabled' = 'true',
    'history.expire.max-snapshot-age-ms' = '2592000000' -- 30 Days (matches retention policy)
);
-- Comments regarding constraints from 00-system-truth.md:
-- 1. NO primary key (Facts are an append-only stream).
-- 2. Sort order optimizes for time-range scans first, then service filtering.