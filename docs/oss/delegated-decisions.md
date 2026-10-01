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

## DD-016 — GRVX-1506: the foundation evaluation becomes final with its `NOT YET` verdict

**Date** 2026-10-01 · **Tier** routine (closing a recorded window) · **Spec** GRVX-1506

**Options.** Leave the evaluation provisional until the owner announces a new window; or mark it
final now that the window has closed.

**Chosen.** Final. The window closed on 2026-09-30, and the repository has no issues at all, so no
comments arrived. The window was never announced outside the repository, so nobody outside was
asked. That matters less here than it would elsewhere: `NOT YET` changes nothing and commits the
project to nothing. A donation would need its own charter-tier RFC and its own 14-day window, and the
evaluation now says that window must be announced.

**Done.** The status row and closing note record the close, the zero comments and the caveat.
`TestCommentWindowOpened` accepts a closed window only when the evaluation records its closing date
and the number of comments. It reads the document, never today's date, so CI cannot change colour
on a day with no commit.

**To reverse.** Restore "not final until the window closes" in the status row and reopen the window
with new dates. Do that if the owner announces the evaluation and comments arrive.

## DD-017 — F-056: the bench measures ingest on every core, with fsyncs at the service's batch

**Date** 2026-10-01 · **Tier** routine (bug fix in the benchmark) · **Finding** F-056

**Options.** Keep one goroutine and stop dividing by the core count; or run one worker per core and
keep the division. Separately, keep skipping fsync and say so; or fsync at the service's batch size.

**Chosen.** One worker per core, with an fsync every 512 facts. "Per core" in G4.3 means a loaded
machine, and one goroutine is only an upper bound on that. Skipping fsync would leave out most of the
cost of a write, and the README already promised a durable buffer. The service's batcher is in
`package main` and cannot be imported, so the bench carries its batch size, and a test pins the two
together.

**Done.** `bench/measure.go`, `bench/main.go`, three tests, and the README's field description.

**To reverse.** Restore the single-goroutine loop. Then also stop dividing by `num_cpu`, or the
figure is wrong again.

## DD-018 — GRVX-1005: the reference machine is a GitHub-hosted `ubuntu-24.04` runner

**Date** 2026-10-01 · **Tier** routine (measurement setup) · **Spec** GRVX-1005, G4.3

**Options.** A maintainer's own machine; a fixed cloud instance type, such as a dedicated 4-vCPU VM;
or GitHub's hosted `ubuntu-24.04` runner.

**Chosen.** The hosted runner. It is the one machine an outsider can use for free, by forking and
running the same workflow, which is what `bench/README.md` says the benchmark is for. A maintainer's
machine cannot be checked by anyone else. A fixed cloud instance is steadier, but it costs money and
needs an account, so fewer people can reproduce it. The runner's noise is real. Each result records
the CPU it got and the spread across runs, and a published figure names its result file.

**Done.** `.github/workflows/bench.yml` runs on demand at any scale, and at small scale on pull
requests that touch `bench/`. `bench/README.md` and G4.3 name the machine.

**Not decided, and why.** Whether the first standard-scale result becomes a regression threshold in
`scripts/perf_baseline.json`. That file says one unreviewed run is not a baseline, and the review
belongs to `perf-cost-engineer`, not to the run's author.

**To reverse.** Name another machine in `bench/README.md` and G4.3, and change the workflow's
`runs-on`.

## DD-019 — F-026: gate every job the summary waits for; gate the Docker jobs once each has passed

**Date** 2026-10-01 · **Tier** routine (CI repair) · **Finding** F-026

**Options.** Add every Docker job to `ci-summary` now, red or not; add none until all pass; or fix
what is diagnosed, gate what is green, and gate the rest after its first green run.

**Chosen.** The last. `docker-build` passed on `main` for all four images, so it is gated now.
`docker-smoke` and `image-scan` failed on configuration, not on Gravix: a missing `.env` variable and a
tag that no longer resolves. Both are repaired, but neither has run past those errors yet, and gating a
job before its first real result would turn `main` red on whatever layer comes next. `vuln`,
`helm-validate` and `docker-lint` were already awaited and simply never checked. That was an oversight,
not a decision, so they are gated without waiting.

