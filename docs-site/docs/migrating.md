---
title: Migrating history
sidebar_position: 4
---

# Arriving with history

A new observability tool that shows you the last four minutes is not much use for the first month.
`gravix import` converts a Datadog metric export into Gravix storage so the first dashboard shows
months instead.

It will also tell you, plainly, what it cannot do. That is the more important half of this page.

## What Gravix will not do

**It will not invent facts.** Gravix stores raw request events and derives metrics from them; that is
what makes a metric recomputable and a percentile correct. Datadog and Prometheus store the other
thing: series that were already aggregated before you exported them. A Datadog counter reading 4,201
requests is one number, not 4,201 records.

An importer *could* turn that number into 4,201 rows with invented event IDs, invented latencies and
invented timestamps. The dashboard would look full and every correctness claim Gravix makes would
become false, silently, for the imported range. So it doesn't.

## The two modes

| Mode | For | Produces |
|---|---|---|
| `--mode metrics` | Pre-aggregated series — nearly everything | Warehouse partitions marked as imported |
| `--mode facts` | Genuinely per-request sources | Real `RequestFact` rows |

`--mode facts` over an aggregated source is refused outright:

```
importer: datadog holds aggregates; facts mode would fabricate data that never existed. Use --mode metrics.
```

Datadog declares the aggregation itself, in the export's `aggr` and `interval` fields, and either one
alone is enough to trigger the refusal. When a source is ambiguous, Gravix treats it as derived.

## What an imported partition cannot do

Every `--mode metrics` import prints this before it writes anything, and again in each partition's
manifest:

```
Imported partitions hold derived metrics, not facts. Gravix did not receive the
original requests and will not pretend it did.

  - gravix recompute cannot rebuild these partitions.
  - gravix explain reports import provenance, not source facts.
  - Adding a percentile or dimension retroactively does not apply to them.
  - Percentiles carry the source system's accuracy, not Gravix's sketch bound.

Data ingested natively after the import has none of these limitations. The two
are distinguishable in every partition manifest.
```

These are the honest price of importing aggregates. Hiding them would make every Gravix guarantee
unreliable for anyone who migrated, which is worse than the gap they fill.

## Telling the two apart

Every partition manifest carries a `provenance` field: `imported:datadog`, or absent for a partition
built from facts Gravix received itself. Nothing else about an imported partition is special — it
sits in the same `warehouse/` layout, it is read by the same queries, and it appears on the same
dashboard.

```bash
duckdb -c "SELECT * FROM read_parquet('warehouse/request_metrics_minute/event_day=*/*.parquet') LIMIT 5;"
```

To check a partition's origin, read the manifest beside it:

```bash
cat data/warehouse/request_metrics_minute/event_day=2026-01-01/*.manifest.json | grep provenance
```

## Running an import

```bash
gravix import datadog \
  --input datadog_export.json \
  --service-map checkout-prod=checkout,payments-prod=payments \
  --path-label resource \
  --dry-run
```

Start with `--dry-run`. It reads the export, reports what it would write and why it would skip
anything, and writes nothing. Without `--yes`, a real run prints the same report and then asks you to
type `yes` — an import writes into the warehouse and is not trivially reversible.

| Flag | Meaning |
|---|---|
| `--input` | Path to the export file |
| `--mode` | `metrics` (default) or `facts` |
| `--service-map` | `source=service` pairs; a series whose labels match none is skipped, never guessed |
| `--path-label` | Which source label carries the path template (default `resource`) |
| `--from`, `--to` | Restrict the sample range |
| `--tenant` | Tenant id; omit for single-tenant |
| `--dry-run` | Report only |
| `--yes` | Skip the confirmation prompt |

### Series that are skipped

- **No service mapping.** Gravix will not guess a service name, because a guessed name puts rows on a
  dashboard under a label nobody chose. Add the mapping and re-run.
- **Label cardinality above 1,000/day.** The same bound every other ingest path enforces. An import
  is not a way around it, and the count of rejected series appears in the report.

Skipped series are counted and reported; they never fail the whole import.

## Prometheus

Gravix reads the text that `promtool tsdb dump` writes, not the TSDB directory itself. `promtool`
ships with every Prometheus release, so you already have it:

```bash
promtool tsdb dump /path/to/prometheus/data > prometheus-dump.txt
gravix import prometheus --input prometheus-dump.txt \
  --service-map checkout-prod=checkout --path-label handler --dry-run
```

Reading the block format directly would mean either reimplementing Prometheus's index and chunk
encoding, or depending on `prometheus/prometheus`, which brings 296 modules into a core package. The
dump is a stable text format, and the importer still reads only a file. It never contacts a running
Prometheus.

What the importer does with it:

- **Only counters are imported**, meaning series whose names end in `_total` or `_count`. Each
  becomes per-minute request counts: the counter's increase in each minute, with a reset treated as
  starting from zero. A raw counter value is a running total and would put a meaningless number in
  every row.
- **Everything else is skipped and counted**, under its own reason in the report. That covers
  gauges, native histogram samples, and series with no metric name.
- **Facts mode is always refused.** Every Prometheus series is an aggregate, so there are no
  per-request records to import.
- Map only request counters to services. A counter of bytes or of errors would be read as requests.
