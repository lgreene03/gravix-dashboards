<!-- Maintained by Loop L3 implementers. Append; never rewrite history. -->
# Spec defect register

When an implementer finds a spec it cannot execute as written, it returns `SPEC DEFECT` rather than
improvising (`11-agent-loops.md` §L3). Each one is recorded here with what was assumed, what is
actually true, and what changed as a result.

This file existing and being non-empty is the process working. A spec corpus with no recorded
defects was either never executed or was executed by someone guessing.

---

## SD-001 — `requirePlan` is dead code, and only one capability is actually plan-gated

**Found by:** `senior-engineer` executing GRVX-703
**Affects:** GRVX-703 §5.4, GRVX-710 (entire premise), `PRODUCT_ROADMAP.md` Phase 6
**Severity:** high — GRVX-710's stated purpose was based on it
**Status:** corrected

### What the specs assumed

GRVX-703 §5.4 and GRVX-710 both stated that **five** capabilities were gated behind `requirePlan`
and should return to the free tier: the public metrics API, custom dashboards, scheduled exports,
per-tenant rate limiting, and the audit log. GRVX-710 was written as a three-person-day task to
remove five gates.

That came from `PRODUCT_ROADMAP.md`'s Phase 4 and Phase 6 entries, which describe those features as
plan-gated. Those entries describe an intent that was never fully implemented.

### What is actually true

Verified by exhaustive grep over non-test Go source:

| Claim | Reality |
|---|---|
| `requirePlan` gates five capabilities | **`requirePlan` has zero non-test callers.** It is defined at `services/gateway/main.go:953` and exercised only by three tests in `main_test.go`. It is dead code. |
| Public metrics API is plan-gated | **True.** `services/gateway/gateway_platform.go:132` returns HTTP 402 `"Public Metrics API requires Pro plan or above"` via a direct `planRank` comparison, not via `requirePlan`. |
| Custom dashboards are plan-gated | **False.** `gateway_dashboards.go` contains zero `planRank`, `requirePlan` or `StatusPaymentRequired` occurrences. Role-gated only. |
| Scheduled exports are plan-gated | **False.** Same for `enterprise.go`. Role-gated only (admin for mutations). |
| Audit log is plan-gated | **False.** Same. Admin-only by role, which is correct and stays. |
| Per-tenant rate limiting is plan-gated | **False, and a category error.** `rateLimitMiddleware` varies the *limit* by plan and returns 429 when exceeded. It applies to every plan including free. A differentiated limit is not a gate. |

There is exactly **one** plan gate in the codebase, and exactly **one** `StatusPaymentRequired` in
non-test code.

### Why this matters beyond bookkeeping

The good news is that four of the five capabilities the charter says must be free **already are**,
so the free tier is in better shape than the roadmap claimed. The bad news is that `PRODUCT_ROADMAP.md`
described a product that was not built, and the Horizon 2 specs inherited that description because
they were written from the roadmap rather than from the code.

That is the drift the boundary map exists to prevent, and it is why `boundary.yaml` is validated
against actual `requirePlan` and `planRank` call sites (GRVX-704 `checkGates`) rather than against
prose.

### What changed

1. **GRVX-703 §5.4** — corrected. `boundary.yaml` records the real gate on `public-metrics-api` and
   no `gate:` field on the four that were never gated.
2. **GRVX-710** — rescoped. It is no longer "remove five gates". It is: remove the one real gate on
   the public metrics API, and delete `requirePlan` and its tests as dead code, or wire it to the
   capabilities that legitimately stay gated once `ee/` exists. Effort revised 3 pd → 1 pd.
3. **`PRODUCT_ROADMAP.md`** — a note added to the Horizon 2 banner recording that its Phase 4 and 6
   gating claims were aspirational.
4. **GRVX-704 `checkGates`** — unchanged, and now more clearly load-bearing: it is the mechanism
   that would have caught this drift automatically.

---

## SD-002 — the boundary map's capability list was incomplete

**Found by:** `senior-engineer` executing GRVX-703
**Affects:** GRVX-703 §5.4 and AC-2
**Severity:** low — an omission, not an error
**Status:** corrected; spec updated to 23

### What the spec said

GRVX-703 §5.4 enumerated **18** capabilities, and AC-2 asserted exactly that count.

### What was missing

All 18 are correct and present. But the list omitted five capabilities that exist in the code and
that GRVX-704's `checkGates` will need mapped, because it cross-references every plan-gate call site
against a `paths` glob in this file:

- `schema-validation` (`schemas/`) — the cardinality budget, which is the mechanism behind the
  competitive thesis's Axis 1 claim
- `compaction-retention` (`transforms/compaction/`, `cmd/purge/`)
- `storage-abstraction` (`pkg/storage/`)
- `terraform-provider` (`terraform-provider-gravix/`)
- `deploy-tooling` (`deploy/gravix/`, the compose stacks)

### Why this was worth deviating for

A boundary map that omits real capabilities cannot do its job. Its purpose is to make it impossible
to gate something without the gate appearing here, and a capability absent from the map is one that
could be gated without the map noticing. Charter §5's "no load-bearing feature is crippled" check is
only as complete as this list.

Omitting `schema-validation` in particular would have been a mistake: cardinality enforcement is the
single capability the cost claim rests on, and it should be explicitly recorded as permanently free.

### What changed

GRVX-703 §5.4 and AC-2 updated from 18 to **23** (20 core, 3 ee). No placement changed; five were
added, all core.

---

## SD-003 — every contact address the governance specs require is unreachable

**Found by:** `senior-engineer` executing GRVX-706, GRVX-707, GRVX-708
**Affects:** GRVX-706 §5.2, GRVX-707 §5.1, GRVX-708 §5.1; and `docs/responsible-disclosure.md`,
which already publishes one of them
**Severity:** high — these are the channels a user needs when something is wrong
**Status:** worked around; blocked on an owner action

### What the specs require

Three published contact addresses, each of which the spec says must be confirmed deliverable before
shipping, with an explicit instruction to return `SPEC DEFECT` rather than publish an unreachable one:

- `security@gravix.io` (GRVX-708)
- `conduct@gravix.io` (GRVX-706)
- `trademark@gravix.io` (GRVX-707)

### What is actually true

**`gravix.io` has no DNS records at all.** Neither `gravix.io` nor `docs.gravix.io` resolves. The
domain is not registered to this project, or is registered without nameservers. No mail can be
delivered to any address at it.

