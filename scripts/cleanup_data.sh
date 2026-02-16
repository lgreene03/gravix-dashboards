#!/bin/sh
# scripts/cleanup_data.sh
# Deletes raw and warehouse data older than 30 days to prevent disk fill-up.

RETENTION_DAYS=30
DATA_DIR="/Users/lgreene/gravix-dashboards/data"

echo "[$(date -u)] Starting cleanup (Retention: $RETENTION_DAYS days)"

# 1. Clean Raw JSONL
find "$DATA_DIR/raw" -name "*.jsonl" -type f -mtime +$RETENTION_DAYS -print -delete
echo "[$(date -u)] Cleaned raw JSONL files"

# 2. Clean Warehouse Parquet
find "$DATA_DIR/warehouse" -name "*.parquet" -type f -mtime +$RETENTION_DAYS -print -delete
echo "[$(date -u)] Cleaned warehouse Parquet files"

echo "[$(date -u)] Cleanup complete"
