---
title: Bare-Parquet access
sidebar_position: 3
---

# Read your warehouse with no Gravix running

Gravix writes plain Apache Parquet into `data/warehouse/`, Hive-partitioned by day. Reading it back
needs nothing from Gravix: no agent, no API key, no query language, no running process, no
Gravix-specific tool of any kind. Any engine that speaks Parquet will do — this page uses the stock
[DuckDB](https://duckdb.org) CLI because it is a single binary and a foreign project with no
knowledge of Gravix whatsoever.

This is not a design intention that might quietly rot. `tests/e2e/bare_parquet_test.go` runs the
query below on every commit against an unmodified DuckDB CLI, so the first change that made the
warehouse layout unreadable by a generic SQL engine would fail CI before it shipped.

## Install DuckDB

```bash
# The pinned build CI verifies against:
curl -fsSL -o duckdb.zip \
  https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip
unzip duckdb.zip && ./duckdb --version

# Or the official installer, for the current release:
curl -fsSL https://install.duckdb.org | sh
export PATH="$HOME/.duckdb/cli/latest:$PATH"
```

## The query CI verifies

Run from the directory containing `request_metrics_minute/`:

```sql
SELECT event_day, SUM(request_count) AS total_requests
FROM read_parquet('request_metrics_minute/event_day=*/part-0.parquet', hive_partitioning=true)
GROUP BY event_day ORDER BY event_day;
```

Against the two-day test fixture, that returns:

```csv
event_day,total_requests
2026-01-01,55
2026-01-02,55
```

`part-0.parquet` is the filename the test fixture writes. **A real Gravix warehouse names its files
differently**, so on your own data widen the glob to match any Parquet file in the partition:

```sql
SELECT event_day, SUM(request_count) AS total_requests
FROM read_parquet('request_metrics_minute/event_day=*/*.parquet', hive_partitioning=true)
GROUP BY event_day ORDER BY event_day;
```

A rollup writes `request_metrics_minute_<YYYYMMDD>.parquet` — one deterministic name per partition, so
a recompute replaces its output rather than adding a second file beside it. Compaction writes
`metrics_<uuid>_<YYYYMMDD>.parquet`. The `*.parquet` glob above matches both, and is the form to
prefer for anything you script.

## No Gravix process is required

The queries above read files off a disk. Nothing in Gravix is listening, and nothing needs to be:
ingestion, the gateway, Cube and Trino can all be stopped, uninstalled or never installed, and the
numbers come out the same. `TestBareParquetReadNoGravixProcessRequired` asserts exactly this — it
first proves nothing is listening on any of the four documented Gravix ports (8080, 8081, 8090,
8091), and only then runs the query. If a Gravix process were quietly required, that test would fail
rather than pass for the wrong reason. The DuckDB process is also launched with no environment beyond
`PATH`, `HOME` and `TMPDIR`, so no Gravix endpoint, API key or config file can be reached even by
accident.

This is what data ownership means here in practice. Your history is a directory of open-format files
you can copy, back up, move to another object store, or query with something else entirely, whether
or not Gravix is still running — and whether or not you are still a Gravix user.

## What is in each file

`request_metrics_minute/` — one row per minute bucket, per service, method and path template:

| Column | Type | Meaning |
|---|---|---|
| `tenant_id` | `VARCHAR` | Empty in single-tenant deployments |
| `bucket_start` | `VARCHAR` | RFC3339 start of the one-minute bucket |
| `service` | `VARCHAR` | Service name as reported by the caller |
| `method` | `VARCHAR` | HTTP method |
| `path_template` | `VARCHAR` | Templated path, e.g. `/orders/{id}` |
| `request_count` | `BIGINT` | Requests in the bucket |
| `error_count` | `BIGINT` | Requests with status ≥ 500 |
| `error_rate` | `DOUBLE` | `error_count / request_count` |
| `p50_latency_ms` | `DOUBLE` | Exact within this bucket |
| `p95_latency_ms` | `DOUBLE` | Exact within this bucket |
| `p99_latency_ms` | `DOUBLE` | Exact within this bucket |
| `latency_sketch` | `BLOB` | Mergeable quantile sketch over the bucket's latencies |
| `sketch_version` | `VARCHAR` | Sketch format version, so a reader can refuse one it cannot parse |
| `user_agent_family` | `VARCHAR` | Populated only where added as a dimension |
| `extra_quantile_label` | `VARCHAR` | A retroactively added percentile's label, e.g. `p99.9` |
| `extra_quantile_ms` | `DOUBLE` | That percentile's value |
| `event_day` | `DATE` | Partition day, derived from the path by `hive_partitioning=true`; stored inside the file as `VARCHAR` |

`service_events_daily/` — one row per day, service and event type: `tenant_id`, `event_day`,
`service`, `event_type`, `event_count`.

:::warning Percentiles across buckets

Do not average `p95_latency_ms` across rows. The mean of percentiles is not a percentile, and it can
be arbitrarily wrong. To get a correct p95 over a range, merge the `latency_sketch` values — that
column exists for precisely this reason. A single bucket's scalar percentile is exact and can be read
directly.

Note also that compacted partitions currently carry the eleven scalar columns and `event_day` only:
compaction drops `latency_sketch`, `sketch_version`, `user_agent_family`, `extra_quantile_label` and
`extra_quantile_ms`, so a correct cross-bucket percentile is not recoverable from a day that has been
compacted. This is a known defect, not a design decision.

:::

## Verifying this page yourself

```bash
go test ./tests/e2e/ -run TestBareParquet -v
go test ./tests/e2e/ -run TestDocContainsVerifiedQuery -v
```

`TestDocContainsVerifiedQuery` reads this file and compares the first query above, character for
character, with the one the test actually executes. The guide cannot drift into publishing SQL that
nobody ran.