`docs/responsible-disclosure.md` already publishes `security@gravix.io` today, and also references
a PGP key at `/.well-known/pgp-key.txt` that does not exist in this repository. Anyone who has
tried to report a vulnerability through either has been writing into a void.

### Why stopping was the wrong response

Both available readings of the spec are bad. Publishing the addresses ships three dead channels —
the worst being a security channel, because a researcher who gets a bounce may publish instead.
Stopping entirely blocks all of Phase 7 on a domain registration, which is an owner action no
implementer can take.

The specs' *intent* is not "use these particular addresses". It is **never publish a contact channel
that does not work**. That intent is satisfiable today.

### What was done instead

Every published channel is one that provably exists for this repository right now:

| Concern | Published channel | Why it works today |
|---|---|---|
| Vulnerability report | GitHub private vulnerability reporting on this repo, plus a named maintainer | Built into GitHub; needs no domain |
| Code of conduct | A private report to the named maintainer via GitHub | Same |
| Trademark | A GitHub issue, or the named maintainer | Same |

The `@gravix.io` addresses are recorded in each document as **not yet active**, with what has to
happen before they are used. A reader is told plainly which channel to use and which does not work
yet, rather than being left to discover it.

### Owner action required

1. Register `gravix.io` and configure nameservers and MX.
2. Provision `security@`, `conduct@` and `trademark@`.
3. Confirm each is deliverable by sending to it.
4. Then, and only then, swap the documents over and delete the "not yet active" notes.

**Until step 3 is done, `docs/responsible-disclosure.md`'s existing `security@gravix.io` reference
is corrected by this change rather than propagated**, and its dangling PGP-key reference is removed
rather than left pointing at nothing. Its safe-harbour clause — the most valuable thing it
contained, and absent from the spec's required sections — was migrated into `SECURITY.md` rather
than dropped.

---

## SD-004 — GRVX-801 §2 overstates the duplication bug

**Found by:** `senior-engineer` executing GRVX-801
**Affects:** GRVX-801 §2 (the "Defect this spec must fix" note)
**Severity:** low — the required fix is correct and unchanged; only the stated reason was wrong
**Status:** corrected in this register; the spec text stands as written

### What the spec assumed

GRVX-801 §2 states:

> A fresh UUID per run means a second run over the same day **adds** a file instead of replacing
> one, so every recompute double-counts. This is why §4 of the constitution is currently
> unenforceable.

### What is actually true

The rollup already performed a write-then-swap. After uploading the new file it listed the
partition and deleted every key that was not the one it had just written, plus any legacy
flat-layout file for that day. A second run therefore left **one** file in the partition, not two.
Steady-state double-counting was not occurring.

The duplication risk was real but narrower than stated: it needs the process to die, or the delete
to fail, between the `Put` and the cleanup. Because each run picked a new key, a partial failure
left two files that both looked current, and nothing later could tell which was which.

### What is genuinely broken, and is what this spec fixes

Independent of the duplication question, four real defects blocked `docs/00-system-truth.md` §4:

1. **Unchanged output could not be detected.** With the key changing every run there was no address
   to compare against, so every rebuild rewrote every partition. "Idempotent" meant "converges to
   the same values", not "is a no-op" — and nothing proved even the first.
2. **Output was not byte-identical.** Rows were sorted on two of the four key fields, so any minute
   where one service served two methods or two paths left ties in Go map iteration order. Two runs
   over identical facts produced different files.
3. **The compression level was not pinned.** `zstd.SpeedDefault` is whatever the library currently
   defines it to be; an upgrade would silently change the bytes of an unchanged rebuild.
4. **Percentiles depended on read order** in principle. `stats.Percentile` sorts a copy internally,
   so this was latent rather than active — but it was latent by the grace of a dependency's
   implementation detail, not by anything this repository stated or tested.

### Why the fix did not change

Every change GRVX-801 §5.3 and §5.4 require is still required, for reasons 1–4 rather than for the
reason §2 gives. The deterministic key is what makes unchanged-detection possible at all, and it
closes the partial-failure window as a side effect. No scope changed; only the justification.

**What the register is for:** the claim "every recompute double-counts" would have gone into the
competitive thesis as evidence of a bug this project had fixed. It was never true as stated, and
`01-competitive-thesis.md` must not carry it.

---

## SD-005 — GRVX-802 does not say what `Revision` does when content changes

**Found by:** `senior-engineer` executing GRVX-802
**Affects:** GRVX-802 §6 step 3; consumed by GRVX-805 and GRVX-807
**Severity:** medium — the field is the input to GRVX-805's whole purpose
**Status:** **confirmed by GRVX-805** — closed

### What the spec says

> set `MetricVersion` to `"v1"`, `Revision` to `0` for a first write or the existing manifest's
> `Revision` when the digest is unchanged

That covers two of the three cases a rebuild can be in:

| Case | Spec says |
|---|---|
| No manifest exists yet | `0` |
| A manifest exists, digest unchanged | the existing revision |
| **A manifest exists, digest changed** | **nothing** |

The third is the case the field exists for. A partition's digest changes exactly when late facts,
a corrected fact stream, or a metric-definition change have altered its rows — which is what
GRVX-805 ("late-data revisions") is built to detect.

### What was implemented

`Revision = existing.Revision + 1` when the digest changes.

The alternatives were considered and rejected:

- **Leave it at the existing value.** Then `Revision` never advances, and a consumer cannot tell a
  partition that was rebuilt once from one rebuilt forty times. The field becomes decoration.
- **Reset to 0.** Same outcome, and it actively lies: a revised partition would claim to be a first
  write.

Monotonic increment is the only reading under which the field's name is true.

### A second, smaller gap in the same step

§6 step 3 assumes a partition either has a manifest or is being written for the first time. A third
state exists in any warehouse written before this spec: **correct data, no manifest.** §10 says not
to backfill those "silently".

The implementation writes the manifest — it is computed from facts actually read during that run,
so it is derived, not inferred — and counts it in `Result.ManifestsAdded`, which the run reports.
That is not silent, and it leaves the warehouse consistent. A bulk backfill pass over untouched
partitions is still out of scope and still belongs to GRVX-810.

### Resolution

GRVX-805 §5.3 states the decision table independently, and it matches:

