<!-- GENERATED FROM contracts/*.yaml BY 'make contracts' — DO NOT EDIT -->

# Derived Metrics

Every metric Gravix reports has a published contract: its formula, the facts and
fields it consumes, its grain, how exact it is, how it may be aggregated, what
happens to late data, and the command that rebuilds it.

The contract is the promise. Changing a shipped metric's meaning requires a new
version rather than an edit in place, because someone who stored last month's
numbers must still be able to find out what they meant.

Two fields are worth reading before any of the definitions:

- **Exactness** is `exact` (computed from raw facts), `sketch` (a bounded-error
  summary, with the bound stated), or `approximate` (an error that is **not**
  bounded). An `approximate` metric is a defect this project owes a fix on, not a
  design choice, and every one of them is listed under [Known defects](#known-defects).
- **Mergeability** says whether two grains may be combined at all. `none` means
  any query that aggregates the metric across grains is reporting a number with no
  defined meaning.

## Metrics

### `request_count` — Requests

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | COUNT(*) over RequestFacts in the bucket |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `exact` |
| **Error bound** | none; this is a count of facts |
| **Mergeability** | `sum` |

**Aggregating it.** Counts add. Summing the per-minute counts over any window gives the exact count for that window.

**Late data.** A late fact is appended to the fact stream with its original event_time. The bucket it belongs to is rebuilt from scratch on the next rollup or recompute, so the count corrects itself rather than drifting.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `error_count` — Errors

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | COUNT(*) over RequestFacts in the bucket WHERE status_code >= 500 |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `status_code` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `exact` |
| **Error bound** | none; this is a count of facts |
| **Mergeability** | `sum` |

**Aggregating it.** Counts add. Note the threshold: a 4xx is a client error and is deliberately not counted here, because it is usually the caller's fault and not a signal about this service's health.

**Late data.** Same as request_count: the bucket is rebuilt from facts, so a late error is counted once the bucket is next computed.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `error_rate` — Error rate

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | error_count / request_count within the bucket; 0 when request_count is 0 |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `status_code` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `exact` |
| **Error bound** | none within a bucket; exact across buckets when merged as specified |
| **Mergeability** | `weighted_mean` |

**Aggregating it.** MUST be recomputed across buckets as sum(error_count) / sum(request_count). NEVER average the per-bucket rates. Averaging gives every bucket equal weight regardless of traffic, so one idle minute with a single failed request counts as much as a busy minute with ten thousand successes — which can turn a healthy hour into an apparent outage, or hide a real one.

**Late data.** Both numerator and denominator are recomputed together from facts, so the rate is always internally consistent; it is never a new numerator over a stale denominator.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p50` — Median latency (p50)

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | 50th percentile of latency_ms over RequestFacts in the bucket |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `approximate` |
| **Error bound** | UNBOUNDED across buckets; exact within one bucket |
| **Mergeability** | `none` |
| **Deprecated** | yes |

**Aggregating it.** Do not aggregate across buckets. There is no correct way to combine per-bucket percentiles without the underlying distribution, which this metric does not store.

**Late data.** The bucket is recomputed from all of its facts, so a late fact shifts the percentile to its correct value rather than being folded into a stale one.

> **Known defect.**
>
> Exact within a single one-minute bucket, computed from raw latencies.
> Across buckets the Cube model currently exposes MAX of the per-minute values
> (cube/model/schema/RequestMetricsMinute.js), which is not the percentile of the
> combined window and has an unbounded error. Do not aggregate this metric across
> buckets. Fixed by GRVX-804, which stores a mergeable t-digest sketch.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p95` — 95th percentile latency (p95)

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | 95th percentile of latency_ms over RequestFacts in the bucket |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `approximate` |
| **Error bound** | UNBOUNDED across buckets; exact within one bucket |
| **Mergeability** | `none` |
| **Deprecated** | yes |

**Aggregating it.** Do not aggregate across buckets. There is no correct way to combine per-bucket percentiles without the underlying distribution, which this metric does not store.

**Late data.** The bucket is recomputed from all of its facts, so a late fact shifts the percentile to its correct value rather than being folded into a stale one.

> **Known defect.**
>
> Exact within a single one-minute bucket, computed from raw latencies.
> Across buckets the Cube model currently exposes MAX of the per-minute values
> (cube/model/schema/RequestMetricsMinute.js), which is not the percentile of the
> combined window and has an unbounded error. Do not aggregate this metric across
> buckets. Fixed by GRVX-804, which stores a mergeable t-digest sketch.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p99` — 99th percentile latency (p99)

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | 99th percentile of latency_ms over RequestFacts in the bucket |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `approximate` |
| **Error bound** | UNBOUNDED across buckets; exact within one bucket |
| **Mergeability** | `none` |
| **Deprecated** | yes |

**Aggregating it.** Do not aggregate across buckets. There is no correct way to combine per-bucket percentiles without the underlying distribution, which this metric does not store. At p99 a one-minute bucket may hold too few requests for the figure to mean much on its own — a bucket with fewer than 100 requests has no 99th percentile in any useful sense.

**Late data.** The bucket is recomputed from all of its facts, so a late fact shifts the percentile to its correct value rather than being folded into a stale one.

> **Known defect.**
>
> Exact within a single one-minute bucket, computed from raw latencies.
> Across buckets the Cube model currently exposes MAX of the per-minute values
> (cube/model/schema/RequestMetricsMinute.js), which is not the percentile of the
> combined window and has an unbounded error. Do not aggregate this metric across
> buckets. Fixed by GRVX-804, which stores a mergeable t-digest sketch.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p50` — Median latency (p50)

| Field | Value |
|---|---|
| **Version** | `v2` |
| **Formula** | 50th percentile of latency_ms, from the merged latency_sketch over the queried window |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `sketch` |
| **Error bound** | RANK error <= 1% at q in [0.5, 0.99]: the value returned sits within 1% of the requested quantile's true rank. That is the guarantee a t-digest makes, and it holds regardless of distribution or window size — measured worst 0.09% over 24,000 observations end to end (TestMergeabilityEndToEnd). VALUE error is not bounded by it and depends on how many observations the window holds and how heavy the tail is, because one observation of rank error at q=0.99 can be an order of magnitude in value, and integer-millisecond latencies make a sub-millisecond difference read as several percent at a small median. Measured worst relative value error across uniform, normal, lognormal, bimodal and pareto (TestSketchErrorIsAFunctionOfSampleSize): n=100 -> 157%, n=1,000 -> 17%, n=10,000 -> 5.8%, n=100,000 -> 0.8%. Below roughly 10,000 observations in the queried window, treat the value as indicative rather than accurate. This supersedes an earlier claim of a flat "relative error <= 1%", which was measured only at 1e6 observations and does not hold at realistic bucket sizes. See CD-001. |
| **Mergeability** | `sketch_merge` |
| **Supersedes** | `latency_p50@v1` |

**Aggregating it.** Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles — that is what v1 did and it is why v1 is deprecated. Merging is exact: it is a multiset union of centroids, so any grouping or ordering of the buckets gives the same answer.

**Late data.** The bucket is recomputed from all of its facts, sketch included, so a late fact shifts the quantile to its correct value rather than being folded into a stale summary.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p95` — 95th percentile latency (p95)

| Field | Value |
|---|---|
| **Version** | `v2` |
| **Formula** | 95th percentile of latency_ms, from the merged latency_sketch over the queried window |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `sketch` |
| **Error bound** | RANK error <= 1% at q in [0.5, 0.99]: the value returned sits within 1% of the requested quantile's true rank. That is the guarantee a t-digest makes, and it holds regardless of distribution or window size — measured worst 0.09% over 24,000 observations end to end (TestMergeabilityEndToEnd). VALUE error is not bounded by it and depends on how many observations the window holds and how heavy the tail is, because one observation of rank error at q=0.99 can be an order of magnitude in value, and integer-millisecond latencies make a sub-millisecond difference read as several percent at a small median. Measured worst relative value error across uniform, normal, lognormal, bimodal and pareto (TestSketchErrorIsAFunctionOfSampleSize): n=100 -> 157%, n=1,000 -> 17%, n=10,000 -> 5.8%, n=100,000 -> 0.8%. Below roughly 10,000 observations in the queried window, treat the value as indicative rather than accurate. This supersedes an earlier claim of a flat "relative error <= 1%", which was measured only at 1e6 observations and does not hold at realistic bucket sizes. See CD-001. |
| **Mergeability** | `sketch_merge` |
| **Supersedes** | `latency_p95@v1` |

**Aggregating it.** Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles — that is what v1 did and it is why v1 is deprecated. Merging is exact: it is a multiset union of centroids, so any grouping or ordering of the buckets gives the same answer.

**Late data.** The bucket is recomputed from all of its facts, sketch included, so a late fact shifts the quantile to its correct value rather than being folded into a stale summary.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `latency_p99` — 99th percentile latency (p99)

| Field | Value |
|---|---|
| **Version** | `v2` |
| **Formula** | 99th percentile of latency_ms, from the merged latency_sketch over the queried window |
| **Grain** | 1 minute, per service/method/path_template |
| **Input facts** | `RequestFact` |
| **Input fields** | `event_time`, `service`, `method`, `path_template`, `latency_ms` |
| **Dimensions** | `tenant_id`, `service`, `method`, `path_template` |
| **Exactness** | `sketch` |
| **Error bound** | RANK error <= 1% at q in [0.5, 0.99]: the value returned sits within 1% of the requested quantile's true rank. That is the guarantee a t-digest makes, and it holds regardless of distribution or window size — measured worst 0.09% over 24,000 observations end to end (TestMergeabilityEndToEnd). VALUE error is not bounded by it and depends on how many observations the window holds and how heavy the tail is, because one observation of rank error at q=0.99 can be an order of magnitude in value, and integer-millisecond latencies make a sub-millisecond difference read as several percent at a small median. Measured worst relative value error across uniform, normal, lognormal, bimodal and pareto (TestSketchErrorIsAFunctionOfSampleSize): n=100 -> 157%, n=1,000 -> 17%, n=10,000 -> 5.8%, n=100,000 -> 0.8%. Below roughly 10,000 observations in the queried window, treat the value as indicative rather than accurate. This supersedes an earlier claim of a flat "relative error <= 1%", which was measured only at 1e6 observations and does not hold at realistic bucket sizes. See CD-001. |
| **Mergeability** | `sketch_merge` |
| **Supersedes** | `latency_p99@v1` |

**Aggregating it.** Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles — that is what v1 did and it is why v1 is deprecated. Merging is exact: it is a multiset union of centroids, so any grouping or ordering of the buckets gives the same answer.

**Late data.** The bucket is recomputed from all of its facts, sketch included, so a late fact shifts the quantile to its correct value rather than being folded into a stale summary.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

### `service_event_count` — Service events

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | count of ServiceEvent facts |
| **Grain** | one row per event, per service/event_type |
| **Input facts** | `ServiceEvent` |
| **Input fields** | `event_time`, `service`, `event_type` |
| **Dimensions** | `tenant_id`, `service`, `event_type` |
| **Exactness** | `exact` |
| **Error bound** | none — this is a count of stored facts, not an estimate |
| **Mergeability** | `sum` |

**Aggregating it.** Counts of disjoint sets add. Summing this measure across services, event types or any time range gives the same answer as counting the underlying facts over that range, because every fact is counted once and belongs to exactly one group.

**Late data.** The detail table is rebuilt from all facts for the day, so an event that arrives late appears at its event_time rather than at its arrival time.

**Rebuild it.**

```bash
not rebuildable by the gravix CLI yet; run: go run ./transforms/service_events_detail/
```

### `service_event_count_daily` — Service events per day

| Field | Value |
|---|---|
| **Version** | `v1` |
| **Formula** | count of ServiceEvent facts, grouped by day |
| **Grain** | 1 day, per service/event_type |
| **Input facts** | `ServiceEvent` |
| **Input fields** | `event_time`, `service`, `event_type` |
| **Dimensions** | `tenant_id`, `service`, `event_type` |
| **Exactness** | `exact` |
| **Error bound** | none — this is a count of stored facts, not an estimate |
| **Mergeability** | `sum` |

**Aggregating it.** Summing across days, services or event types is exact. Note that this is the SUM of the event_count column, never the row count: the daily table holds one row per service/event_type/day, so counting its rows counts groups rather than events. That is the same trap the Cube model's built-in `count` measure sets, which is why that one is hidden.

**Late data.** The day is rebuilt from all of its facts, so a late event is counted in the day its event_time names.

**Rebuild it.**

```bash
not rebuildable by the gravix CLI yet; run: go run ./transforms/service_events_daily/
```

## Known defects

Three of the metrics above are `approximate`: their error is not bounded. They are listed here because an undisclosed approximation is worse than a missing metric — a reader cannot tell a wrong number from a right one.

| Metric | Error bound | Fixed by |
|---|---|---|
| `latency_p50@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
| `latency_p95@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
| `latency_p99@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
