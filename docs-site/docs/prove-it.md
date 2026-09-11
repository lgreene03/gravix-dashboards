---
title: Prove it
sidebar_position: 2
---

# Prove it

Gravix claims you can change a metric definition after the fact and rebuild history to match. That is
the kind of claim every observability vendor makes some version of, so here is the version you can run.

It takes about ten seconds and needs nothing but Go.

## Run it yourself

```bash
git clone https://github.com/lgreene03/gravix-dashboards
cd gravix-dashboards
./scripts/prove_it.sh
```

No Docker. No account. No network. No signup. It generates its own data, computes on it, and deletes
the directory when it finishes — pass `--keep` if you want to poke at the files.

## What happens, step by step

The output below is the real thing, produced by running the script. Dates and the temporary directory
are the only substitutions.

```
Gravix: prove it

Adding a percentile and a dimension to a week of already-ingested data,
and checking the answers against computing them from scratch.

─────────────────────────────────────────────────────────────────────
1/7  Generate 7 days of request facts
─────────────────────────────────────────────────────────────────────
$ $WORK/bin/gen_facts -dir $WORK/data/raw/request_facts -origin YYYY-MM-DD -days 7 -seed 42 -services 1 -paths 1 -per-minute 400 -minutes-per-day 60
168000 facts written to $WORK/data/raw/request_facts
  7 days from YYYY-MM-DD, 1 services, pareto latency, 0.8% errors
  seed 42 — the same seed always produces exactly these facts

  168000 facts on disk, as newline-delimited JSON. Nothing is aggregated yet.

─────────────────────────────────────────────────────────────────────
2/7  Roll them up into minute-level metrics
─────────────────────────────────────────────────────────────────────
$ $WORK/bin/gravix recompute --from YYYY-MM-DD --to YYYY-MM-DD
recompute: request_metrics_minute YYYY-MM-DDT00:00:00Z .. YYYY-MM-DDT00:00:00Z
partitions: 7   rebuilt: 7   unchanged: 0
facts read: 168000   rows written: 1680
duration: 823ms

  p50/p95/p99 for svc-a, straight out of the rollup:
    values:        request_count=103  error_count=0  error_rate=0  p50_latency_ms=16  p95_latency_ms=76.5  p99_latency_ms=151  method=DELETE  path_template=/a/{id}  service=svc-a

─────────────────────────────────────────────────────────────────────
3/7  Ask a question the rollup cannot answer
─────────────────────────────────────────────────────────────────────
  What is the p99.9 for svc-a?

  p99.9 was never computed. In Prometheus this is where the story ends: the raw
  observations were discarded at scrape time.

  Gravix kept them. They are the 168000 facts from step 1.

─────────────────────────────────────────────────────────────────────
4/7  Add p99.9 to the history that already exists
─────────────────────────────────────────────────────────────────────
$ $WORK/bin/gravix evolve add-percentile --quantile 0.999 --from YYYY-MM-DD --to YYYY-MM-DD --yes
plan: percentile p99.9 -> request_metrics_minute@v3
partitions: 7   from: YYYY-MM-DD   to: YYYY-MM-DD
fact re-read: no
evolve: percentile p99.9 -> request_metrics_minute@v3
partitions: 7   rebuilt: 7   rows written: 1680
fact re-read: no
days beyond retention (not backfilled): 0
duration: 884ms

  p99.9 for svc-a at YYYY-MM-DD 00:05, added after the fact: 278 ms

─────────────────────────────────────────────────────────────────────
5/7  Prove that number is right
─────────────────────────────────────────────────────────────────────
  Rebuilding the same window from scratch, with p99.9 defined from the start,
  and comparing. A retroactive answer that does not match a from-scratch one
  is not recomputability, it is a guess.

    added to existing history:   278 ms
    computed from scratch:       278 ms
    difference:                  0.0000%  (bound: 1%)

─────────────────────────────────────────────────────────────────────
6/7  Add a dimension that was never a dimension
─────────────────────────────────────────────────────────────────────
  user_agent_family was recorded on every fact and used by no metric row.
  Nothing in the warehouse is grouped by it — until now.

  This runs against its own copy of the week, not the one from step 4.
  Applying a second evolution to the same warehouse discards the first: the
  p99.9 added in step 4 would disappear. That is a real limitation, recorded
  as CD-004, and working around it here rather than hiding it is the point.

$ $WORK/bin/gravix evolve add-dimension --field user_agent_family --from YYYY-MM-DD --to YYYY-MM-DD --yes
plan: dimension user_agent_family -> request_metrics_minute@v3
partitions: 7   from: YYYY-MM-DD   to: YYYY-MM-DD
fact re-read: yes
observed distinct values per day: 4 (limit 1000)
evolve: dimension user_agent_family -> request_metrics_minute@v3
partitions: 7   rebuilt: 7   rows written: 6720
fact re-read: yes
days beyond retention (not backfilled): 0
duration: 975ms

    rows are now grouped by user_agent_family, which was never a dimension
    added to existing history vs. computed from scratch: identical

─────────────────────────────────────────────────────────────────────
7/7  Show where the number came from
─────────────────────────────────────────────────────────────────────
$ $WORK/bin/gravix explain request_metrics_minute YYYY-MM-DD 00:05 --filter service=svc-a
metric:        request_metrics_minute@v2
bucket:        YYYY-MM-DDT00:05:00Z
filters:       service=svc-a
values:        request_count=103  error_count=0  error_rate=0  p50_latency_ms=16  p95_latency_ms=76.5  p99_latency_ms=151  method=DELETE  p99.9_latency_ms=278  path_template=/a/{id}  service=svc-a

contract:      latency_p50@v2
               (a rollup covers several metrics; this is the weakest guarantee among them)
formula:       50th percentile of latency_ms, from the merged latency_sketch over the queried window
grain:         1 minute, per service/method/path_template
exactness:     sketch (RANK error <= 1% at q in [0.5, 0.99]: the value returned sits within 1% of the requested quantile's true rank. That is the guarantee a t-digest makes, and it holds regardless of distribution or window size — measured worst 0.09% over 24,000 observations end to end (TestMergeabilityEndToEnd). VALUE error is not bounded by it and depends on how many observations the window holds and how heavy the tail is, because one observation of rank error at q=0.99 can be an order of magnitude in value, and integer-millisecond latencies make a sub-millisecond difference read as several percent at a small median. Measured worst relative value error across uniform, normal, lognormal, bimodal and pareto (TestSketchErrorIsAFunctionOfSampleSize): n=100 -> 157%, n=1,000 -> 17%, n=10,000 -> 5.8%, n=100,000 -> 0.8%. Below roughly 10,000 observations in the queried window, treat the value as indicative rather than accurate. This supersedes an earlier claim of a flat "relative error <= 1%", which was measured only at 1e6 observations and does not hold at realistic bucket sizes. See CD-001.)
mergeability:  sketch_merge — Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles — that is what v1 did and it is why v1 is deprecated. Merging is exact: it is a multiset union of centroids, so any grouping or ordering of the buckets gives the same answer.

derived from:  24000 facts in 4 file(s)
  raw/request_facts/YYYY-MM-DD/00/facts_00.jsonl
  raw/request_facts/YYYY-MM-DD/00/facts_01.jsonl
  raw/request_facts/YYYY-MM-DD/00/facts_02.jsonl
  raw/request_facts/YYYY-MM-DD/00/facts_03.jsonl

data file:     warehouse/request_metrics_minute/event_day=YYYY-MM-DD/request_metrics_minute_20260903.parquet
idempotency:   request_metrics_minute:v2:_single:20260903
digest:        sha256:db3b4faca1add9c1396021f2bf633ca9c47922fb66a70ef9efbe6de354defafc
revision:      1  (revised YYYY-MM-DDT17:59:48Z, previously sha256:9b4a9ce6653529ea1fd8407a74975b61bd81e0baa7fdd037a4702797a78087f9)

reproduce:     gravix recompute --metric request_metrics_minute --from YYYY-MM-DD --to YYYY-MM-DD

═════════════════════════════════════════════════════════════════════
PROVED
  p99.9 added to 7 days of already-ingested data:      278 ms
  same window computed from scratch with p99.9:        278 ms
  difference:                                          0.0000% (bound: 1%)
  dimension added to already-ingested data:            user_agent_family
  from-scratch comparison:                             identical
  every number above traced to its source facts:       yes

  Not yet: the two evolutions above cannot be combined. Applying a dimension
  to a warehouse that already has an added percentile discards the percentile,
  so each was proven against its own copy of the week. See CD-004.

  runtime: 9s

  What this shows: Gravix keeps the raw observation, so a metric definition can
  change after the fact and history can be rebuilt to match.

  What this does NOT show: that we are better than Datadog at everything. We do
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
  docs/oss/01-competitive-thesis.md for the full comparison, including what we
  are worse at.
═════════════════════════════════════════════════════════════════════
```