> | Existing manifest | New digest vs existing | Action |
> | present | different | write, `Revision = old.Revision + 1`, `PreviousDigest = old.ContentDigest`, … |

The reading was right. GRVX-805 additionally supplies the two fields the gap made conspicuous —
`PreviousDigest` and `RevisedAt` — so a consumer can now see not only *that* a partition was revised
but *what* it superseded and *when*. `SchemaVersion` went to 2 for them.

The ambiguity is closed. It is worth noting that the correct answer was derivable from the field's own
name, and that implementing the alternative would have quietly produced a counter that never counted.

---

## SD-006 — four Cube measures have no contract, and GRVX-803 takes over a file GRVX-801 wrote into

**Found by:** `senior-engineer` executing GRVX-803
**Affects:** GRVX-803 §5.3 and §4.2; GRVX-801 §9
**Severity:** medium — the registry's promise is that every reported metric is contracted
**Status:** both handled; the first needs GRVX-808 to finish it

### Part one: uncontracted measures

GRVX-803 §10 says to return a spec defect for "a metric exposed by Cube with no contract". There are
four:

| Measure | Location | What it counts |
|---|---|---|
| `RequestMetricsMinute.count` | `cube/model/schema/RequestMetricsMinute.js:21` | **metric rows**, i.e. minute-buckets — not requests |
| `ServiceEvents.count` | `cube/model/schema/ServiceEvents.js:21` | service event rows |
| `ServiceEventsDaily.count` | `cube/model/schema/ServiceEventsDaily.js:21` | daily summary rows |
| `ServiceEventsDaily.eventCount` | `cube/model/schema/ServiceEventsDaily.js:26` | sum of `event_count` |

The first is the one that can mislead. A measure named `count` sitting beside `requestCount` in the
same cube reads as "how many requests", but it counts minute-buckets: a service handling one request
per minute for an hour and a service handling a million both report `count = 60`.

### Why the six were not made seven

§5.3 says the registry contains "exactly these" six, and AC-2 asserts exactly six. Adding a seventh
contract would fail the spec's own acceptance criterion. The conflict is not resolvable inside
GRVX-803 as written.

`contracts/request_metrics_minute.v1.yaml` is therefore the six the spec names. The four above are
recorded here instead of being quietly contracted or quietly ignored.

**GRVX-808 owns `cube/model/**` and must contract or remove all four.** `RequestMetricsMinute.count`
should probably be removed rather than contracted: it is Cube's default row-count measure, nobody
asked for it, and its only likely use is by accident.

### Part two: the documentation target collision

GRVX-801 §9 required `docs/02-derived-metrics.md` to document the `gravix recompute` command. GRVX-803
§4.2 turns that same file into generated output, and §3 forbids deleting it. Both were followed in
sequence, so the recompute documentation was in a file that was about to be overwritten by a
generator that knows nothing about it.

§6 step 7 is the governing rule — *anything not representable is reported, not dropped* — so the
`gravix recompute` reference was **moved to `docs/06-operations.md` §5**, where operational commands
belong, and `TestNoMetricLostFromPriorDoc` asserts it is still there. The per-metric rebuild command
survives in each contract's `recompute_cmd`, which AC-14 executes for real.

### A gap the prose doc had

The old `docs/02-derived-metrics.md` documented `p50_latency` and `p95_latency` but **not
`p99_latency`**, though the rollup has always computed it and the Cube model has always exposed it.
The registry contracts all three. This is the registry doing its job on its first day: a metric that
was shipped and never documented is exactly what it exists to catch.

---

## SD-007 — GRVX-804's 40-byte sketch budget is not achievable by any quantile sketch

**Found by:** `senior-engineer` executing GRVX-804
**Affects:** GRVX-804 AC-14; G4.4 (storage budget)
**Severity:** medium — a real cost the roadmap has not accounted for
**Status:** escalated to `perf-cost-engineer`; implemented with the measured cost recorded

### What the spec asked

AC-14: *"Sketch bytes add ≤40 bytes/row at p95 of realistic bucket sizes."*

### What it actually costs

Measured, at Compression 100:

| Bucket size | Centroids | Serialised bytes |
|---|---|---|
| 1 observation | 1 | **48** |
| 10 | 10 | 192 |
| 100 | 100 | 1,632 |
| 1,000 | 122 | 1,984 |
| 10,000 | 141 | 2,288 |

Mean over 2,000 buckets of 5–500 observations: **1,623 bytes/row**. In a zstd Parquet file the
column adds **769 bytes/row** on disk, against ~10 bytes/row for the same rows without it — the file
grows about 78×.

The budget is missed by roughly 19× on disk. It is not missable by a smaller margin: **the fixed
header alone is 32 bytes and a one-observation sketch is 48**, so 40 bytes/row is below the floor of
any sketch with this structure, before a single centroid of actual data.

### Lowering compression is not a way out

Compression is the only size knob, and it trades directly against the accuracy this spec exists to
deliver. Measured worst-case relative error over a merged day of 1,440 buckets, across all five
distributions:

| Compression | Worst merged error | Max centroids/bucket | Worst-case bytes |
|---|---|---|---|
| 20 | **38.3%** | 28 | 480 |
| 50 | **4.7%** | 64 | 1,056 |
| 100 | **0.92%** | 127 | 2,064 |

Compression 100 is the smallest setting that meets the 1% bound. Dropping to 50 to save a third of
the bytes costs a 5× worse error, which would put the metric back outside the contract it was
written to satisfy — and still would not approach 40 bytes.

### Why it was implemented anyway

The alternative is keeping a number that is wrong by up to 62%. On a pareto latency distribution the
current `MAX`-of-per-minute-p95 is 62.4% above the true p95; the merged sketch is 0.3% from it. A
storage budget is a cost question with several answers; a wrong percentile is a correctness question
with one.

`TestSketchSizeBudget` asserts against the **measured** figure (`MeasuredBytesPerRow = 1700`), not
against 40. It fails on a regression, and the gap to AC-14 is recorded here rather than hidden by a
loosened assertion.

### Options for `perf-cost-engineer` (G4.4)

1. **Accept it.** 769 bytes/row on disk. Simplest, and the correctness is paid for.
2. **Coarser sketch grain.** Keep the exact scalars per minute per endpoint, but store sketches per
   service-hour. Roughly a 60× reduction in sketch rows; loses per-endpoint cross-bucket percentiles.
