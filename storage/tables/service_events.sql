-- Protocol: Iceberg V2
-- Table: service_events
-- Description: Raw, immutable structured business/state events.
-- Storage: Parquet / ZSTD
-- Partitioning: Daily (UTC) based on event_time
CREATE TABLE IF NOT EXISTS gravix.service_events (
    event_time TIMESTAMP(6) WITH TIME ZONE NOT NULL,
    service VARCHAR NOT NULL,
    event_type VARCHAR NOT NULL,
    entity_id VARCHAR,
    properties MAP(VARCHAR, VARCHAR) -- Strictly flat map of string->string
) WITH (
    format = 'PARQUET',
    partitioning = ARRAY ['day(event_time)'],
    sorted_by = ARRAY ['event_time', 'service', 'event_type'],
    -- Optimize for batch writes & queries
    'write.format.default' = 'parquet',
    'write.parquet.compression-codec' = 'zstd',
    'write.target-file-size-bytes' = '134217728',
    -- 128 MB target
    -- Retention Policy (30 Days)
    'history.expire.max-snapshot-age-ms' = '2592000000'
);
-- Note: `properties` map column will be stored efficiently in Parquet
-- but queries filtering on map keys may be slower than dedicated columns.
-- This is acceptable for MVP "low-cost" design.