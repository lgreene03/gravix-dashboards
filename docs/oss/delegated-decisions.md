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

## DD-009 — SD-023 and SD-056: one batcher per topic file, wired into every write

**Date** 2026-10-01 · **Tier** routine (implementing an approved spec) · **Spec** GRVX-1005

**Options.** SD-023: one `Batcher` per (tenant, topic) file, or one fronting every file. SD-056:
leave the batcher unwired until a reference machine exists, or wire it now.

**Chosen.** Per-file, and wire it now. SD-023 credited the shared design with better amortisation on
a multi-tenant node; it has none, because fsync is per file and both designs make one sync per file
per batch window — the shared one just makes them in sequence. SD-056 held the wiring back because
it could not be measured and rotation looked risky. Both were answered with tests rather than
assumed: a rotation stress test and a before-and-after throughput measurement.

**Done.** Every `DurableSink` write goes through its topic's batcher, which writes and fsyncs under
the lock rotation takes. Full queues answer 503 with `Retry-After: 1`. A hang in `Append` racing
`Close` was found and fixed. Throughput at the sink on a 4-core machine went from 3,875 to 21,801
facts/sec with 64 concurrent writers. Six tests, each mutation-tested.

**Cost, stated.** A lone request now waits up to `MaxBatchDelay` (2 ms) for peers before its fsync.
That is GRVX-1005 §5.1's own trade, and `TestTailLatencyBounded` holds it to that bound.

**To reverse.** Make `WriteBatch` call `appendAndSync` directly; the batcher, the tests of its
properties and the overload response all stand on their own.

## DD-010 — SD-013: the dashboard is served a read-only key, and the write key never reaches the browser

**Date** 2026-10-01 · **Tier** routine (security fix to an approved spec) · **Specs** GRVX-901, GRVX-907

**Options.** Keep serving the write key (simplest first run, and the status quo); restrict the served
key and drop the pasteable command; or restrict the served key and keep the command by having it
fetch the write key from the running stack.

**Chosen.** The third. The served key turned out to be fully unrestricted, because no code had ever
written the scopes column, and the file it sits in is served before login. That made it an
authentication bypass on any reachable dashboard, including the VPS deployment GRVX-1004 prices.
SD-013 asked for `security-engineer` to rule; least privilege on a credential served
unauthenticated needs no special judgement, and the delegation covers it.

**Done.** `pkg/tenantdb.Restrict`; a second, `admin:read` key in `dashboard_config.js`; an
`apiKeyCommand` field that the empty states turn into an `export` line; and an upgrade step that
replaces a served write key on the next boot. GRVX-901 §5.1 and AC-3 amended.

**Cost, stated.** The empty-state command is two lines instead of one, and it needs `docker
compose` on the machine running the stack, which the bootstrap stack already requires. A deployment
that wants something else sets `-api-key-command`.

**To reverse.** Pass the write key to `dashboardConfig` again. Not recommended without binding the
dashboard to localhost.

## DD-011 — SD-029: one `/exports` family; the job endpoint waits on a destination design

**Date** 2026-10-01 · **Tier** routine (route rename before first release; tests) · **Spec** GRVX-1107

**Options.** Keep `/api/gateway/export` and add `/api/gateway/exports` beside it, documenting the
difference; or move the existing download into the plural family.

**Chosen.** Move it, to `/api/gateway/exports/archive`. Nothing released depends on the singular
path, two specs already named the plural, and a client one character from the wrong endpoint gets
no error to tell it so. The dashboard, the migrate CLI, the OpenAPI document and the `ee/` degrade
guard follow the move, and a test keeps every export route in the family.

**Done.** AC-7, AC-8, AC-9 and AC-12 of GRVX-1107 pass with named tests. Found F-054: scheduled
exports never run.

**Not decided, and why.** §5.4's endpoint lets any role name a write destination, and running
scheduled exports means writing to customers' buckets. Both are security designs. They are on the
stops list below.

**To reverse.** Restore the singular route string in the five places named above.

