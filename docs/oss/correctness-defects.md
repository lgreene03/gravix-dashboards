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
| CD-004 | GRVX-806 | A second evolution silently discards the first: adding a dimension erases a previously added percentile | Open |

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

---

## CD-004 — evolutions are not cumulative, and the second one erases the first silently

**Owning spec:** GRVX-806 (retroactive percentiles and dimensions)
**Found by:** `scripts/prove_it.sh`, composing the two features the demo exists to show
**State:** open

### What happens

```
$ gravix evolve add-percentile --quantile 0.999 --from … --to … --yes
$ gravix explain request_metrics_minute "… 00:02" --filter service=svc-a
values:  request_count=59  p50_latency_ms=15  p95_latency_ms=102.5  p99_latency_ms=156.5
         p99.9_latency_ms=205        ← added retroactively, correct

$ gravix evolve add-dimension --field user_agent_family --from … --to … --yes
$ gravix explain request_metrics_minute "… 00:02" --filter service=svc-a
values:  request_count=15  p50_latency_ms=14  p95_latency_ms=39  p99_latency_ms=39
         user_agent_family=Chrome    ← the dimension arrived
                                     ← and p99.9_latency_ms is gone
```

Each `evolve` invocation rebuilds the window with **its own** `Evolution` and nothing else. The second
run has no `ExtraQuantiles`, so the column it would have populated is left empty, and the partition it
overwrites had it.

It fails silently. The second command reports success, the rebuild is legitimate, the digest changes as
it should, and nothing anywhere says a previously added metric has just been removed.

### Why it matters more than it looks

The two features are the two halves of the same claim. "You can change a metric definition after the
fact" is not a claim about percentiles or about dimensions; it is a claim about the definition. A user
who adds p99.9 in March and a dimension in April loses the p99.9 and is not told.

It also means the flagship demonstration could not show both against one warehouse.
`scripts/prove_it.sh` proves each against its own copy of the week and **says so in its output**, which
is the honest arrangement available today — but a demo that has to explain why it is running things
twice is a demo carrying a defect.

### What resolving it means

An `Evolution` should be a property of the metric's current definition, not an argument to one command
run. Two candidate shapes:

1. **Read the existing partition's evolution and merge.** The manifest already records the metric
   version; the partition's columns already say which extra quantile it carries. A rebuild could take
   the union of what is there and what is being asked for.
2. **Store the evolution set beside the contract.** `add-percentile` records that p99.9 is now part of
   the metric, and every subsequent rebuild — cron, recompute, a later evolution — applies the whole
   set. This is the better answer: it also makes the nightly rollup keep the added percentile, which
   today it would drop the first time it ran.

**Option 2 exposes a second, larger version of this bug.** A cron rollup after an `add-percentile` has
no evolution either, so it would erase the added column on its next run over that day. The demo did not
hit it because it runs no cron, but any real deployment would within a day.

Until it is fixed, `gravix evolve` should at minimum refuse to run against a partition that already
carries an evolution it is not preserving, rather than silently dropping it — a loud failure is a much
smaller bug than a quiet one.

---

## CD-005 — compaction re-encodes Parquet at a different zstd level than recompute, so a compacted partition can never be byte-identical to a recomputed one

**Owning spec:** GRVX-801 (byte-identical recompute), surfaced by GRVX-1003
**Found by:** reading the three Parquet writers while measuring the storage footprint
**State:** open

### What happens

`pkg/recompute/recompute.go:45-49` pins the level, and says why:

```go
// CompressionLevel pins the zstd level used for every Parquet file this package
// writes. It is an explicit constant rather than zstd.SpeedDefault because a
// [library default change] would change every content digest.
const CompressionLevel = zstd.SpeedFastest
```

Four other writers ignore it:

| File | Level |
|---|---|
| `pkg/recompute/recompute.go:1052` | `CompressionLevel` — `SpeedFastest` |
| `transforms/compaction/main.go:397, 440, 483` | `zstd.SpeedDefault` |
| `transforms/service_events_daily/main.go:423` | `zstd.SpeedDefault` |
| `transforms/service_events_detail/main.go:420` | `zstd.SpeedDefault` |

The same rows written at the two levels produce different bytes, confirmed directly:

```
recompute  (CompressionLevel=SpeedFastest): 6627 bytes  sha256:c92b444de45fa463
compaction (zstd.SpeedDefault):             6421 bytes  sha256:de2f65a2c8f1f747
RESULT: digests DIFFER (-206 bytes)
```

### Why it matters

`Run` is documented as "idempotent: running twice over the same window with the same facts produces
byte-identical output and reports `Rebuilt == 0` on the second run". That is the mechanism behind the
recomputability axis, and behind `Result.Unchanged`, `Result.Revised` and the digests `gravix explain`
prints.

Once compaction has touched a partition, that stops holding:

1. **`Unchanged` can never be reported again for it.** Recompute compares its output against what is
   stored; the stored file was written at a different level, so the bytes differ and the partition is
   rebuilt on every run, forever.
2. **`Revised` becomes a false alarm.** Its comment says a non-zero value "means numbers someone may
   have already read have moved". After compaction, it fires when nothing has moved — only the
   compression level differs. A signal that cries wolf on every run is worse than no signal.
3. **The digest in `gravix explain` is not stable across compaction.** A user who records a digest,
   waits for compaction to run, and re-reads it gets a different value for identical data.

None of the numbers are wrong. What is broken is the guarantee that lets a reader *prove* they are
not, which is the claim Gravix makes that the alternatives do not.

### Why nothing caught it

`TestRecomputeDeterminism` runs recompute twice and compares — both runs use `CompressionLevel`, so
they agree. Nothing exercises recompute *after* compaction, which is the only order in which the two
levels meet. The compaction tests check row counts and merge correctness, not byte-identity against a
recomputed file.

### The repair

Export the constant's use: every Parquet writer in the repository takes its level from
`recompute.CompressionLevel` (or a shared package that both import, to avoid `transforms/` depending
on `pkg/recompute` for a constant). Then add the test that would have caught it — recompute a
partition, compact it, recompute again, and assert `Rebuilt == 0`.

Not fixed under GRVX-1003: that spec's §4.2 covers the compaction and recompute writers but its
§5.3 forbids changing the compression level without changing GRVX-801's constant and re-verifying
determinism, and the decision of which level wins (fastest, or the smaller default) is a
size-versus-CPU trade this spec was not given the authority to make. It belongs to GRVX-801's owner.
