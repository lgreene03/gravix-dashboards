---
title: SQL for PromQL users
sidebar_position: 7
---

# The same questions, in SQL

If you already think in PromQL, this page is the translation. Each row is a question you currently
ask Prometheus, and the SQL that asks it of Gravix's warehouse through Trino.

Gravix has no query language of its own and is not getting one
([`docs/04-non-goals.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/04-non-goals.md)
§6). The right-hand column is standard SQL that Trino executes unmodified — there is nothing
Gravix-shaped to learn.

## Two things about the columns, before the table

**`bucket_start` is a `VARCHAR`, not a timestamp.** It is formatted `YYYY-MM-DD HH:MM:SS`, so a range
filter compares against a string in exactly that shape. String comparison happens to sort correctly
for this format, which is why it works at all.

**It is UTC.** `current_timestamp` in Trino carries your session's time zone, so comparing it to
`bucket_start` without converting compares two different clocks and silently returns the wrong
window. Every example below converts with `AT TIME ZONE 'UTC'`. If you drop that, the queries still
run — they are just wrong by your UTC offset, which is the worst kind of broken.

**`event_day` is also a `VARCHAR`** (`YYYY-MM-DD`). `WHERE event_day = current_date` compares a
varchar to a date and Trino rejects it; cast explicitly, as below.

## The table

| PromQL intent | PromQL | Gravix SQL |
|---|---|---|
| Request rate | `rate(http_requests_total[5m])` | <pre>SELECT service, SUM(request_count) / 300.0 AS rps<br/>FROM gravix.raw.request_metrics_minute<br/>WHERE event_day = CAST(current_date AS varchar)<br/>  AND bucket_start >= format_datetime(<br/>        (current_timestamp - INTERVAL '5' MINUTE) AT TIME ZONE 'UTC',<br/>        'yyyy-MM-dd HH:mm:ss')<br/>GROUP BY service</pre> |
| p95 latency | `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))` | <pre>SELECT service, p95_latency_ms FROM (<br/>  SELECT service, p95_latency_ms,<br/>         ROW_NUMBER() OVER (PARTITION BY service<br/>                            ORDER BY bucket_start DESC) AS rn<br/>  FROM gravix.raw.request_metrics_minute<br/>  WHERE event_day = CAST(current_date AS varchar)<br/>) WHERE rn = 1</pre> |
| Error ratio | `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))` | <pre>SELECT service,<br/>       CAST(SUM(error_count) AS double)<br/>         / NULLIF(SUM(request_count), 0) AS error_ratio<br/>FROM gravix.raw.request_metrics_minute<br/>WHERE event_day = CAST(current_date AS varchar)<br/>  AND bucket_start >= format_datetime(<br/>        (current_timestamp - INTERVAL '5' MINUTE) AT TIME ZONE 'UTC',<br/>        'yyyy-MM-dd HH:mm:ss')<br/>GROUP BY service</pre> |
| Per-endpoint breakdown | `sum by (path) (rate(http_requests_total[5m]))` | <pre>SELECT path_template, SUM(request_count) / 300.0 AS rps<br/>FROM gravix.raw.request_metrics_minute<br/>WHERE event_day = CAST(current_date AS varchar)<br/>  AND bucket_start >= format_datetime(<br/>        (current_timestamp - INTERVAL '5' MINUTE) AT TIME ZONE 'UTC',<br/>        'yyyy-MM-dd HH:mm:ss')<br/>GROUP BY path_template</pre> |
| Raw request inspection | *not expressible — Prometheus discards the raw observation* | <pre>SELECT * FROM gravix.raw.request_facts<br/>WHERE service = 'checkout' LIMIT 100</pre> |

## Three places the translation is not literal

**A `[5m]` window is a filter, and forgetting it is not a small error.** Every rate above filters
`bucket_start` to the last five minutes and then divides by 300 seconds. Sum a whole day and divide
by 300 and you do not get a rate — you get the day's total inflated by however many minutes have
elapsed over five. At midday that is 144×. It is the same number shape, in the same units, and
wrong, which is why it is worth saying rather than assuming.

**An error ratio is a ratio of sums, not an average of ratios.** The table divides
`SUM(error_count)` by `SUM(request_count)`. Averaging the per-minute `error_rate` column instead
gives every minute equal weight regardless of its traffic, so one quiet minute with one failed
request out of two counts as much as a busy minute with a thousand successes. Those are different
numbers whenever traffic is uneven, which is always.

**`p95_latency_ms` is the percentile of one minute.** The query returns the most recent minute's
value, because that is a real percentile. There is no correct way to combine per-minute percentiles
into a five-minute one by arithmetic — averaging them can be
[62% off on a long-tailed distribution](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/02-derived-metrics.md),
which is what mergeable sketches exist to prevent. For a percentile over a wider window, query the
sketch column rather than averaging this one.

## What is proven about this page

| | |
|---|---|
| All five intents are present with a SQL equivalent | **Tested** — `TestSQLPromQLGuideHasAllRows` |
| No query compares `event_day` to a bare `current_date` | **Tested** — the type error that made the original draft unrunnable |
| Every rate filters `bucket_start` to its window | **Tested** — guards against the 144× error above |
| The queries return correct numbers against a live Trino | **Not run** — needs a running warehouse |

The last row is the honest one. These queries are reasoned from the column types in
`storage/trino/init.sql` and the project's own working SQL, not executed against a live Trino in CI.
`docs/oss/spec-defects.md` SD-052 records what was wrong with the first draft and how it was found.
