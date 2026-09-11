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
