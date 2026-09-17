<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: BUSL-1.1 -->

# `ee/warehouse` — what this adds over the free export

```
You can already do this by hand.

gravix export writes Parquet, CSV or JSONL for any range, free, with no cap, and
your warehouse can load those files on a schedule you write yourself. That path
is not going away and is not being made worse.

What this package adds is the part that is tedious rather than the part that is
possible: tracking what has already been loaded, creating and evolving the target
schema, and — the genuinely hard part — reconciling partitions that Gravix
revised after a late fact arrived, so your warehouse never quietly disagrees
with your dashboard.
```

Naming the free alternative in the paid feature's own README is the charter working as intended.

## The genuinely hard part

Gravix **revises** partitions. When late facts arrive for a bucket that has already been rolled up,
the rollup recomputes it and the partition's content digest changes (GRVX-805). A sync that only
appended would leave a warehouse permanently disagreeing with a dashboard about a historical day,
and nobody would find out until somebody compared them.

So a revised partition is **replaced**, never updated:

1. Load the new rows into a staging table named for the partition's idempotency key.
2. In one transaction: delete every row for that tenant and `event_day`, insert the staged rows,
   update the sync state.
3. Drop the staging table.
4. Record the old digest, the new digest and the row delta in the sync report.

Never an `UPDATE` of individual rows. A revision can change a partition's row count, not just its
values, so there is no row-for-row correspondence to update — a warehouse that had been `UPDATE`d
would hold the old row count with new values, which is the worst of both.

If a target cannot do steps 1–3 in one transaction, it says so at registration and the sync
**refuses to run against it**. A non-atomic delete-then-insert means a customer's dashboard shows a
missing day at some point, which is worse than no sync at all.

## The adapters

| Target | Atomic replace | Mechanism |
|---|---|---|
| Snowflake | yes | `BEGIN` … `DELETE` … `INSERT` … `COMMIT` |
| BigQuery | yes | `BEGIN TRANSACTION` … `DELETE` … `INSERT` … `COMMIT` |
| Databricks | yes | one `INSERT INTO … REPLACE WHERE …` statement |

Each adapter's claim is a claim about the statements it generates, which are built and tested in
`ee/warehouse/targets` without any connection, so a reviewer can read exactly what would be sent to
a customer's warehouse.

## Schema evolution, and one deliberate asymmetry

| Change | Handling |
|---|---|
| Gravix added a column | `ALTER TABLE ADD COLUMN`, nullable, backfilled null — an existing row genuinely has no value for a column that did not exist when it was written |
| A column's type widened | Altered where the target supports it; otherwise that column is refused, the rest continue, and the report says so |
| Gravix removed a column | **Retained**, nullable, forever |
| The metric version bumped | New table `<table>_v<n>`; the old table is kept and no longer written |

Never dropping a column is the asymmetry, and it is deliberate. Gravix removing a column is a
Gravix decision; destroying a report a customer built on it is not ours to make, and a dropped
column cannot be put back with the data that was in it.

## It never writes to a table it did not create

Every table this package creates carries the comment `created-by=gravix-warehouse-sync`. A table
without that marker belongs to somebody else and is refused, by name, before anything is written to
it.

## It cannot hurt anything

A warehouse outage, a bad credential or a schema conflict produces **no** effect on ingestion,
rollup, alerting, dashboards or the free export. Sync reads stored partitions and writes to
somebody else's system; there is no path back. `TestWarehouseOutageDoesNotAffectCore` runs the
whole core pipeline against a target that fails every call and compares the bytes.

One unreachable day does not stop the other twenty-seven either: failures are recorded per
partition in the sync report and the run continues.

## When the licence lapses

Changing *which* warehouse you sync to is configuration, and stops. A sync that is already
scheduled keeps running (GRVX-1303 §5.2): stopping it mid-month because a card expired would put a
hole in a customer's warehouse that renewing does not fill.
