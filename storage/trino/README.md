# Trino Configuration

This directory contains the Trino catalog and setup scripts for analyzing the data warehouse.

## Files

- `catalog/gravix.properties` and `catalog/gravix_iceberg.properties`: templates for the 'gravix' and 'gravix_iceberg' catalogs, with `${S3_ACCESS_KEY}`, `${S3_SECRET_KEY}` and `${S3_BUCKET}` filled in when the container starts. The rendered files live only inside the container; `config/` holds Trino's server configuration and no catalogs (F-066).
- The 'gravix_iceberg' catalog keeps its metastore in the bucket, at `s3a://<bucket>/iceberg-warehouse`, beside its tables, so other engines can read each table from its path (DD-033). The 'gravix' catalog's metastore is `data/trino-metastore/` on the host, which `data-init` gives to Trino's uid 1000.
- The 'gravix' catalog Note: It uses the **Hive** connector to read the raw/flattened Parquet files, as our ingestion sinks write standard Parquet without full Iceberg metadata.
- `init.sql`: DDL for creating the logical tables over the raw data directories.
- `run-queries.sh`: Helper script to execute setup and sample queries.

## Getting Started

1. Start the stack: `docker-compose up -d trino`
2. Run `./storage/trino/run-queries.sh` to see sample output.

## Notes

- The tables are defined as **EXTERNAL TABLES** pointing to `/data/warehouse/...`
- Partitions (YYYY-MM-DD directories) are automatically picked up by Trino/Hive if they follow standard Hive layout (`event_day=...`).