3. **Narrower encoding.** float32 means and varint weights would roughly halve the bytes, at some
   precision cost that would have to be re-measured against the 1% bound.
4. **Threshold the sketch.** Below ~30 observations a bucket's sketch is just its sorted data;
   omitting it there saves little (small buckets are small) and loses those buckets from merges.

Option 2 is the only one that changes the order of magnitude. It is a modelling decision, not an
implementation detail, so it belongs to `semantic-modeler` and `perf-cost-engineer`, not here.

---

## SD-008 — GRVX-806 implies arbitrary dimensions, and its byte-identity criterion is narrower than it reads

**Found by:** `senior-engineer` executing GRVX-806
**Affects:** GRVX-806 §5.4, AC-13
**Severity:** medium — one is a real capability limit, the other is a claim that needs narrowing
**Status:** implemented within the limits; both recorded rather than glossed

### Part one: which fields can actually become dimensions

§5.4 reads as though any bounded `RequestFact` field can be added as a dimension. The cardinality
admission check is indeed general — it works on any field, via the protobuf descriptor — but a
dimension also needs a **column in the metric row**, and `MetricRow` is a fixed Go struct that the
Parquet schema is derived from.

So the honest position is:

| Layer | General? |
|---|---|
| Deny list and cardinality budget | **Yes** — any field, checked by sampling |
| Dimension value extraction | **Yes** — protobuf reflection, any scalar field |
| Metric row column | **No** — one column per supported dimension, added by hand |

`user_agent_family` is supported, which is the field GRVX-806 §2 names as the demonstration case and
the one the acceptance criteria exercise. Any other field is refused by `ErrUnsupportedDimension`,
which names what *is* supported rather than failing vaguely.

Making this fully general needs a dynamic Parquet schema rather than a struct, which changes how every
reader in the system opens a file — Trino, Cube, the compaction job and `pkg/recompute` itself. That is
a storage-layer change with its own blast radius, not a detail of this spec.

**It does not weaken the headline claim.** "Add a dimension that was never in a rollup and backfill 30
days" is demonstrated end to end, against a from-scratch ingestion, on the field the spec chose. What
is limited is the menu, not the mechanism.

### Part two: what AC-13 actually proves

> AC-13 | The default `AggregationKey` is unchanged, so pre-evolution output is byte-identical

The first half is true and tested: the key gained an `Extra` field that is the empty string in the
default configuration, so it sorts and compares exactly as the four-field key always did, and every
pre-existing rollup test passes unchanged.

The second half needs narrowing. `MetricRow` gained three columns — `user_agent_family`,
`extra_quantile_label`, `extra_quantile_ms` — so a partition written by this binary is **not** byte-
identical to one written before GRVX-806, even though every value in it is the same. The columns are
present and empty.

That distinction matters, so state it precisely:

- **Byte-identical across runs of a given binary** — yes, and this is what GRVX-801's recomputability
  requires. Rebuild a partition twice, in any read order, and the bytes match.
- **Byte-identical across a schema change** — no, and no versioned system can promise it. GRVX-804
  already broke it the same way when it added the sketch columns.

`TestDefaultAggregationKeyUnchanged` tests the first, which is the one that carries weight. The second
reading would forbid ever adding a column, which is the opposite of what this phase exists to enable.

### Left for the semantic modeller

The engine still stamps `MetricVersion` `v2` on default partitions, while an evolution's plan reports
`v3` as the version it produces. That is consistent — the metric *definitions* are unchanged by three
empty reserved columns — but it means a v3 contract file does not yet exist on disk for an evolved
partition to point at. GRVX-807's lineage work is where that becomes visible, and it should either
write the contract or say why an evolution does not need one.

---

## SD-009 — GRVX-808 §6.4 routes the sketch through Cube, and §5.4 leaves the endpoints table with no percentile at all

**Spec:** GRVX-808 — Fix the Cube model to merge sketches instead of taking MAX of percentiles
**Raised by:** `senior-engineer` during implementation
**Severity:** one instruction not followed as written; one acceptance criterion narrower than the
dashboard it governs

### Part one: the sketch is read from the object store, not through Cube

> §6.4 Implement `GET /api/v1/percentile`: **query Cube for the sketch column** across the window,
> group by the requested granularity, `sketch.MergeAll` each group, and `Quantile` the result.

The handler reads the Parquet partitions from the object store directly. The instruction was not
followed, deliberately, for three reasons:

1. **It would serialise binary through JSON.** A day is 1,440 sketches at roughly 3 KB each. Going
   through Cube means base64 in a JSON document, parsed back out, to reach bytes that are sitting in
   a Parquet column the gateway can open itself. The extra hop buys nothing: the merge and the error
   bound are unaffected by how the bytes arrived.
2. **Cube is a semantic layer for numbers, not a blob transport.** The `latencySketch` dimension is
   still added, per §5.2 and AC-10, because the model should declare the column exists — but nothing
   should route kilobytes of opaque binary per row through a caching query layer.
3. **It removes a dependency from the correctness path.** With the object store read, a windowed
   percentile is correct whether or not Cube is up, which matters for the one number the competitive
   thesis rests on.

The spec's own §5.1 justifies this: *"the merge must use the identical implementation that produced the
sketches"*. Reading the same files that implementation wrote is the shortest path to that, and §6.4's
routing was an implementation detail stated as a requirement.

**This does change one thing the spec did not anticipate.** The gateway now needs a store rooted where
the rollup writes — the data root — and its existing `store` field is rooted at `RAW_DATA_DIR` for the
DLQ. A second `metricStore` was added rather than re-rooting the first, because moving the DLQ store
would change unrelated behaviour. When it is absent the endpoint answers `503`, which is not in §6.1's
table and should be added to it.

### Part two: the endpoints table has no percentile, and the spec did not notice

`fetchAllEndpointsData` in `dashboards/app.js` queries with **no time granularity at all** — it
aggregates the entire window, grouped by `path_template` and `method`, to fill the per-endpoint table.
Its P50/P95/P99 columns were therefore `max` over the whole selected range, per endpoint: the worst
instance of the defect in the product, and the one §5.4 does not mention, because §5.4 is written in
terms of granularity being "coarser than one minute" and this query has no granularity to be coarse.