## Why this is hard for other tools

The mechanism, not the marketing.

**Prometheus** aggregates at scrape time. A classic histogram records how many observations fell into
each pre-declared bucket and then discards the observations. Ask for p99.9 and there is nothing left to
compute it from — you can only interpolate within whatever buckets someone chose in advance. Native
histograms, added in 2.40, improved the resolution and the cardinality cost substantially, but they did
not change that: the raw observation is still gone, and the bucket boundaries are still decided before
the question is asked.

**Grafana** queries what Prometheus stored. It cannot recover what was never kept.

**Datadog is a different case, and the honest comparison says so.** Datadog's distribution metrics use
DDSketch, which *is* mergeable and *does* give a relative-error guarantee on the value — a stronger
guarantee than the t-digest Gravix currently uses. The percentile-merging half of this demonstration is
not an advantage over Datadog. What remains is the second half: neither Datadog nor Prometheus can add
a **dimension** that was never emitted, because neither kept the individual requests the dimension
would have to be derived from.

So the scope of the claim is: **against Prometheus and Grafana, both halves. Against Datadog, only the
dimension.** That limit is recorded in the
[claim register](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/01-competitive-thesis.md)
and it is binding on everything we publish.

## What this does not prove

  What this does NOT show: that we are better than Datadog at everything. We do
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
  docs/oss/01-competitive-thesis.md for the full comparison, including what we
  are worse at.

