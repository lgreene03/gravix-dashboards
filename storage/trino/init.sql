-- Create the Schema in the gravix catalog (mapped to local file system)
CREATE SCHEMA IF NOT EXISTS gravix.default;
CREATE SCHEMA IF NOT EXISTS gravix.raw;
-- Request Facts (JSONL lines in /data/raw/request_facts/YYYY-MM-DD/HH/*.jsonl)
-- Uses recursive directory scanning (hive.recursive-directories.enabled=true)
CREATE TABLE IF NOT EXISTS gravix.raw.request_facts (
    event_id VARCHAR,
    event_time TIMESTAMP,
    service VARCHAR,
    method VARCHAR,
    path_template VARCHAR,
    status_code INTEGER,
    latency_ms INTEGER,
    user_agent_family VARCHAR
) WITH (
    format = 'JSON',
    external_location = '/data/raw/request_facts'
);
-- Service Events (JSONL)
CREATE TABLE IF NOT EXISTS gravix.raw.service_events (
    event_id VARCHAR,
    event_time TIMESTAMP,
    service VARCHAR,
    event_type VARCHAR,
    entity_id VARCHAR,
    properties MAP(VARCHAR, VARCHAR)
) WITH (
    format = 'JSON',
    external_location = '/data/raw/service_events'
);
-- Derived Metrics (Parquet)
-- The rollup job writes to /data/warehouse/request_metrics_minute/event_day=YYYY-MM-DD/*.parquet
-- Using non-partitioned table with recursive directory scanning to avoid partition sync issues
-- with Trino 351's file-based Hive metastore.
DROP TABLE IF EXISTS gravix.raw.request_metrics_minute;
CREATE TABLE gravix.raw.request_metrics_minute (
    bucket_start VARCHAR,
    service VARCHAR,
    method VARCHAR,
    path_template VARCHAR,
    request_count BIGINT,
    error_count BIGINT,
    error_rate DOUBLE,
    p50_latency_ms DOUBLE,
    p95_latency_ms DOUBLE,
    p99_latency_ms DOUBLE,
    event_day VARCHAR
) WITH (
    format = 'PARQUET',
    external_location = '/data/warehouse/request_metrics_minute'
);

-- Service Events Daily Summary (Parquet from service_events_daily rollup)
DROP TABLE IF EXISTS gravix.raw.service_events_daily;
CREATE TABLE gravix.raw.service_events_daily (
    event_day VARCHAR,
    service VARCHAR,
    event_type VARCHAR,
    event_count BIGINT
) WITH (
    format = 'PARQUET',
    external_location = '/data/warehouse/service_events_daily'
);