`GET /api/v1/percentile` cannot replace it. The endpoint filters by `path_template` but has no
`group_by`, so answering a table of forty endpoints would take forty requests. Adding a `group_by` is
a real interface change and belongs in a spec, not in an implementation note.

What ships instead: those three cells render `—` with a tooltip saying why, and the column headers are
no longer sortable, because a sort over absent values is a control that does nothing. An empty cell a
user asks about is better than a plausible cell they believe — the same argument §5.2 makes for `min`
over `max`, taken one step further where no number is available at all.

**For the semantic modeller and the next spec:** `GET /api/v1/percentile` needs a `group_by` parameter
restricted to the three declared dimensions, returning one series per group. That is GRVX-809's natural
home and it is the only thing standing between this dashboard and a correct per-endpoint p95.

### Part three: two references the model would have died on

Not a spec defect — an existing bug the spec's change exposed, recorded here because it is what the
text-level acceptance criteria could not see:

`preAggregations.endpointDaily` and `metricsHourly` both listed `p50Latency, p95Latency, p99Latency`.
Removing those measures per §5.2 left two pre-aggregations referring to members that no longer exist,
which Cube rejects at model load. Worse, had they been renamed rather than removed, a pre-aggregation
rolling `bucketP95LatencyMs` up to `day` would have **materialised and cached** min-of-1440-p95s — the
defect this spec removes, stored as if it were a fact.

Both pre-aggregations now carry only `requestCount` and `errorCount`. The fix is enforced by
`cube/model/schema/RequestMetricsMinute.test.js`, which loads the model rather than grepping it.

### Part four: the spec's file list is missing the alert evaluator

§4.2 names four files to modify. It does not name `services/gateway/gateway_alerts.go`, which held:

```go
"p95_latency": "RequestMetricsMinute.p95Latency",
```

Removing that measure per §5.2 would have left every latency alert querying a member that no longer
exists — so latency alerting would have stopped, silently, as a side effect of a correctness fix. That
is worse than the defect being fixed.

It is also the same defect: `queryCubeMetric` aggregates over the whole alert window with no
granularity, so a rule on "p95 over the last 15 minutes" was firing on **max of fifteen per-minute
p95s**. An alert is a number someone is paged by, which makes it the worst place in the product to be
94% out.

The evaluator now answers `p50_latency`, `p95_latency` and `p99_latency` by calling
`computeWindowPercentile` in-process — the same merge, the same error bound, no HTTP hop — and keeps
`error_rate` and `throughput` on Cube, where they aggregate correctly. The anomaly path, which compares
one hour against the same hour on previous days, takes the same route at hourly granularity; the
statistics that judge the comparison were extracted so both paths share them exactly.

Proven by `TestAlertPercentileComesFromSketches`, which points the evaluator at an unreachable Cube so
a regression fails loudly rather than returning the old number, and by
`TestAlertNonPercentileMetricsStillUseCube`, which checks every metric that passes rule validation can
be answered by one path or the other.

**For the spec author:** §4.2 should have listed every consumer of a measure it removes. A spec that
deletes a public name owes the implementer the list of things that read it.

---

## SD-010 — GRVX-809 wires a minute-grained panel to hour-grained charts, and its "reproduce" command omits the tenant

**Spec:** GRVX-809 — Dashboard lineage UI
**Raised by:** `frontend-engineer` during implementation
**Severity:** one unreconciled grain mismatch; one command that does not reproduce what it claims to

### Part one: the panel explains a minute, the chart draws an hour

§5.3 says:

> A chart point or table cell showing a metric value is focusable and activates the panel on click.

But `pkg/lineage.Explain` matches a bucket **exactly**, at minute grain, and
`fetchCubeData` in `dashboards/app.js` requests `granularity: "hour"`. So a user clicking the point
labelled 14:00 — an aggregate over sixty minute buckets — would be shown the provenance of the single
14:00 minute row, whose `request_count` is roughly a sixtieth of the number they clicked.

The spec reconciles these nowhere. Three ways out, and why two were rejected:

1. **Change the chart to minute granularity.** Forbidden by §3: *"Do NOT change any chart's data
   source."* It would also draw 1,440 points for a day.
2. **Aggregate lineage over a period.** That is a new capability, not a UI change: the manifest, the
   digest and the fact list are per partition, and "the provenance of an hour" would have to be
   defined before it could be assembled. It belongs in a spec of its own.
3. **Say so.** Shipped. When the clicked period is wider than the grain, the panel opens with a
   `role="status"` note: *"You clicked a value covering one hour. Provenance is recorded per minute, so
   what follows explains the minute at the start of it — its numbers will be smaller than the one you
   clicked."*

Option 3 is the only one available that does not lie, and it is consistent with what the rest of this
phase does: when a number cannot be given exactly, say exactly how it falls short rather than rounding
the difference away. `renderGrainNote` implements it and two tests pin it.

**What GRVX-810 or a follow-up should decide:** whether lineage over a period is a thing Gravix
offers. If it is, the honest shape is probably a list of the constituent partitions with their digests,
not a merged pseudo-manifest.

### Part two: `recompute_cmd` cannot reproduce a tenant's partition

`pkg/lineage`'s `recomputeCommand(rollup, day)` emits:

```
gravix recompute --metric request_metrics_minute --from 2026-09-09 --to 2026-09-10
```

with no `--tenant`. The CLI supports a repeatable `--tenant` flag, and `Query.TenantID` is right there
in the call — it is simply not used. In a multi-tenant deployment the command as printed rebuilds the
tenant-less partition, which in that deployment does not exist.

So the panel's **"Reproduce this number"** button — the point of the section, and of the feature —
copies a command that reproduces something else. The saving grace is that it fails visibly rather than
quietly: there is nothing at the tenant-less path to rebuild, so the operator gets "nothing to do"
rather than a different number. That is the difference between a bug and a correctness incident, and it
is luck rather than design.

**Not fixed here.** §4.3 lists `pkg/lineage/**` as do-not-touch, on the grounds that assembly is settled
by GRVX-807. This is a one-line change inside that file and improvising into a file the spec explicitly
fences off is how implementation drift starts. It needs a two-line spec of its own, or GRVX-810's
correctness suite should catch it and fix it as part of that work.

### Part three: the gateway image did not contain the contracts

Not a spec defect — a deployment fact the spec's file list did not reach. `services/gateway/Dockerfile`
copied only the binary into the final stage, so `lineage.Explain` would have found no contracts
directory and every request would have been a 500 in the only deployment that exists. One `COPY` line,
added. Contracts are source, not data: they ship with the binary.

