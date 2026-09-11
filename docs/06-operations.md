# Local Operations Runbook (Docker Compose)

> For a **Kubernetes production** deployment, see [`operations.md`](operations.md) instead.
> This runbook covers a local `docker-compose` stack.

This guide covers common operational tasks, maintenance procedures, and troubleshooting steps for running Gravix locally with Docker Compose.

> **Kubernetes operations**: For production Kubernetes operations, see [operations.md](operations.md).

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

The purge container runs automatically at 03:00 UTC daily, deleting raw and warehouse data older than 30 days.

To run manually:

```bash
make purge
# or
go run ./cmd/purge/ --retention-days 30 --data-dir ./data
```

### Manual Rollup (Backfill/Recovery)

If the rollup job fails or you need to re-process data for a specific time range:

```bash
# Process specific hour
go run transforms/request_metrics_minute/main.go \
  --start-time 2026-02-16T10:00:00Z \
  --end-time 2026-02-16T11:00:00Z
```

### Manual Events Detail Rollup

To re-process service events detail for a specific day or range:

```bash
go run transforms/service_events_detail/main.go \
  -input-dir ./data/raw/service_events \
  -output-dir ./data/warehouse/service_events_detail \
  -start-day 2026-03-01 -end-day 2026-03-15
```

### Batch Job Metrics

All batch jobs export Prometheus metrics on dedicated ports. These are scraped
by Prometheus and visualized in the **Gravix Batch Jobs** Grafana dashboard.

| Job | Container | Metrics Port | Key Metrics |
|-----|-----------|-------------|-------------|
| Request Rollup | `gravix-request-rollup` | 9090 | `rollup_processed_events_total`, `rollup_duration_seconds` |
| Purge | `gravix-purge` | 9091 | `purge_files_deleted_total`, `purge_errors_total` |
| Events Daily | `gravix-events-rollup` | 9092 | `events_daily_processed_total`, `events_daily_duration_seconds` |
| Events Detail | `gravix-events-detail-rollup` | 9093 | `events_detail_processed_total`, `events_detail_duration_seconds` |

To check metrics manually:

```bash
docker exec -it gravix-events-detail-rollup wget -qO- http://localhost:9093/metrics
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

If Trino fails to start or query tables, the metastore may be corrupted. Trino uses a writable metastore directory (`data/trino-metastore/`) separate from the S3 data.

**Fix**: Reset the Trino metastore and re-initialize tables.

```bash
docker-compose down
rm -rf data/trino-metastore
docker-compose up -d trino
# Wait for Trino to become healthy (~60-90s), then re-initialize tables:
docker-compose up -d init-trino
# Or manually:
make trino-init
```

### Generating Test Data

Use the demo script to simulate realistic traffic from an "Acme Retail" e-commerce company:

```bash
./scripts/demo.sh              # Full demo: boot + traffic + verify
./scripts/demo.sh --traffic    # Traffic only (services already running)
./scripts/demo.sh --teardown   # Stop and clean up
```

Or use the load generator directly:

```bash
go run ./cmd/load_generator/ --qps 10 --concurrency 2
```

### Ingestion "Unauthorized"

Ensure your client is sending the correct API Key.

- Header: `X-API-Key: <your-secret>`
- Env Var: Check `API_KEY` in your `.env` file.

### API Key Management (Multi-Tenant)

In multi-tenant mode, API keys are managed via the gateway. Keys can be created with an optional expiry:

```bash
# Create a key expiring in 90 days
curl -X POST http://localhost:8091/api/gateway/api-keys \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-service", "expires_in_days": 90}'

# List all keys
curl http://localhost:8091/api/gateway/api-keys \
  -H "Authorization: Bearer $JWT"

# Check keys expiring within 7 days
curl http://localhost:8091/api/gateway/api-keys/expiring \
  -H "Authorization: Bearer $JWT"

# Revoke a key
curl -X DELETE http://localhost:8091/api/gateway/api-keys/{key_id} \
  -H "Authorization: Bearer $JWT"
```

Expired keys are automatically rejected at ingestion time. The dashboard Settings page shows key status, expiry dates, and warnings for keys expiring within 7 days.

## 4. Disaster Recovery

### Ingestion Crash

The Ingestion Service is designed to be crash-safe.

1. Restart the service: `docker-compose restart ingestion`
2. It will automatically scan `data/buffer` for any orphaned files and upload them to `data/raw`.

### Data Corruption

Since raw data (JSONL) and warehouse data (Parquet) are separated:

- If **Parquet** is corrupted: Delete the files and re-run the Rollup Job from Raw data.
- If **Raw** is corrupted: Data for that period may be lost if not backed up externally.

## 5. Rebuilding metrics (`gravix recompute`)

Recomputation is not a manual procedure. `gravix recompute` rebuilds any historical
window from the raw facts and replaces the prior output in place.

```bash
# Rebuild one week
gravix recompute --from 2026-09-01 --to 2026-09-08