## DD-012 — F-042: two mechanical readiness checks, gating undispatched specs

**Date** 2026-10-01 · **Tier** routine (process tooling) · **Doc** `11-agent-loops.md`

**Options.** Leave the gate as twelve judgement checks; add the two mechanical checks and run them
over every spec; or add them and gate only specs not yet dispatched.

**Chosen.** The last. Over every spec they would fail on history already recorded in the registers;
on `planned` specs they catch a defect while it costs one edit. They report on everything else, which
surfaced GRVX-1305's wrong path before that spec is dispatched.

**Done.** `scripts/spec_lint.py`, `make spec-lint`, a CI step, two tests, and the gate documentation.

**To reverse.** Remove the CI step; the script is inert without it.

## DD-013 — F-050: correct the published Go install now; leave the module path to the owner

**Date** 2026-10-01 · **Tier** routine (documentation) · **Doc** `findings.md` F-050

**Options.** Leave the pages as they were, with a warning over a command that cannot work; rename
the root module to `lgreene03` now (option 1); or correct the pages to an install that works today
and leave the name decision open.

**Chosen.** The last. The pages documented an API that does not exist, which is a documentation bug
and an implementer's to fix. The module path is the project's canonical name for every future
consumer. Renaming it to a personal account would mean renaming it again when the repository moves to
an organisation, and only the owner can create one.

**Done.** `sdk-go.md` rewritten from the exported API, with an install through `replace` directives
that never fetches the module path. `getting-started.md` and `deployment.md` corrected.
`TestGoSDKDocsUseTheRealAPI` keeps both pages to the real API and module path.

**Not decided, and why.** Which name the root module and the SDK take. That needs the owner to
create an organisation (option 2, recommended) or provision `gravix.io` (option 3).

**To reverse.** Restore the three pages from git and delete the test.

## DD-014 — SD-055: keep the 120-byte target, propose raw compression as RFC 0003, leave §5.2 unapplied

**Date** 2026-10-01 · **Tier** routine (spec correction) for this entry; the compression itself is
design tier · **Spec** GRVX-1003

**Options.** Restate G4.4 to about 210 bytes/event; apply §5.2's Parquet encodings and report the
miss; or keep the target, propose compressing raw facts, and leave the encodings unapplied.

**Chosen.** The last. Raw JSONL is 203.78 of 206.68 bytes/event, and gzip takes the total to 35.59, so
the target is reachable and restating it would hide a cheap fix. The encodings act on 2.89 bytes/event
and cannot move the total. Applying them changes every Parquet file's bytes for no measured benefit.

**Done.** RFC 0003 drafted: compaction writes `.jsonl.gz`, ingestion is untouched, every reader goes
through one function, and a guard test catches a reader that skips compressed files. GRVX-1003 §11.8
records each criterion's state. SD-055 and `open-decisions.md` updated.

**Not decided, and why.** RFC 0003 itself. It changes the stored format of the recompute source of
truth, which is design tier, and needs two maintainer approvals. The project has one.

**To reverse.** Withdraw RFC 0003, and execute GRVX-1003 §6 step 3 from `pkg/encoding`'s table.

## DD-015 — F-003: compaction stays blind to the Hive layout, because nothing there needs merging

**Date** 2026-10-01 · **Tier** routine (finding resolution) · **Finding** F-003

**Options.** Teach `parseWarehouseKey` the Hive layout for the event tables, with the dry-run and
rollback story F-003 asked for; or show that no partitioned table accumulates files and close it.

**Chosen.** Close it. Each event transform writes the day's file and deletes the rest of the
partition, so a partition holds one file after every run. The existing idempotency test in each
transform requires that, and removing the delete fails it. A compaction path for these tables would
be a data-movement change whose every run merges one file into itself.

**Done.** F-003 marked resolved, with the two consequences recorded: GRVX-810's held-open criterion
cannot occur, and compaction's merged-manifest code has no production input.

**To reverse.** Reopen F-003 if any warehouse writer starts appending a file per run instead of
replacing the partition. The idempotency tests are the tripwire.
