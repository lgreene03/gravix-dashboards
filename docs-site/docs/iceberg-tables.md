---
title: Iceberg tables
sidebar_position: 4
---

# Query Gravix from Spark, or anything that reads Iceberg

Gravix's warehouse is also published as Apache Iceberg tables. Any engine that reads Iceberg can
query them directly — no Gravix process, no Gravix driver, no shared metastore, and no permission
from us.

This page is the companion to [bare-Parquet access](bare-parquet-access.md). That one shows the
files are readable; this one shows they are readable as *tables*, with schema, partitioning and
snapshot isolation, by a second engine.

## Why this exists, and what it is not

The Hive catalog Gravix has always shipped uses Trino's `hive.metastore=file`, which is Trino's own
embedded format. It works, it needs no metastore service, and **no other engine can read it.** That
is fine for Gravix's own dashboard and a dead end for anybody with a Spark cluster.

A `hadoop`-type Iceberg catalog needs no metastore service either, and is a published format. Every
table's location and its current metadata pointer live inside the warehouse path. Point any engine
at the same path with the same credentials and it sees the same tables.

The Hive tables are unchanged and remain the primary read path. Iceberg is an additional one.

## The catalog

`storage/trino/catalog/gravix_iceberg.properties`:

```properties
connector.name=iceberg
iceberg.catalog.type=hadoop
iceberg.catalog.warehouse=s3a://${S3_BUCKET}/iceberg-warehouse
iceberg.file-format=PARQUET
hive.s3.endpoint=http://minio:9000
hive.s3.aws-access-key=${S3_ACCESS_KEY}
hive.s3.aws-secret-key=${S3_SECRET_KEY}
hive.s3.path-style-access=true
hive.s3.ssl.enabled=false
```

Trino's entrypoint substitutes the three variables from the environment. Nothing else is needed — no
Hive Metastore, no Glue, no Nessie.

## The sync job

`iceberg-sync` copies the last N days from the Hive tables into the Iceberg ones. It issues nothing
but standard Trino SQL:

```sql
CREATE TABLE IF NOT EXISTS gravix_iceberg.raw.request_metrics_minute (…) WITH (partitioning = ARRAY['event_day'])
DELETE FROM gravix_iceberg.raw.request_metrics_minute WHERE event_day = '2026-09-16'
INSERT INTO gravix_iceberg.raw.request_metrics_minute SELECT … FROM gravix.raw.request_metrics_minute WHERE event_day = '2026-09-16'
```

Trino writes the Iceberg manifests and snapshots. Gravix writes none of them, which is the point:
the interoperability comes from a format two engines already implement.

```bash
./bin/iceberg-sync \
  -iceberg-dsn "http://gravix@localhost:8081?catalog=gravix_iceberg&schema=raw" \
  -days 2
```

| Flag | Default | What it does |
|---|---|---|
| `-iceberg-dsn` | `http://gravix@localhost:8081?catalog=gravix_iceberg&schema=raw` | Trino DSN for the destination catalog |
| `-days` | `2` | Trailing days to sync, including today (UTC). Must be ≥ 1 |

It is a one-shot process, not a daemon. `docker-compose` runs it every 300 seconds in the
`iceberg-sync` sidecar; in Kubernetes, use a `CronJob`.

**The `DELETE` is what makes a re-run safe.** Without it, syncing a day twice doubles its rows —
and every number stays plausible while being exactly twice what it should be. A five-minute cron
would be wrong within ten. `TestIcebergSyncIsIdempotent` exists for that one failure.

A single day's failure is logged and the run continues to the next; the process exits non-zero at
the end if anything failed. One bad partition should not cost the backfill behind it.

## Querying from Trino

```sql
SELECT service, sum(request_count) AS requests, max(p99_latency_ms) AS worst_p99
FROM gravix_iceberg.raw.request_metrics_minute
WHERE event_day = current_date - interval '1' day
GROUP BY service
ORDER BY requests DESC;
```

Two tables are published: `request_metrics_minute` and `service_events_daily`. Both are partitioned
by `event_day` and carry exactly the columns their Hive counterparts expose.

Raw facts are not published as Iceberg tables. They are JSONL, and they are yours already — see
[leaving Gravix](leaving-gravix.md).

## Querying from Spark

This is the part that matters, because Trino reading back what Trino wrote proves only that Trino
is self-consistent.

```bash
spark-sql \
  --packages org.apache.iceberg:iceberg-spark-runtime-3.5_2.12:1.6.1 \
  --conf spark.sql.catalog.gravix_iceberg=org.apache.iceberg.spark.SparkCatalog \
  --conf spark.sql.catalog.gravix_iceberg.type=hadoop \
  --conf spark.sql.catalog.gravix_iceberg.warehouse=s3a://gravix/iceberg-warehouse \
  --conf spark.hadoop.fs.s3a.endpoint=http://minio:9000 \
  --conf spark.hadoop.fs.s3a.access.key=$S3_ACCESS_KEY \
  --conf spark.hadoop.fs.s3a.secret.key=$S3_SECRET_KEY \
  --conf spark.hadoop.fs.s3a.path.style.access=true \
  -e "SELECT COUNT(*) FROM gravix_iceberg.raw.request_metrics_minute"
```

Nothing in that command mentions Gravix except the bucket name. Swap Spark for Flink, Dremio,
DuckDB's Iceberg extension or anything else that reads the format, and the same warehouse path
works.

```bash
./scripts/verify_spark_iceberg_read.sh
```

runs exactly that against a running stack, in an ephemeral container, and exits non-zero if the
query fails. Spark is deliberately **not** a permanent service in `docker-compose.yml`: it is a
verification, not a component.

## What is proven, and what is not

Honest scope, because this page is read by somebody deciding whether to depend on it:

| | |
|---|---|
| The SQL the job builds | **Unit-tested** — exact statements, explicit column lists, idempotent DELETE |
| Column lists match the Hive tables | **Tested** against `storage/trino/init.sql` |
| `-days 0` refuses before issuing SQL | **Tested** |
| Row counts survive the copy | Needs a running stack — `TestIcebergSyncPreservesRowCount` |
| Re-running does not double rows | Needs a running stack — `TestIcebergSyncIsIdempotent` |
| Spark can read the tables | Needs a running stack and Docker — `TestVerifySparkIcebergRead` |

The last three skip when no stack is reachable. They have not yet been run in an environment that
had one, so treat the Spark interoperability claim as **designed and not yet demonstrated** until
that test has passed somewhere you can see. `docs/oss/spec-defects.md` SD-050 records why.