**Done.** The `ci-summary` failure condition, the smoke `.env`, the trivy tag, and two guard tests.

**Not decided, and why.** Whether `image-scan` should fail `main` on a HIGH or CRITICAL finding in a
base image. That is the security engineer's call under `10-agent-roster.md`, and it should be made
when there is a first real scan to look at.

**To reverse.** Remove `docker-build` and the three jobs from the failure condition. The guard test
will then fail, by design.

## DD-020 — F-025: port the bootstrap stack's init container, and boot the full stack on pull requests

**Date** 2026-10-01 · **Tier** routine (bug fix) · **Finding** F-025

**Options.** Keep waiting; set `user: root` on the services that write `./data`; or port the
bootstrap stack's one-shot init container.

**Chosen.** Port the init container. F-025 said to port it the moment it was seen working, and it has
worked on every green onboarding run since 2026-09-14. Running the services as root would fix the
symptom by giving up the reason the images run as `gravix`. `docker-smoke` also runs on pull requests
now, because the full stack had no other check before merge.

**Done.** `data-init` in `docker-compose.yml`, the dependencies, a guard test, and the
`docker-smoke` trigger.

**To reverse.** Remove `data-init` and its dependencies. Fresh clones then fail as F-025 describes.

## DD-021 — F-017: report a real p95 from the load generator and gate on it

**Date** 2026-10-01 · **Tier** routine (bug fix) · **Finding** F-017

**Options.** Rename `max_p95_latency_ms` to say "average"; or make the load generator report a p95
and compare that.

**Chosen.** Report a real p95. The threshold is meant to bound the tail, and a renamed average would
still let a heavy tail through. Failed requests now count in every latency figure, because leaving
them out made the numbers improve as the system got worse.

**Done.** The load generator's digest and summary fields, the gate's comparison, two tests, and the
baseline file's comment.

**Not decided, and why.** New threshold values. The old ones were set loose for the average. Setting
real ones needs a measured run on the reference machine, and `perf-cost-engineer` reviews it.

**To reverse.** Compare `avg_latency_ms` again in `perf_test.sh`. The extra summary fields are
harmless.

## DD-022 — F-018: the rollup lock follows the store, and the cron and recompute share it

**Date** 2026-10-01 · **Tier** routine (bug fix) · **Finding** F-018

**Options.** Leave the working-directory lock and document it; derive the lock path from the store
and have both writers take it; or build a lock object in the store with conditional writes.

**Chosen.** Derive it from the store, and make the cron take the same per-tenant locks as recompute.
`Run`'s own comment promised that the two could never write a partition at once, and the code did
not keep that promise in any deployment. This keeps it on one machine without changing the storage
interface.

**Done.** `LockDir`, `AcquireLocks`, `LocalStore.Root`, the cron's lock, the bench clean-up removed,
and four tests.

**Not decided, and why.** A lock that holds across machines sharing a bucket. It needs conditional
writes in the storage interface and an expiry policy, which is a design change for a spec.

**To reverse.** Restore the cron's `leaderelect.NewFileElector(outputDir, …)` and the
working-directory path in `acquire`. Both defects return.

## DD-023 — F-020: size storage from the measured raw figure and a warehouse range

**Date** 2026-10-01 · **Tier** routine (documentation) · **Finding** F-020

**Options.** Keep the estimate-based tables beside the measured figures; scale the tables by the
measured averages; or use the raw figure, which is linear, and a range for the warehouse, which is
not.

**Chosen.** The raw figure and a range. A single warehouse average of 2.9 B would under-provision a
deployment with few events per row by up to seven times. The upper bound costs a few percent of the
total and cannot be short.

**Done.** `docs/capacity-planning.md`'s plan-tier, retention, S3, buffer and upload figures, and the
reference machine's small-scale result committed under `bench/results/`.

**To reverse.** Restore the old tables from git. They over-provision, so reversing is safe but
wasteful.