The same file labelled the image `org.opencontainers.image.licenses="MIT"`, as did the ingestion,
rollup and load-generator images. The repository has been Apache-2.0 since GRVX-701. An image that
misstates its own licence is a licence-boundary defect regardless of which licence it names, so all four
were corrected.

### Part four: two layout defects the CSS could not show

Found by rendering the page in headless Chromium at 1440px and 400px in all three theme states, not by
reading the stylesheet. **Neither made the page scroll horizontally**, which is what makes them worth
recording: `document.scrollWidth > clientWidth` — the obvious test for AC-11 — returned false in both
cases while the right-hand side of the panel was simply not on screen.

1. **The dashboard grid overflowed its container, and had done before this spec.** At ≤1024px
   `.grid` used `grid-template-columns: 1fr`, and a `1fr` track carries an implicit `min-width: auto`,
   so it grows to its widest card's min-content size. Measured at a 400px viewport: a **524px track
   inside a 368px grid** with the lineage panel hidden, 742px with it shown. `#main-content` has
   `overflow-x: auto`, so the excess was absorbed there with no visible scrollbar — content lost, page
   apparently fine. Now `minmax(0, 1fr)`, plus `min-width: 0` on `.card`. The 524px half is a
   pre-existing bug in every card at phone width; the difference above it was this panel's.

2. **Flex and grid children will not shrink below their content.** The warning region's text and the
   definition list's values (a content digest is 71 unbroken characters) both needed `min-width: 0`,
   and the values column needed `minmax(0, 1fr)` rather than `1fr`.

Three tests now pin these, and the render harness checks two things a CSS assertion cannot: that no
element renders past the viewport's right edge, and that nothing outside a scroll container has
`scrollWidth > clientWidth`.

### Part five: the page had been throwing on every load

`<script src="app.js">` sat above the quick-start wizard, the feedback panel and the cookie banner in
`index.html`. A classic script blocks the parser where it sits, so
`document.getElementById('wizardClose')` was `null`, and the resulting `TypeError` aborted the rest of
that block — roughly 1,700 lines of `app.js` that had never run. Pre-existing; found because the render
harness records console errors, and "zero console errors" is in this spec's definition of done.

Fixed by moving all five script tags to the end of `<body>`, which is where they belonged.

---

## SD-011 — GRVX-811 names a type it never defines, asks for a rollback the repository cannot do, and leaves a burn-rate rule nowhere to record its kind

**Spec:** GRVX-811 — SLO engine and error-budget burn-rate alerts
**Raised by:** `senior-engineer` during implementation
**Severity:** one undefined type; one untestable acceptance criterion; one schema gap worked around

### Part one: `MetricQuerier` is used twice and defined nowhere

§5.1 gives two signatures that take it:

```go
func Evaluate(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Status, error)
func EvaluateTiers(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Tier, error)
```

and never says what it is. §6.3 and §6.4 constrain it — availability comes from `request_count` and
`error_count`, latency from the merged sketch — so the shape is inferable, and the smallest interface
that satisfies both is one method returning minute buckets.

Defined that way, deliberately, with `slo.Bucket` carrying only the four aggregate fields. **The engine
cannot reach a fact even if someone later wants it to**, which makes "no per-request querying"
(docs/04-non-goals.md §5) a property of the types rather than a rule someone has to remember. A wider
interface would have been easier to write against and would have made the guarantee a promise again.

### Part two: AC-12 asks for a rollback this repository has never supported

> AC-12 | Both migrations apply and roll back on SQLite and Postgres

**There are no down migrations anywhere in this repository** — not for SLOs, not for any of the seven
versions that came before. `pkg/tenantdb/migrate.go` embeds `migrations/*/\*.sql` and applies them
forward; nothing reads a `.down.sql` because none exists.

So half of AC-12 cannot be satisfied without first building a rollback mechanism, which is a change to
the migration runner and its own piece of work. What is tested instead is everything that can be:

- both migrations apply, which is implied by every gateway test running against a migrated database;
- the CHECK constraints they declare are enforced, so a direct writer cannot store what the API would
  refuse;
- both files declare the same constraints, and the Postgres one uses Postgres types — `DOUBLE
  PRECISION` rather than `REAL`, which is four bytes there and cannot hold 0.999 exactly.

**For the engineering lead:** either add down migrations as a general capability and restore this
criterion, or drop "and roll back" from the spec template. Asking for it once per spec while the
runner cannot do it means every implementer either lies or writes this paragraph.

### Part three: a burn-rate rule has nowhere to record which SLO it watches

§6.5 says to add `burn_rate` as a rule type "in the existing evaluator, reusing its cron, its
notification dispatch and its deduplication". That was straightforward. What the spec does not address
is that a burn-rate rule needs to name an SLO, and `tenantdb.AlertRule` has no field for one.

A rule's `Service` names the service. The SLO *kind* — availability or latency — has nowhere to go, and
a service may have one of each. The implementation reuses the unused `PathTemplate` field to carry it,
defaulting to availability when empty.

**That is a compromise and it should not survive.** Reusing a field for an unrelated purpose is how a
schema becomes unreadable, and a reader of `AlertRule` has no way to know that `PathTemplate` means
something different on one rule type. The clean fix is a nullable `slo_id` column on `alert_rules`,
pointing at the SLO directly, which also removes the lookup-by-service-and-kind entirely. It needs a
migration and therefore a spec.

### Also worth recording: the tier table's numbers are derived, not chosen

`TestDefaultTiersMatchTheSpecTable` checks each threshold against the fraction of a 30-day budget it
consumes over its long window — 14.4× for an hour is 2%, 6× for six hours is 5%, and so on. The table
in §5.2 gives both columns, and they agree.

That is worth a test rather than a comment because the two halves can drift: someone tuning a threshold
down to reduce noise would leave the "budget consumed before firing" column saying something false, and
the column is what a reader uses to decide whether the tier is reasonable.

---

## SD-012 — GRVX-812 §8.2 greps for a phrase that §5.1 requires to be broken across two lines

**Severity** low — caught before the verification record was written, resolved without a product decision.
**Resolution** §5.1 wins; §8.2's command is corrected in place below.

§5.1 fixes the final output block, and says of the last paragraph:

