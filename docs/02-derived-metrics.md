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
| **Error bound** | relative error <= 1% at q in [0.5, 0.99], measured by TestAccuracyWithinBound over 1e6 observations across five distributions (uniform, normal, lognormal, bimodal, pareto); worst observed 0.66% for a single bucket and 0.92% for a merged day of 1440 buckets |
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
| **Error bound** | relative error <= 1% at q in [0.5, 0.99], measured by TestAccuracyWithinBound over 1e6 observations across five distributions (uniform, normal, lognormal, bimodal, pareto); worst observed 0.66% for a single bucket and 0.92% for a merged day of 1440 buckets |
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
| **Error bound** | relative error <= 1% at q in [0.5, 0.99], measured by TestAccuracyWithinBound over 1e6 observations across five distributions (uniform, normal, lognormal, bimodal, pareto); worst observed 0.66% for a single bucket and 0.92% for a merged day of 1440 buckets |
| **Mergeability** | `sketch_merge` |
| **Supersedes** | `latency_p99@v1` |

**Aggregating it.** Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles — that is what v1 did and it is why v1 is deprecated. Merging is exact: it is a multiset union of centroids, so any grouping or ordering of the buckets gives the same answer.

**Late data.** The bucket is recomputed from all of its facts, sketch included, so a late fact shifts the quantile to its correct value rather than being folded into a stale summary.

**Rebuild it.**

```bash
gravix recompute --metric request_metrics_minute --from 2026-09-01 --to 2026-09-02
```

## Known defects

Three of the metrics above are `approximate`: their error is not bounded. They are listed here because an undisclosed approximation is worse than a missing metric — a reader cannot tell a wrong number from a right one.

| Metric | Error bound | Fixed by |
|---|---|---|
| `latency_p50@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
| `latency_p95@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
| `latency_p99@v1` | UNBOUNDED across buckets; exact within one bucket | `GRVX-804` |
