# Trino Configuration

This directory contains the Trino catalog and setup scripts for analyzing the data warehouse.

## Files

- `catalog/gravix.properties`: Defines the 'gravix' catalog. Note: It uses the **Hive** connector to read the raw/flattened Parquet files, as our ingestion sinks write standard Parquet without full Iceberg metadata.
- `init.sql`: DDL for creating the logical tables over the raw data directories.
- `run-queries.sh`: Helper script to execute setup and sample queries.

## Getting Started

1. Start the stack: `docker-compose up -d trino`
2. Run `./storage/trino/run-queries.sh` to see sample output.

## Notes

- The tables are defined as **EXTERNAL TABLES** pointing to `/data/warehouse/...`
- Partitions (YYYY-MM-DD directories) are automatically picked up by Trino/Hive if they follow standard Hive layout (`event_day=...`).