> The concession paragraph is mandatory and verbatim.

The block it fixes wraps that paragraph like this:

```
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
```

§8.2 then verifies the concession with:

```bash
./scripts/prove_it.sh | grep -c "Datadog's distribution metrics are mergeable"
# expect: >= 1
```

`grep` is line-oriented, and the spec's own wrap puts `Datadog's distribution` at the end of one line
and `metrics are mergeable` at the start of the next. The command returns `0` against the exact output
the same spec mandates. The two sections are not merely inconsistent — they are mutually unsatisfiable:
reproducing the paragraph verbatim guarantees the check fails, and passing the check requires changing a
paragraph declared verbatim.

**Resolved in favour of §5.1**, because that requirement is explicit, load-bearing and labelled
mandatory, while §8.2 is a convenience command. The verification record runs the whitespace-normalised
equivalent, which is what `TestProveItConcedesDDSketch` (AC-5) already did:

```bash
./scripts/prove_it.sh | tr -s '[:space:]' ' ' | grep -c "Datadog's distribution metrics are mergeable"
```

**Note for future specs.** Six of the eight §8 commands in this spec are `grep` over wrapped prose. Any
verification that greps human-formatted output should either normalise whitespace or match a fragment
short enough to survive a wrap. The corresponding Go test had this right from the start; the shell
command in the spec did not, and only the shell command is what a reader runs by hand.

---

## SD-013 — GRVX-901's `dashboard_config.js` cannot pre-configure the dashboard's API key, because nothing reads it

**Severity** high when filed; **largely resolved by GRVX-902**, which adds the missing consumers.
**Status** partly closed. See the update at the end of this entry.

§1 promises a stack "whose dashboard is pre-configured with a working API key". §5.1 fixes the file
that is supposed to deliver it:

```js
window.GRAVIX_CONFIG = {
  ingestionApiUrl: "<IngestionURL>",
  gatewayUrl: "<GatewayURL>",
  apiKey: "<plainKey>"
};
```

`dashboards/app.js:6-12` merges `window.GRAVIX_CONFIG` over five defaults, and those five are the
only fields anything reads:

```
GRAVIX_CONFIG.cubeApiUrl
GRAVIX_CONFIG.gatewayUrl
GRAVIX_CONFIG.percentileApiUrl
GRAVIX_CONFIG.refreshIntervalMs
GRAVIX_CONFIG.staleThresholdMs
```

So of the three fields §5.1 mandates, one works and two are inert:

| Field | Effect |
|---|---|
| `gatewayUrl` | real — consumed at `app.js:15` |
| `ingestionApiUrl` | **none** — no consumer anywhere in `dashboards/` |
| `apiKey` | **none** — the dashboard reads its key from `localStorage.getItem('gravix_api_key')` (`app.js:851`, `:2735`, `:2941`, `:2994`) |

The dashboard's key has never come from configuration. It comes from browser storage, populated by
the onboarding flow. Writing `apiKey` into `window.GRAVIX_CONFIG` changes nothing about what the
dashboard sends, so a user following this spec still reaches a dashboard that cannot query.

**Why this is not fixable in implementation.** The three ways out are all product decisions:

1. **Teach `app.js` to read `GRAVIX_CONFIG.apiKey`**, falling back to localStorage. Cleanest, but
   §4.3 explicitly fences `dashboards/app.js` — and the fence is right, because this changes the
   dashboard's auth precedence for every deployment, not just bootstrap.
2. **Have `dashboard_config.js` seed localStorage directly.** Works without touching `app.js`, but
   the file's contract in §5.1 is "set `window.GRAVIX_CONFIG`", not "write to browser storage", and
   it silently overwrites a key a user may have entered by hand.
3. **Leave the dashboard unauthenticated** against Cube in bootstrap mode and drop the claim.

**A security note that applies to all three.** `dashboard_config.js` is bind-mounted into the nginx
web root, so whatever it contains is served at `GET /dashboard_config.js` to anyone who can load the
dashboard. Today that publishes a live ingestion key — which grants *write*, so a reader could inject
false facts, an escalation over the read access they already have by seeing the page. On a localhost
self-host this is close to a non-issue; on a dashboard exposed to a network it is not. Option 2 makes
this worse by design. `security-engineer` should rule before any of the three ships.

**Implemented as specified meanwhile.** `provision` writes all three fields, because AC-3 requires
the key to be in the file and deviating would improvise in the opposite direction. The field is inert
rather than wrong, and when the decision lands only the consumer side changes.

### Update, 2026-09-11 — GRVX-902 supplies the consumers

GRVX-902 §4.2 adds `ingestionApiUrl` and `apiKey` to the `GRAVIX_CONFIG` defaults in
`dashboards/app.js`, and §5.3 adds `ingestionFetch`, which sends `X-API-Key` from
`GRAVIX_CONFIG.apiKey`. Both fields now have a reader. The spec sequence intended this all along —
GRVX-901's §4.3 fenced `app.js` because GRVX-902 owns it — so the defect was in the two specs being
read one at a time, which is the working rule, not in either spec alone.

**What is fixed.** The generated config now reaches ingestion: `ingestionFetch` is the path by which
the dashboard asks "is my data arriving?" and it authenticates with the generated key. Proven by
`TestServicesEndpointRequiresAuth`, which exercises the real middleware chain.

**What is still open, and is the part that always needed a person:**

1. **The dashboard's *Cube* queries still read their key from `localStorage`.** `ingestionFetch`
   covers the ingestion API only. Nothing in this pair of specs made `GRAVIX_CONFIG.apiKey` the
   source for the rest of the dashboard, so §1's "pre-configured with a working API key" is true for
   the services endpoint and not for the metric charts.
2. **The key is still served over HTTP.** `dashboard_config.js` is in the nginx web root, so
   `GET /dashboard_config.js` returns a live ingestion key to anyone who can load the dashboard —
   which grants *write*, so a reader could inject false facts. On a localhost self-host that is close
   to a non-issue; on an exposed dashboard it is not. `security-engineer` should rule on whether the
   bootstrap key should be ingest-scoped, or the config served with a narrower key than the one in
   `api_key.txt`.

Item 2 is the reason this entry stays open rather than being marked fixed.

---

## SD-014 — GRVX-902 records batch facts before they are persisted, and single facts after

**Severity** low — a discovery counter, not a billing figure.
**Status** implemented as specified, documented in code and in the OpenAPI description.

§6.4 and §6.5 place the same call on opposite sides of the write:

| Path | Where `RecordFact` is called |
|---|---|
| `handleFacts` (§6.4) | "immediately after the successful `sink.Write` call" |
| `handleBatchFacts` (§6.5) | "in the same loop iteration that appends to `validRecords`" — the loop runs *before* `sink.WriteBatch` |

So if `WriteBatch` fails, the handler returns `500` and the client retries, but every service in
that batch is already counted. Retry and they are counted again. The single-fact path cannot do
this: nothing is recorded unless the write succeeded.

**Implemented as written.** The divergence is small and the registry is a "what exists" aid rather
than an accounting record — but in a project whose thesis is correctness, an unstated discrepancy
between two paths computing the same thing is the sort of thing that is discovered later by someone
comparing two numbers. So it is stated: in a comment at the call site, and in the `request_count`
description in `docs/openapi.yaml`, which says the figure is not reconciled against stored facts and
that a retried batch can count twice.

**If it is ever worth fixing**, the fix is to collect the services during the loop and record them
after `WriteBatch` returns, matching §6.4. That is a two-line change; it was not made here because
choosing different semantics from the ones a spec states is how implementation quietly becomes
product design.

---

## SD-015 — Phase 9's "one alert rule armed" exit criterion has no owning key result

**Filed under GRVX-904 §10**, which names this exact condition and says to return the escalation
rather than invent the missing KR.

`SPEC DEFECT: docs/oss/12-goal-tree.md — Phase 9 exit criterion "one alert rule armed" has no
owning KR`

**Confirmed, not assumed.** G3's key results are:

| KR | Covers |
|---|---|
| G3.1 | clone → populated dashboard ≤10 min |
| G3.2 | 0 required config steps before first data |
| G3.3 | services auto-discovered |
| G3.4 | default SLO dashboard per service |
| G3.5 | `gravix doctor` diagnoses the top 10 failures |
| G3.6 | path templates auto-learned within the cardinality budget |
| G3.7 | every empty state contains the command that fills it |

None mentions alerting. GRVX-904's header records the same gap — its **Goal** field reads "no
individual G3 KR names this" — so the spec was written knowing it was building toward an unowned
criterion.

**Why the KR was not simply added.** The goal tree is product: a key result fixes a number, a
measurement source and an accountable role, and choosing those is the CPO's call, not the
implementer's. GRVX-904's Definition of Done offers exactly this either/or — "gains a KR … **or the
escalation is filed instead**" — and filing is the branch that keeps product decisions out of
implementation.

**The implementation is complete and verified regardless.** This defect is about the goal tree, not
the feature: nine acceptance criteria pass, and the arming path is proven end to end.

**What a KR would need to say**, if one is added: the measurable thing is not "an alert rule exists"
but "a rule armed from a proposal fires correctly on the traffic it was derived from". A rule that
is armed and inert is worse than none, which is the failure this spec's own guard now catches —
`TestEvaluatorFiresRulesOnALogChannel`. A plausible G3.8 is *"A user can arm a working alert rule
without choosing a threshold or configuring a destination — target 100%, measured by
`TestArmProposalCreatesRuleAndChannel` and `TestEvaluatorFiresRulesOnALogChannel`, owned by
`senior-engineer`"*, but the number and the owner are the CPO's to set.

---

## SD-016 — the cardinality budget is per-process, and the shipped production chart runs ingestion at 2–10 replicas

**Filed under GRVX-905 §10**, third row, which names this exact condition.

`SPEC DEFECT: §3 — non-goal "in-memory only" breaks under multi-process ingestion`

**Severity** medium. The feature works and is a large improvement on unbounded cardinality; what
does not hold is the guarantee §1 states — "no amount of naive instrumentation can create unbounded
dimension cardinality" — at the bound the spec claims.

**Confirmed against the chart, not assumed.** `deploy/gravix/templates/hpa.yaml` targets the
**ingestion** deployment, and:

| values file | `autoscaling.enabled` | replicas |
|---|---|---|
| `values.yaml` (default) | false | 1 |
| `values-dev.yaml` | false | 1 |
| `values-prod.yaml` | **true** | **2–10** |
| `values-production.yaml` | **true** | **2–10** |

Each replica holds its own `pathlearn.Learner`, and nothing is shared. So under the shipped
production configuration:

- **The template budget multiplies.** 200 per service per process becomes up to **2,000** across ten
  replicas — over the "< 1000 unique values per day" limit in `docs/04-non-goals.md` §5, which is the
  very number §3 says the 200 was chosen to sit comfortably under.
- **The segment budget weakens.** With traffic spread across ten replicas, each sees roughly a tenth
  of the distinct values at a position, so a genuinely dynamic segment needs ~10× more distinct
  values before *any* replica collapses it — and replicas disagree about whether it is collapsed, so
  the same raw path becomes two different templates depending on which pod served it.

The second effect is the more insidious: it does not merely loosen a bound, it makes the output
non-deterministic. Two identical requests can produce `/shop/boots` and `/shop/{param}`.

**Why this was not fixed here.** §3 declares in-memory-only a deliberate simplification and forbids
persisting the counters, so the fix is outside this spec by construction. It is also a real design
choice with costs — sharing this state means either a round trip per fact on the ingestion hot path,
or accepting staleness, and both deserve deciding rather than assuming.

**Options, none of them free:**

1. **Shard by service at the load balancer**, so all facts for one service reach one replica. The
   budget then holds exactly. Costs an ingress-level routing rule and uneven load.
2. **Move the counters into the discovery SQLite registry**, which already persists per-service
   state. Correct across restarts too, but puts a database write on the fact path — the thing
   `pkg/discovery`'s batching exists to avoid.
3. **Divide the budget by replica count** and pass it in as configuration. Cheapest, keeps the total
   bound honest, and costs nothing at runtime — but over-collapses when replicas are idle.
4. **Accept it and document it**, lowering the published claim from "cardinality is bounded" to
   "cardinality is bounded per replica".

**Meanwhile the limitation is visible rather than hidden**: the package doc for `pkg/pathlearn`
states it, and so does the comment where the `Learner` is constructed in `services/ingestion/main.go`.
A single-replica deployment — the default, and every self-hoster following the bootstrap path — has
the exact bound the spec claims.