## DD-024 — F-041: a changelog row in the spec template, and a release gate on the changelog

**Date** 2026-10-01 · **Tier** routine (process tooling) · **Finding** F-041

**Options.** Rely on the release manager to fill the changelog by hand; or put the question in the
spec template and refuse a release the changelog does not describe.

**Chosen.** Both mechanisms. The template is where an implementer learns what to touch, so the
question belongs there. The gate makes the last line of defence mechanical, in the same spirit as
`check-boundary`.

**Done.** The template's §4.2 row and §9 item, `scripts/changelog_check.sh`, its step in
`release.yml`, two tests, and this session's entries under `[Unreleased]`.

**To reverse.** Remove the release step. The template row is harmless on its own.

## DD-025 — F-043: run the Postgres backend's tests in CI against a real server

**Date** 2026-10-01 · **Tier** routine (test infrastructure) · **Finding** F-043

**Options.** Run them, with a Postgres service container; delete them; or document that the backend
is untested.

**Chosen.** Run them. Postgres is the production backend, so its tests are worth the minute of CI.
Skips fail the job, because a skip is exactly how this file stayed silent.

**Done.** The `postgres` CI job, its place in `ci-summary`, a shared `Restrict` check, and the suite
test's rule that a tagged file needs a CI job passing its tag.

**To reverse.** Remove the job, and restore the suite test's known exception for the file.

## DD-026 — SD-027: external metrics require a tenant; legacy single-key mode gets a 400

**Date** 2026-10-01 · **Tier** routine (spec correction and bug fix) · **Spec** GRVX-1102, GRVX-1105

**Options.** Drop the `tenant_id` rule so legacy mode writes to the single-tenant layout; or keep the
rule and refuse legacy-mode writes with a clear 4xx.

**Chosen.** Keep the rule and refuse. The rule is the guard against a multi-tenant bug writing into
the shared layout. Legacy mode is the older configuration, and the shipped stacks all set
`TENANT_DB_PATH`. The old 500 was the worst of both, because Prometheus retries a 500 indefinitely.

**Done.** One shared refusal in both handlers, a test over both endpoints, and the two specs amended.

**To reverse.** Remove the `tenant_id` rule from `ValidateExternalMetricSample` and the refusal from
both handlers. Legacy-mode samples then land in `external_metrics` beside single-tenant facts.

## DD-027 — SD-028: one `make proto` target that reproduces the tracked generated files

**Date** 2026-10-01 · **Tier** routine (tooling and documentation) · **Spec** GRVX-1102

**Options.** Correct the command in `CLAUDE.md`; or encode the whole procedure, including the
licence header, in a make target and document that.

**Chosen.** The make target. The procedure has three parts, the module flag, both proto files and
the header, and a command in a document drops whichever part its reader forgets.

**Done.** `make proto`, `CLAUDE.md`, and GRVX-1102's three references, verified against the tracked
files.

**Not decided, and why.** A CI check that `gen/` matches `proto/`. It needs a pinned `protoc` in CI,
because the generated header names the `protoc` version. That is a toolchain choice for whoever owns
CI images.

**To reverse.** Remove the target and restore the old line in `CLAUDE.md`.

## DD-028 — SD-030: Prometheus import reads `promtool tsdb dump` output

**Date** 2026-10-01 · **Tier** routine (product call assigned to the owner, and spec correction) ·
**Spec** GRVX-1108

**Options.** Depend on `prometheus/prometheus` to read blocks; hand-write a block reader; or read the
text `promtool tsdb dump` writes.

**Chosen.** The dump. The first is a 296-module dependency, which is design tier and reverses a
decision a week old. The second is days of core code for a one-time migration. The third costs the
user one command they can already run.

**Done.** `pkg/importer/prometheus.go` with counter-to-increase conversion and counted skips, seven
tests, the migration guide's Prometheus section, and GRVX-1108 amended.

**Not decided, and why.** Facts mode for Prometheus (AC-1). No Prometheus source holds per-request
records, so there is nothing to decide until one exists.

**To reverse.** Point `readerFor` back at an error. The Datadog reader is unaffected.
