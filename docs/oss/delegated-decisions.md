<!-- Append-only. One entry per decision. Reversing a decision is a new entry, not an edit. -->
# Decisions made under delegation

On 2026-10-01 the project owner delegated the open decisions in
[`open-decisions.md`](open-decisions.md) with one instruction: where a recommendation can be made,
make it and act on it; stop where one cannot.

This file is the audit trail for that delegation. Each entry names the options that were on the
table, the one chosen, why, and what reversing it would take. A reader who disagrees with an entry
should be able to undo it from this page alone.

## What the delegation does and does not cover

It covers what one maintainer may decide alone: routine-tier changes under
[`GOVERNANCE.md`](../../GOVERNANCE.md) — bug fixes, documentation, tests, and the implementation or
correction of an approved spec — and product calls the registers assigned to the owner or the CPO.

It does **not** cover anything the governance documents require more than one person for. A new
extension point, public API or schema change is design tier and needs two maintainer approvals; a
charter change needs the CPO and the License & Boundary Auditor. One person's delegation cannot
supply a second person's approval, so those items are listed at the end as stops, not decided.

It also does not cover verification that a spec reserves for someone other than the implementer,
such as GRVX-1002's pricing audit, or anything that needs a third party.

---

## DD-001 — SD-021: the at-scale cost caveat states the computed multiple, never a fixed one

**Date** 2026-10-01 · **Tier** routine (spec correction) · **Spec** GRVX-1004 §5.2

**Options.** Keep the mandated "roughly 10x" sentence with a computed correction beside it (the
shipped workaround); state the multiple as a range; or drop the fixed multiple and print only the one
computed from the figures on screen.

**Chosen.** Drop it. The multiple is ~44× at a million events a month and ~4× at a billion, so no
single figure is right and a range still invites budgeting from the wrong end. The page already
computes the real one.

**Done.** `CaveatAWSSingle` and its JS twin no longer quote a multiple; the computed-multiple caveat
no longer refers back to one. `TestNoCaveatQuotesAFixedMultiple` and its JS twin fail on any
"roughly/about/around/approximately N×" in a caveat, and a mutation restoring the old sentence fails
them. GRVX-1004 §5.2 amended in place.

**To reverse.** Restore the old constant in `pkg/costmodel/costmodel.go` and `dashboards/lib/tco.js`
and delete the two guard tests. Not recommended: it reintroduces a four-fold under-budget at the
volume the sentence addresses.

## DD-002 — SD-019: GRVX-910 §2 names the JWT secret, not `CUBEJS_API_SECRET`

**Date** 2026-10-01 · **Tier** routine (spec correction) · **Spec** GRVX-910 §2

**Options.** Leave the stale sentence with the register entry beside it, or correct it.

**Chosen.** Correct it. The behaviour has been right since F-016 and F-029; only the spec text
misled. §2 now says the `load` endpoint is authenticated by `checkAuth` against the secret in
`JWT_SECRET_FILE`, and that the poll authenticates the way the dashboard does.

**To reverse.** Not meaningful; the old text was factually wrong.

## DD-003 — SD-022: GRVX-1005 §5.3 ranks the unit tests above the kill test

**Date** 2026-10-01 · **Tier** routine (spec correction) · **Spec** GRVX-1005 §5.3

**Options.** Leave §5.3 claiming the kill test is "the only test that actually proves §6", or invert
the ranking as measured.

**Chosen.** Invert it. Against a batcher mutated to acknowledge before fsyncing, the kill test
passed three runs of three and both unit tests failed. §5.3 now requires all three and says which
one enforces fsync ordering.

**To reverse.** Not meaningful; the old text was contradicted by measurement.

## DD-004 — SD-026: the bare-Parquet guide publishes one query, and it works on real data

**Date** 2026-10-01 · **Tier** routine (spec correction and docs) · **Spec** GRVX-1101 §5, §6

**Options.** Lead with GRVX-1101's `part-0.parquet` query (matches only the test fixture); lead with
the `*.parquet` query and keep both; or fix the fixture to write production file names and publish
only `*.parquet`.

**Chosen.** The third, which SD-026 itself called the cleaner fix. A fixture that does not
reproduce production file names cannot prove a published path works, and a page that publishes a
query it then tells you not to use spends the reader's attention on a test artefact.

**Done.** The fixture writes `request_metrics_minute_<YYYYMMDD>.parquet` (taken from
`pkg/recompute.DeterministicKey`) and `events_<uuid>.parquet`. The guide publishes the `*.parquet`
query alone. `TestBareParquetProductionFilenameGlob` now asserts the fixture's file name equals the
rollup's, renames the files to compaction's shape and re-runs the published query, and fails if the
guide names `part-0.parquet`. A mutation reverting the fixture name fails it.

