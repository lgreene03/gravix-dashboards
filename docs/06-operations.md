# Operations Runbook

This guide covers common operational tasks, maintenance procedures, and troubleshooting steps for the Gravix system.

## 1. System Management

### Starting the System

```bash
docker-compose up -d
```

### Stopping the System

```bash
docker-compose down
```

### Viewing Logs

```bash
# All services
docker-compose logs -f

# Specific service
docker-compose logs -f ingestion
docker-compose logs -f trino
```

## 2. Data Management

### Data Retention (Cleanup)

Run the cleanup script to delete raw and warehouse data older than 30 days.

```bash
./scripts/cleanup_data.sh
```

*Recommendation: Add this to a daily cron job.*

### Manual Rollup (Backfill/Recovery)

If the rollup job fails or you need to re-process data for a specific time range:

```bash
# Process specific hour
go run transforms/request_metrics_minute/main.go \
  --start-time 2026-02-16T10:00:00Z \
  --end-time 2026-02-16T11:00:00Z
```

## 3. Troubleshooting

### Dashboard Showing "No Data"

1. **Check Ingestion**: Is `gravix-ingestion` running? Are files appearing in `data/raw`?

   ```bash
   ls -R data/raw
   ```

2. **Check Rollup**: Has the rollup job run? Are parquet files in `data/warehouse`?

   ```bash
   ls -R data/warehouse
   ```

3. **Check Trino**: Can Trino see the tables?

   ```bash
   docker exec -it gravix-trino trino --execute "SELECT * FROM gravix.raw.request_metrics_minute LIMIT 10"
   ```

4. **Check Cube**: Is the API returning errors? Check browser console or `gravix-cube` logs.

### Trino "Hive Metastore" Errors

If Trino fails to start or query tables, the metastore may be corrupted.
**Fix**: Reset the Trino environment.

```bash
docker-compose down
rm -rf storage/trino/data  # (If mapped)
docker-compose up -d trino
```

### Ingestion "Unauthorized"

Ensure your client is sending the correct API Key.

- Header: `X-API-Key: <your-secret>`
- Env Var: Check `API_KEY` in `docker-compose.yml`.

## 4. Disaster Recovery

### Ingestion Crash

The Ingestion Service is designed to be crash-safe.

1. Restart the service: `docker-compose restart ingestion`
2. It will automatically scan `data/buffer` for any orphaned files and upload them to `data/raw`.

### Data Corruption

Since raw data (JSONL) and warehouse data (Parquet) are separated:

- If **Parquet** is corrupted: Delete the files and re-run the Rollup Job from Raw data.
- If **Raw** is corrupted: Data for that period may be lost if not backed up externally.
