---
title: Iceberg tables
sidebar_position: 4
---

# Query Gravix from Spark, or anything that reads Iceberg

Gravix's warehouse is also published as Apache Iceberg tables. Any engine that reads Iceberg can
read them directly — no Gravix process, no Gravix driver, no metastore service, and no permission
from us.

This page is the companion to [bare-Parquet access](bare-parquet-access.md). That one shows the
files are readable; this one shows they are readable as *tables*, with schema, partitioning and
snapshot isolation, by a second engine.

## Why this exists, and what it is not

The Hive tables Gravix has always shipped are plain Parquet directories that Trino describes in its
own metastore. Another engine can read the files, but it has to be told their schema and
partitioning, and nothing stops it reading a directory while a rollup is rewriting it.

An Iceberg table carries all of that itself. Its `metadata/` directory holds a `metadata.json` for
every version Trino has written, and each one names the table's schema, partitioning and exact set of
data files. Any Iceberg reader, given the newest one and the S3 credentials, reads a consistent
snapshot of the table.

The Hive tables are unchanged and remain the primary read path. Iceberg is an additional one.

## The catalog

`storage/trino/catalog/gravix_iceberg.properties`:

```properties
connector.name=iceberg
iceberg.catalog.type=TESTING_FILE_METASTORE
hive.metastore.catalog.dir=s3a://${S3_BUCKET}/iceberg-warehouse
iceberg.unique-table-location=false
iceberg.file-format=PARQUET
hive.s3.endpoint=http://minio:9000
hive.s3.aws-access-key=${S3_ACCESS_KEY}
hive.s3.aws-secret-key=${S3_SECRET_KEY}
hive.s3.path-style-access=true
hive.s3.ssl.enabled=false
```

Trino's entrypoint substitutes the three variables from the environment. Nothing else is needed — no
Hive Metastore, no Glue, no Nessie.

The catalog uses Trino's file metastore, the same one the Hive catalog uses, with its directory in the
bucket beside the tables. Trino names it `TESTING_FILE_METASTORE`; it is what
`hive.metastore=file` selects for the primary catalog too. With `iceberg.unique-table-location=false`,
each table lives at a fixed path:

```
s3a://<bucket>/iceberg-warehouse/raw/request_metrics_minute/
s3a://<bucket>/iceberg-warehouse/raw/service_events_daily/
```

Other engines do not read Trino's metastore, so they cannot list these tables by name. They read
each one from its path, as below. Iceberg's `hadoop` catalog type would have let them list the tables,
but Trino has no such type, and Trino 435 refuses to start with it.

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

Spark needs no catalog. Give it the table's newest metadata file:

```bash
spark-shell \
  --packages org.apache.iceberg:iceberg-spark-runtime-3.5_2.12:1.6.1,org.apache.hadoop:hadoop-aws:3.3.4 \
  --conf spark.hadoop.fs.s3a.endpoint=http://minio:9000 \
  --conf spark.hadoop.fs.s3a.access.key=$S3_ACCESS_KEY \
  --conf spark.hadoop.fs.s3a.secret.key=$S3_SECRET_KEY \
  --conf spark.hadoop.fs.s3a.path.style.access=true
```

```scala
import org.apache.hadoop.fs.Path
val table = "s3a://gravix/iceberg-warehouse/raw/request_metrics_minute"
val fs = new Path(table).getFileSystem(spark.sparkContext.hadoopConfiguration)
// Each metadata file is named <version>-<uuid>.metadata.json. The highest version is the table now.
val versions = fs.listStatus(new Path(table + "/metadata")).map(_.getPath).filter(_.getName.endsWith(".metadata.json"))
val newest = versions.maxBy(_.getName.takeWhile(_ != '-').toLong).toString
spark.read.format("iceberg").load(newest).count()
```

That reads the table as it was when that version was written. To query it by name, register the same
metadata file in an Iceberg catalog of your own, with the `register_table` procedure where your
catalog supports it. A table registered that way does not follow Gravix's later versions until it is
registered again. This repository verifies the read above, not registration.

Nothing in those commands mentions Gravix except the bucket name. Other Iceberg readers, such as
Flink or DuckDB's Iceberg extension, also open a table from its metadata file. Spark is the one this
repository verifies.

```bash
./scripts/verify_spark_iceberg_read.sh
```

runs that read against a running stack, in an ephemeral container, and exits non-zero if it fails.
Spark is deliberately **not** a permanent service in `docker-compose.yml`: it is a verification,
not a component.

## What is proven, and what is not

Honest scope, because this page is read by somebody deciding whether to depend on it:

| | |
|---|---|
| The SQL the job builds | **Unit-tested** — exact statements, explicit column lists, idempotent DELETE |
| Column lists match the Hive tables | **Tested** against `storage/trino/init.sql` |
| `-days 0` refuses before issuing SQL | **Tested** |
| Row counts survive the copy | **Tested in CI** — `TestIcebergSyncPreservesRowCount`, on the full stack |
| Re-running does not double rows | **Tested in CI** — `TestIcebergSyncIsIdempotent`, on the full stack |
| Spark can read the tables | **Tested in CI** — `TestVerifySparkIcebergRead`, on the full stack |

The last three run in `docker-smoke` on every pull request, against the whole stack, and fail rather
than skip there. They first passed on 2026-10-01 ([run](https://github.com/lgreene03/gravix-dashboards/actions/runs/36934275599)): the
sync copied three rows, a second sync left three, and Spark, given only the table's path and the S3
credentials, read the same three.

Those three rows are fixtures CI writes into the Hive table first. On the full stack nothing else
writes there yet, because the rollup files each tenant's metrics in a directory the Hive tables
cannot name (`docs/oss/findings.md` F-070). Until that is fixed, the Iceberg tables on a full stack
are copies of empty tables. What CI proves is the path from Hive to Iceberg to Spark, not that your
traffic reaches it.