# See what would change, without writing
gravix recompute --from 2026-09-01 --to 2026-09-02 --dry-run

# Multi-tenant, four partitions at a time
gravix recompute --from 2026-09-01 --to 2026-09-08 --tenant acme --tenant globex --concurrency 4
```

| Flag | Default | Meaning |
|---|---|---|
| `--from` | *required* | Start of the window, RFC3339 or `YYYY-MM-DD` |
| `--to` | *required* | End of the window, **exclusive** |
| `--metric` | `request_metrics_minute` | Metric to rebuild |
| `--tenant` | *(none)* | Tenant id; repeatable. Omit for single-tenant mode |
| `--input` | `./data/raw` | Raw facts directory |
| `--output` | `./data/warehouse` | Warehouse output directory |
| `--dry-run` | `false` | Plan and report without writing |
| `--concurrency` | `1` | Partitions rebuilt in parallel |

Exit codes: `0` success, `1` a partition failed, `2` invalid flags, `3` the lock is
held by a running rollup.

### 5.1 What makes a rebuild safe

- **One partition, one key.** A tenant-day always writes to
  `warehouse/<metric>/event_day=YYYY-MM-DD/<metric>_YYYYMMDD.parquet`. The key is
  derived from the partition, never minted per run, so a rebuild **replaces** its
  prior output rather than adding a second file the query layer would double-count.
- **Byte-identical output.** The same facts always encode to the same bytes. Rows are
  sorted by the full aggregation key (bucket, service, method, path template), each
  bucket's latencies are sorted before its percentiles are taken, the compression
  level is pinned rather than inherited from the library default, and nothing
  run-scoped — no timestamp, hostname, or run id — is written into the file.
- **Unchanged partitions are left alone.** Before writing, the engine encodes the new
  file in memory and compares it with what is already stored. Identical bytes count as
  `unchanged` and no write happens, so re-running a window is a genuine no-op rather
  than a rewrite that merely lands on the same values.
- **Read order does not matter.** The object store gives no ordering guarantee. Fact
  files are read in a fixed order and every aggregation is order-independent, so two
  rebuilds of the same day agree bit for bit.
- **Facts are never touched.** Recompute reads facts and rewrites derivatives only,
  per `docs/00-system-truth.md` §2.
- **One writer at a time.** A recompute takes the same lock as the cron rollup, so the
  two can never write the same partition concurrently. A held lock exits `3` and
  writes nothing.

### 5.2 Why it matters

`docs/00-system-truth.md` §4 promises that every metric is recomputable. That promise
is only worth something if a rebuild is provably the same as the original — otherwise
"recomputable" means "will produce some other number, later". The determinism rules
above are what make the promise checkable, and `pkg/recompute` tests each of them by
name.

## 6. Adding a metric to history (`gravix evolve`)

Gravix kept the facts, so a metric that never existed can be computed for last month.

```bash
# Add p99.9 across the last 30 days. No fact read: the sketch already knows.
gravix evolve add-percentile --quantile 0.999 --from 2026-08-12 --to 2026-09-11

# Add a dimension. This does re-read facts — the rows were never separated.
gravix evolve add-dimension --field user_agent_family --from 2026-08-12 --to 2026-09-11

# See the plan without writing anything.
gravix evolve add-dimension --field user_agent_family --from 2026-08-12 --to 2026-09-11 --dry-run
```

Without `--yes` the command prints the plan and waits for you to type `yes`. Backfilling rewrites
every partition in the window; that should not be one keystroke away.

Exit codes: `0` success, `1` partial failure, `2` invalid flags or a refused change, `3` you declined.

### 6.1 Why the two kinds cost different things

| | Percentile | Dimension |
|---|---|---|
| Reads raw facts | **no** | **yes** |
| Why | each bucket stores a mergeable sketch; a quantile is a question it already answers | the dimension was never in the aggregation key, so the rows that would carry it were never separated |
| Rows after | same count | more — one per distinct value |

### 6.2 What is refused, and why

- **Unbounded dimensions.** `user_id`, `request_id`, `session_id`, `ip_address`, `trace_id`, `span_id`
  and `event_id` are permanently denied. The list is not configurable: a config option would turn a
  constraint into a suggestion, and an unbounded dimension admitted once is unrecoverable — the
  partitions are written by the time anyone sees the bill. See
  [`04-non-goals.md`](04-non-goals.md) §5.
- **Anything above 1,000 distinct values per day.** Sampling reads the first, middle and last day of
  the window in full. The refusal reports the observed count, so you learn why and not only that.
- **A percentile over partitions written before sketches existed.** Those files genuinely lack the
  information. The refusal names the days so you can rebuild them first.
- **A window reaching past fact retention.** Days whose facts are gone are reported and **not**
  backfilled. Partial success is reported as partial, never as success.

### 6.3 What it does not do

It never edits or deletes a fact. An evolution is a rebuild of derivatives, and every partition it
rewrites gets a revision bump and a manifest recording what it superseded, exactly as a late-arriving
fact would.