**To reverse.** Revert the fixture's `FixtureFilename`, the guide's query section and the test.

## DD-005 — SD-015: Phase 9's "one alert rule armed" gets an owning key result, G3.8

**Date** 2026-10-01 · **Tier** routine (goal tree; assigned to the CPO) · **Doc** `12-goal-tree.md`

**Options.** Leave the exit criterion unowned; add a KR that measures "a rule exists"; or add a KR
that measures "an armed rule fires".

**Chosen.** The last, worded as SD-015 proposed: *an alert rule armed from a proposal, with no
threshold chosen and no destination configured, fires on the traffic it was derived from* — 100%,
measured by `TestArmProposalCreatesRuleAndChannel` and `TestEvaluatorFiresRulesOnALogChannel`, owned
by `senior-engineer`. A rule that is armed and inert is worse than none, so "exists" would measure
the wrong thing.

**To reverse.** Delete the G3.8 row and its note. A CPO who wants a different owner or target edits
the row; the measurement sources are the part that should survive.

## DD-006 — F-039: compaction never rewrites `request_metrics_minute`

**Date** 2026-10-01 · **Tier** routine (bug fix) · **Code** `transforms/compaction`

**Options.** F-039 offered two: compaction preserves and merges `latency_sketch` (correct sketches,
larger files), or compacted days are documented as scalar-only with percentiles dropped. Averaging
percentiles, what the code did, was ruled out by the finding itself.

**Chosen.** Neither: compaction refuses the metric table. `pkg/recompute` writes one deterministic
file per partition, so there is nothing to compact; an exact per-bucket percentile over a union of
files needs the facts, which only recompute reads; and F-003 showed the metric path never reached a
partitioned warehouse anyway. Making an unreachable path merge sketches would have been more code
for no user, and it would have left two writers for one table.

**Done.** The twelve-column struct and `mergeMetricRows` are deleted. `ErrMetricsNotCompacted`
names `gravix recompute` as the way to rebuild a partition. A test seeds sketch-bearing metric files,
asserts the refusal and asserts the files are byte-identical afterwards; removing the refusal fails
it. The guide's warning about compacted partitions is replaced by the guarantee.

**To reverse.** Restore the metric branch from git history, and then fix both halves of F-039 in it
before it runs: merge sketches, and include `user_agent_family` in the merge key.

## DD-007 — CD-005: every Parquet writer uses `recompute.CompressionLevel`

**Date** 2026-10-01 · **Tier** routine (bug fix) · **Code** five writers, one guard test

**Options.** Every writer at `SpeedFastest` (recompute's pinned level), or every writer at
`SpeedDefault` (about 3% smaller files).

**Chosen.** `SpeedFastest`. Changing recompute's constant changes every content digest already
recorded and, under GRVX-801 §5.3, needs determinism re-verified; moving the other writers changes
nothing anyone has recorded. Three percent of a warehouse that is already a few bytes per event is
not worth a broken digest.

**Done.** Compaction's event tables, both service-events transforms, the importer and the exporter
now take their level from `recompute.CompressionLevel`. `TestEveryParquetWriterUsesTheRecomputeLevel`
fails on any non-test zstd codec at another level.

**To reverse.** Change the one constant in `pkg/recompute` and re-run GRVX-801's determinism suite;
every writer follows it.

## DD-008 — SD-024: no pre-aggregations; warm means a result-cache hit

**Date** 2026-10-01 · **Tier** routine (spec correction and implementation) · **Spec** GRVX-1006

**Options.** SD-024 listed three. Add a Cube Store container to the bootstrap stack, which F-035
rejected and which would raise GRVX-1004's published cost. Run Cube in dev mode, which embeds a Cube
Store but makes a development flag decide production performance. Or drop the pre-aggregations and
meet the target reading Parquet directly.

**Chosen.** The third, after measuring it on the bootstrap stack's own limits with a real Cube. Warm
p95 is 51–100 ms through Cube's in-memory result cache. Cold p95 is 193–593 ms for one day and up to
3.7 s for a week, which is published rather than hidden. The choice keeps the single-container
premise, the cost figure and the production configuration all intact, and it is the only option
whose number was measured rather than assumed.

**Done.** `RequestMetricsMinute` prunes by the query's date range and declares a refresh key read
from the Parquet footers; `timestampSql` casts on both engines, which also fixed F-053. Five tests
guard it, each mutation-tested. GRVX-1006 is amended and moves to `partial`.

**Not done.** The cache warmer, the `bench/query` driver that would make the warm figure a CI number,
and the percentile endpoint's figure. The cold path past one day needs `bucket_start` stored as a
timestamp, which is a design-tier schema change and is listed under the stops below.

**To reverse.** Choosing option 1 or 2 later needs no undo: declare the rollups and add the store.
The pruning and refresh key are independent of that and worth keeping either way.