It also does not prove that the two changes can be made together. **They cannot, yet.** Applying a
dimension to a warehouse that already carries an added percentile discards the percentile, silently.
The demonstration works around it by giving each change its own copy of the week, and says so in its
own output. The defect is [CD-004](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/correctness-defects.md).

That register exists because the correctness suite keeps finding things, which is the point of having
one. [CD-001](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/correctness-defects.md)
is worth reading before you rely on any percentile: the error bound depends on how many observations
the window holds, and below about ten thousand it is not what you would assume.

## The commands, individually

Run these against your own data. Each is a normal command, not a demo mode.

```bash
# Rebuild a window of metrics from the facts. Byte-identical every time, so
# running it twice rebuilds nothing the second time.
gravix recompute --from 2026-09-03 --to 2026-09-10

# Add a percentile to history. No fact re-read: it comes from the sketch each
# bucket already stores.
gravix evolve add-percentile --quantile 0.999 --from 2026-09-03 --to 2026-09-10 --yes

# Add a dimension to history. This one does re-read the facts, because the rows
# that would carry it were never separated.
gravix evolve add-dimension --field user_agent_family --from 2026-09-03 --to 2026-09-10 --yes

# Show where a number came from: the contract, the exactness, the fact files,
# the digest, and the command that rebuilds it.
gravix explain request_metrics_minute "2026-09-03 00:05" --filter service=api

# Plan without writing anything.
gravix evolve add-dimension --field user_agent_family --from … --to … --dry-run
```
