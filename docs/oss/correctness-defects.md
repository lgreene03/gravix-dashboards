<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Correctness defects

A **correctness defect** is a case where Gravix's numbers, or the claims Gravix makes about its
numbers, do not survive contact with a test. It is distinct from a spec defect (an ambiguous
instruction) and from a finding (something untidy): this register is only for things that make a
published number or a published claim wrong.

They are found by `make test-correctness` (GRVX-810). Per that spec's §10, a failing property is
**never** fixed by softening the assertion — it is recorded here against the spec that owns it.

Gravix's whole argument is that its numbers can be checked. A register of the times they did not check
out is not an embarrassment; it is the argument working. A project claiming correctness with an empty
defect register is a project that has not looked.

| ID | Owning spec | Summary | State |
|---|---|---|---|
| CD-001 | GRVX-804 | The published error bound was a value bound for a rank-error sketch, false by up to 446% at realistic bucket sizes | Corrected |
| CD-002 | GRVX-801 | `gravix recompute` rebuilds one metric of eight; the rest have no CLI reproduction path | Open |
| CD-003 | GRVX-804 | The scalar percentile column and the sketch answer the same question with different definitions | Open |

---

## CD-001 — the sketch's published error bound was the wrong kind of bound

**Owning spec:** GRVX-804 (mergeable quantile sketches)
**Found by:** `TestSketchErrorIsAFunctionOfSampleSize`, `TestMergeabilityEndToEnd`
**State:** corrected — the claim was wrong, not the code

### What was published

`contracts/request_metrics_minute.v2.yaml`, for all three percentiles:

> relative error <= 1% at q in [0.5, 0.99], measured by TestAccuracyWithinBound over 1e6 observations
> across five distributions (uniform, normal, lognormal, bimodal, pareto); worst observed 0.66% for a
> single bucket and 0.92% for a merged day of 1440 buckets

`GET /api/v1/percentile` returned the same sentence in every response, and the competitive thesis rests
on it.

### What is true

Measured across the five distributions the claim itself names, at bucket sizes a real service produces:

| observations in the window | worst relative **value** error |
|---|---|
| 10 | 76.8% |
| 100 | **446.1%** |
| 1,000 | 16.9% |
| 10,000 | 5.8% |
| 100,000 | 0.8% |

The 1% holds only at about 1e5 observations — the regime the original measurement was taken in. A
minute bucket per service/method/path_template containing 100,000 requests is 1,600 requests a second
on one endpoint. Below that the claim is false, and it is most false exactly where latency matters
most: the p99 of a heavy tail.

### Why

**A t-digest guarantees rank accuracy, not value accuracy.** The answer it gives for q sits within the
compression bound of q's true rank. On a heavy tail, one observation of rank error at q=0.99 is an
order of magnitude of value. The contract promised the wrong one of the two.

Two candidate explanations were ruled out by measurement:

- **Insertion order.** `sketch.FromSorted` feeds pre-sorted values in, which can degenerate some
  streaming summaries. Sorted and shuffled insertion give the same numbers to three decimal places, so
  this is not it.
- **A bug in `pkg/sketch`.** The rank error, measured end to end through the real pipeline over 24,000
  observations, is 0.0009, 0.0000 and 0.0001 at q = 0.5, 0.95 and 0.99 — three orders of magnitude
  inside the bound. The sketch is doing its job well.

A third factor showed up only by running the real pipeline rather than a synthetic sweep: `latency_ms`
is an integer, so a low median sits on a tie plateau and a sub-millisecond difference reads as several
percent of a 15 ms value. A relative bound is a harsh measure of a small discrete quantity.

### What changed

- The contract now publishes the **rank** bound, which holds always, alongside the measured **value**
  error table and the sample size each figure applies at. It says plainly that below roughly ten
  thousand observations the value is indicative.
- `GET /api/v1/percentile` computes the bound from the number of observations actually behind the
  answer, so a sparse window is told it is sparse rather than handed a 1% promise.
- `TestSketchErrorIsAFunctionOfSampleSize` asserts ceilings at each sample size, so the numbers cannot
  drift, and fails loudly if the small-n error ever comes *inside* 1% — at which point the
  qualification should be removed rather than left standing out of habit.

### What would earn the stronger claim

`docs/oss/30-technology-review.md` §2.2 already recommends DDSketch, whose guarantee is relative-error
on the **value**. That is the promise the contract was trying to make. `pkg/sketch` owns its own wire
format precisely so the engine underneath can be swapped, so this is a contained change — and CD-001 is
now the concrete reason to make it rather than a preference.

---

## CD-002 — seven of eight metrics have no CLI reproduction path

**Owning spec:** GRVX-801 (the recompute engine)
**Found by:** `TestRecomputeCommandsRun`, when the service-event contracts were added
**State:** open

`gravix recompute` accepts `request_metrics_minute` and rejects everything else with
`ErrUnknownMetric`. The service-event metrics are rebuilt by running their transform binaries directly:

```
go run ./transforms/service_events_detail/
go run ./transforms/service_events_daily/
```

That works, but it is not the promise. "Every number can be rebuilt from the facts with one command" is
a claim about the product, and it currently holds for one metric family.

The registry's `recompute_cmd` field now accepts an explicit `not rebuildable by the gravix CLI yet;
run: <command>` prefix rather than forcing a contract to quote a command that exits non-zero. An honest
"not yet, here is what does" is a better answer to *how do I reproduce this number?* than a confident
wrong one — but it is a placeholder, not a resolution.

**What resolving it means:** `gravix recompute --metric service_events_daily` works, with the same
manifest, digest and idempotency guarantees the minute rollup has. Until then the recomputability axis
of the competitive thesis should say "for request metrics" wherever it is stated.

---

## CD-003 — the scalar percentile and the sketch disagree, and nothing says which is authoritative

**Owning spec:** GRVX-804
**Found by:** `TestScalarAndSketchPercentilesDisagree`
**State:** open

Every metric row stores a percentile twice: as a scalar column (`p95_latency_ms`) and inside
`latency_sketch`. **They are different numbers.** In a representative run, 76 of 80 buckets had a scalar
p95 that differed from the sketch's, with the scalar up to 9.9% from the interpolated truth and the
sketch up to 26.4% at those bucket sizes.

They differ because they use different definitions of "percentile":

- The scalar comes from `montanaflynn/stats`, which indexes at `q·n` and takes the element below a whole
  index. For `[10,20,30,40]` at q=0.5 it returns **20**.
- The sketch interpolates, and returns **25** for the same input.

Neither is wrong as a definition. What is wrong is that both ship under one name and the contract's
`formula` field says only "95th percentile of latency_ms", so nothing in the repository states which
rule is in force.

**The user-visible consequence, introduced by GRVX-808:** the dashboard reads the scalar at minute
granularity and the merged sketch at any wider window. So the same endpoint's p95 for the same minute
changes when you zoom out, and both numbers are labelled p95.

**What resolving it means**, and it is small: `docs/oss/30-technology-review.md` §2.1 already recommends
dropping `montanaflynn/stats` for the ten lines of `slices.Sort` plus interpolation it replaces, for
exactly this reason — owning the ten lines lets the contract state the rule. Do that, make the scalar
use the same definition the sketch does, and write the rule into each contract's `formula`.

Until then `TestScalarAndSketchPercentilesDisagree` holds the divergence inside a measured envelope so
it cannot widen unnoticed, and fails if the two ever agree — at which point the test should be deleted
and the rule recorded.
