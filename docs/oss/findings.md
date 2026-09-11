<!-- Findings about the CODEBASE, discovered while executing specs. -->
<!-- Defects in the SPECS themselves go in spec-defects.md. Append; do not rewrite. -->
# Repository findings

Things noticed while implementing Horizon 2 that are real problems but outside the scope of the
spec that found them. Each names what is wrong, why it matters, and who should fix it.

A finding is recorded rather than fixed in place because a spec that quietly widens to fix
everything it notices becomes unreviewable. See `11-agent-loops.md` §L4, scope gate.

---

## F-001 — compiled binaries are committed to version control

**Found by:** `senior-engineer` executing GRVX-709
**Owner:** `security-engineer`
**Severity:** medium — supply-chain hygiene

Two compiled binaries are tracked in git at the repository root:

| Path | Size | What it actually is |
|---|---|---|
| `cli` | 8.7 MB | **Mach-O 64-bit arm64 executable** — a macOS binary |
| `service_events_detail` | 27.5 MB | compiled binary |

Three problems:

1. **They cannot be verified against source.** GRVX-709 makes every released binary reproducible
   and signed. These are neither. A user who runs `./cli` from a checkout is executing something
   nobody can attest to.
2. **`cli` is a macOS ARM64 binary in a project whose CI, containers and deployment target Linux.**
   It is not usable by most of the people who will clone the repository, so it is not even serving
   the convenience it presumably existed for.
3. **36 MB of the repository is build output.** Every clone pays for it, forever, including the
   history.

**Recommendation:** delete both, add them to `.gitignore`, and let `make build` produce them into
`bin/` as it already does. `make build` already builds `bin/gravix`, so `cli` is redundant as well
as unverifiable.

**Why this was not fixed here:** GRVX-709 §3 explicitly says to report rather than delete —
removing tracked files is its own change with its own review, and bundling it into a signing
change would hide it.

---

## F-002 — 29 Go files are not gofmt-formatted

**Found by:** `senior-engineer` executing GRVX-701
**Owner:** `senior-engineer`
**Severity:** low — consistency

29 files under `services/`, `transforms/` and `pkg/` do not match `gofmt` output. Verified against a
stashed baseline that **all 29 were already unformatted before Horizon 2 began**, and that adding
licence headers introduced none of them.

**Recommendation:** one mechanical `gofmt -w` change, reviewed as a formatting-only diff so it is
easy to verify by eye, ideally followed by a CI check so it cannot recur.

**Why this was not fixed here:** GRVX-701 §4.2 limited that spec to prepending headers. A
formatting pass touching 29 files inside a licensing change would have been a scope violation and
would have made the licence diff unreadable.

---

## F-003 — compaction cannot see Hive-partitioned warehouse output

**Found by:** `senior-engineer` executing GRVX-802
**Owner:** `senior-engineering-lead`
**Severity:** medium — a scheduled job that silently does nothing

`transforms/compaction/main.go` finds files to merge with `parseWarehouseKey`, which accepts only
the **flat** warehouse layout:

| Key | Parsed? |
|---|---|
| `warehouse/request_metrics_minute/metrics_abc_2026-05-21.parquet` | yes |
| `warehouse/t1/request_metrics_minute/metrics_abc_2026-05-21.parquet` | yes |
| `warehouse/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet` | **no** |
| `warehouse/t1/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet` | **no** |

The four-segment Hive key is read as multi-tenant, giving `tenantID="request_metrics_minute"` and
`topic="event_day=2026-05-21"`. The topic then fails the allow-list and the key is skipped. The
five-segment multi-tenant Hive key has no branch at all.

The rollup has written Hive-partitioned output since Phase 5, and `storage/trino/init.sql:32` and
`cube/model/schema/RequestMetricsMinute.js` both read that layout. So **compaction currently
processes none of the metric files the system actually produces.** It runs, logs
"Found 0 Parquet compaction groups", and reports success.

This was verified directly against `parseWarehouseKey`, and the behaviour is now pinned by
`TestWarehouseKeysAreFlatLayoutOnly` so it cannot regress unnoticed while someone believes it is
fixed.

### Why it was not fixed here

GRVX-802 is a manifest spec. Teaching compaction a new key layout changes which files get merged,
deleted and renamed in a live warehouse — that is a data-movement change and needs its own spec,
its own dry-run evidence and its own rollback story. GRVX-802 §5.4's manifest rules **are**
implemented in `compactParquetGroup`, and tested directly, so they are correct the moment
compaction can reach these files.

### What this means for the manifest work

Merged-manifest handling is implemented and proven at the function level, not end to end, because
no production path reaches it. GRVX-810's correctness suite should treat "compaction merges a real
Hive partition and its manifest survives" as an open, unproven criterion until a spec fixes
`parseWarehouseKey`.

### Related

The same function is why GRVX-801's deterministic key is safe from compaction today: compaction
would otherwise rename merged output to `metrics_<uuid>_<date>.parquet`, reintroducing a
non-deterministic key. A spec that fixes `parseWarehouseKey` must fix that naming in the same
change, or it will undo GRVX-801.

---

## F-004 — GO-2026-5764 was reachable from `pkg/storage/s3.go` and CI had been red on it

**Found by:** `senior-engineer` executing GRVX-804, via the `vuln` CI job on pull request #20
**Owner:** `security-engineer`
**Severity:** high — a known-exploitable vulnerability in a reachable code path
**Status:** fixed

### What it was

`govulncheck` reported **GO-2026-5764**, a denial of service via a panic in the AWS SDK for Go v2
EventStream decoder, as *reachable* — not merely present in the dependency tree:

| Module | Found | Fixed in |
|---|---|---|
| `github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream` | v1.7.4 | **v1.7.8** |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.96.0 | **v1.97.3** |

`govulncheck` distinguishes "your code is affected" from "present in a module you require", and this
was the former: 130 call traces, entering through `pkg/storage/s3.go` at `NewS3Store`,
`Put`, `Get`, `Delete`, `Exists` and `List` — every S3 operation Gravix performs.

### Why it matters more than the usual version bump

`CLAUDE.md` gives `security-engineer` a veto the CPO cannot overrule on **releasing a known-exploitable
vulnerability**. This was one, in the storage layer, on a branch being prepared for a public release.

### How it was missed

The `vuln` job was already failing, on every head of the branch, and had been treated as part of the
background noise of a red pull request. It was only isolated after a CI event named it separately from
the DCO failure that was the known blocker.

**The lesson is about attention, not tooling: the job did its job.** A red check that someone has
decided is "the known one" stops being read, and a second failure hiding behind the first is exactly
what that habit costs. Two checks were red; only one had been diagnosed.

### The fix

Bumped both modules. `go mod tidy` pulled six transitive AWS modules and `smithy-go` forward with
them. All 41 packages pass, `pkg/storage`'s own 19 tests included; boundary, `build-oss` and
`test-oss` unaffected.

### Worth doing next

The scan also reports 18 vulnerabilities in required modules that current code does not call. Those are
not urgent by definition, but they are a reason to keep dependencies moving rather than letting them
settle — and `docs/oss/30-technology-review.md` §3 already records the infrastructure images sitting one
to three major versions behind for the same reason.

---

## F-005 — `/api/v1/metrics` has never been able to serve a percentile, and its JS tests never ran

Found while implementing GRVX-808. Neither is a regression from that work; both predate it.

### The public metrics endpoint asks Cube for members that do not exist

`services/gateway/gateway_platform.go` maps its public `metric` parameter onto Cube members:

```go
"p50_latency": "RequestMetricsMinute.p50LatencyMs",
"p95_latency": "RequestMetricsMinute.p95LatencyMs",
"p99_latency": "RequestMetricsMinute.p99LatencyMs",
```

The model has never defined `p50LatencyMs`. Before GRVX-808 the measures were `p50Latency`,
`p95Latency` and `p99Latency`; after it they are `bucketP50LatencyMs` and friends. Neither spelling
matches. The same query names the time dimension `RequestMetricsMinute.timestamp`, which has never
existed either — the model's time dimension is `bucketStart`.

So three of the five metrics this endpoint advertises, and the granularity parameter for all five, have
been returning Cube errors since they were written. It is documented in `docs/openapi.yaml` and offered
to Pro customers.

**Not fixed here.** GRVX-808's §4.2 names the files it may change and `gateway_platform.go` is not one
of them; improvising past that is how product decisions end up in implementation code. It also is not a
rename away from working: GRVX-808 establishes that *no* Cube member can answer a percentile above
minute granularity, so this endpoint's percentile metrics have to be re-pointed at
`GET /api/v1/percentile`, which is an interface decision. It belongs in GRVX-809 with the `group_by`
work SD-009 asks for.

**Until then the endpoint advertises numbers it cannot produce**, which is the failure mode the
correctness axis exists to rule out. It should either be fixed or the three percentile values removed
from its `validMetrics` map and from the OpenAPI document.

### The Node SDK's tests existed but CI never ran them

`sdk/node/package.json` defines `"test": "node --test tests/*.test.js"` and the repo has 29 passing
assertions across `client.test.js` and `sanitize.test.js`. The `sdk-tests` CI job ran `npm ci` and
`npx tsc --noEmit` — a type-check — and then stopped. A test suite that is never executed is
documentation.

**Fixed.** `.github/workflows/ci.yml` now runs `npm test` in `sdk/node`, and runs
`node --test cube/model/schema/*.test.js` for the Cube model and dashboard-routing tests GRVX-808
added. All 29 SDK assertions and all 14 model assertions pass.

### `make check-boundary` failed for anyone who had built the SDK

Running `npm test` in `sdk/node` triggers its `pretest` (`tsc`), which rewrites `sdk/node/dist/`.
The regenerated `.d.ts` files carry no licence header, so the boundary checker immediately reported six
violations — on a clean checkout it reports none. A check whose result depends on which commands you
happened to run last is a check nobody trusts. `cmd/checkboundary` now skips `dist/` the same way it
already skipped `node_modules/`, `gen/` and `bin/`.

**The underlying problem is left alone deliberately, and someone should take it.** `sdk/node/dist/` is
compiled output **committed to the repository**, ten files of it. `.gitignore` lists `dist/`, which has
no effect on files already tracked, so the ignore rule reads as if the build output is excluded when it
is not. Its licence headers exist only because GRVX-701's header pass edited the generated files by
hand — the next `npm run build` removes them again, which is exactly what happened here.

It cannot simply be deleted: `.github/workflows/publish-sdks.yml` runs `npm ci && npm publish` with no
build step, so the published package's contents come from whatever `dist/` is in the checkout. Removing
it needs a `prepublishOnly` (or `prepack`) script added first, and that belongs in its own change
rather than riding along with an unrelated one — deleting it here would have silently published a
broken SDK. Same family as F-001, and the same fix: build artefacts do not belong in version control.

---

## F-006 — the dashboard hides multi-org behind a plan the server does not check

Found while writing GRVX-809's AC-12 upsell test, which matched
`dashboards/index.html:1072`:

```html
<!-- Multi-Org Management (Admin + Scale/Enterprise Only) -->
```

and the gate behind it in `dashboards/app.js`:

```js
if (me.role === 'admin' && (me.plan === 'scale' || me.plan === 'enterprise')) { ... }
```

**It is not an upsell, and AC-12 was narrowed rather than failed.** Charter §7.4 forbids showing a free
user a locked feature and inviting them to pay. This shows them nothing at all — the card is
`display: none` and never becomes a padlock or a "Upgrade to Scale" teaser. A feature that is simply
absent is not a sales pitch. The test now matches calls to action (`upgrade to`, `unlock this`,
`premium feature`, a padlock glyph) rather than every mention of a plan name, which is what §7.4 is
actually about.

**What is worth someone's attention is the other side of it:** `handleMultiOrg` in
`services/gateway/enterprise.go` checks authentication and the admin role, and does **not** check the
plan. Any authenticated admin can call `POST /api/gateway/orgs` on the free plan and create child
organisations. The dashboard hides a feature the server gives away.

That is not a security hole — the endpoint is tenant-scoped and role-gated, so nobody reaches anything
that is not theirs — but it is a third instance of the pattern SD-001 recorded: plan gating that exists
in the UI and in nobody's server code. Two readings, and the choice is the CPO's:

- **The gate is correct and unenforced** → the server needs the plan check, and SD-001's dead
  `requirePlan` is the place to put it.
- **The gate is wrong** → multi-org is a core feature and the UI should stop pretending otherwise.

Either is defensible. What is not defensible is leaving the two halves disagreeing, because whichever
one a customer discovers first is the one they will believe.

---

## F-007 — there are two OpenAPI documents and they have diverged

The repository maintains two, by hand, with nothing keeping them in step:

| File | Paths | Version | Who reads it |
|---|---|---|---|
| `docs/openapi.yaml` | 25 | 0.1.0 | The Makefile, `scripts/golden_path_test.sh`, human readers |
| `services/gateway/openapi.json` | 57 | 1.0.0 | `scripts/generate-sdk-types.sh` → the Node, Python, Go and Java SDKs |

They disagree on almost everything a reader would check first: the number of endpoints, the version
number, and — until this commit — the licence, which both gave as MIT for a repository that has been
Apache-2.0 since GRVX-701.

**The concrete consequence right now:** GRVX-808's `GET /api/v1/percentile` and GRVX-809's
`GET /api/v1/lineage` are documented in the YAML, because that is the file both specs name in their
§4.2. They are absent from the JSON, so the generated SDKs do not have them. A user who installs
`@gravix/sdk` cannot call either endpoint without writing the request by hand, while the documentation
says they exist.

Only the licence was corrected here — a document that misstates its own project's licence is wrong
whichever way you read the scope question, and it was a one-line change in each file that leaves the
generated SDKs byte-identical (verified by re-running the generator).

**The fix is to delete one of them.** Two hand-maintained descriptions of one API is one description
and one lie, and which is which changes depending on who last edited what. The JSON is the one with
teeth — it generates code and CI validates it — so the likely answer is to generate the YAML from it,
or to drop the YAML and point `docs/` at the JSON. Either way it needs a spec: `docs-engineer` owns
"no merged behaviour change ships undocumented", and that promise is unenforceable while there are two
places to forget.

---

## F-008 — `gravix explain` only worked inside the source tree

Found by `scripts/prove_it.sh`, which runs the CLI from a temporary directory the way a user would.

```
$ cd /tmp/my-gravix-data
$ gravix explain request_metrics_minute "2026-09-03 00:05" --filter service=api
lineage: no contract for that metric version: request_metrics_minute@v2
```

`cmd_explain.go` defaulted `--contracts` to the relative path `"contracts"`. Run from the repository
root that resolves; run from anywhere else — which is everywhere a user actually runs a binary — it
finds nothing, the registry loads empty, and the command reports that the metric has no contract.

So the command whose entire purpose is showing where a number came from answered "there is no published
definition for this" to every user outside a source checkout. It passed every test because every test
ran with the repository as the working directory.

**Fixed.** `contracts/` is now an embedded filesystem, and `metriccontract.LoadDirOrEmbedded` prefers a
real directory when one is present — so editing a contract still works without rebuilding — and falls
back to the copy compiled into the binary when it is not. Contracts are source: they ship with the code
that computes the metrics they define, exactly as the SQL migrations already did.

Two tests cover it, including the case a packaged install actually hits: an existing but empty
`contracts/` directory, made by a packager and never populated.

**It also makes SD-010 part three redundant.** The gateway Dockerfile's `COPY --from=builder
/app/contracts` was added so `GET /api/v1/lineage` would have contracts to read. The binary now carries
them. The COPY is left in place deliberately — a reader looking for the files should find them, and an
operator who wants to edit one without rebuilding still can.

**The general lesson is about where tests run.** Every test in this repository has the package directory
as its working directory, so a path resolved relative to the working directory is resolved correctly in
every test and incorrectly in production. Anything the CLI reads by relative path deserves the same
look: this was the one the demo happened to walk into.

---

## F-009 — the no-Docker guard forbade the strongest proof that Docker is not needed

**Found by** the license-boundary-auditor's own suite, while landing GRVX-812.
**Severity** low — no user-visible defect. It is here because the failure mode is instructive.
**Status** fixed.

`TestSuiteNeedsNoDocker` (GRVX-810, AC-11) enforced "the correctness suite must run without a
container stack" by reading every `.go` file in the package and failing on the byte sequence
`docker`, `DOCKER_HOST`, `testcontainers` or `compose`, with a hard-coded exemption for the file
doing the checking.

GRVX-812's demo test proves the same property a stronger way: it runs `scripts/prove_it.sh` in a
child process with

```go
"DOCKER_HOST=unix:///nonexistent/docker.sock",
```

so that any attempt to reach a daemon fails at runtime rather than passing a grep. The guard failed
that file three times — for the sabotage itself, for the comment explaining it, and for the list of
tokens the demo script is forbidden to contain.

So the guard rejected the only change that made its property harder to violate, and would have kept
rejecting it. The two available ways out — deleting the sabotage, or exempting a second file by name
— both weaken the guarantee.

**Fixed.** The check is now structural. It parses the package and fails on two things:

- `exec.Command` / `exec.CommandContext` whose command literal has a base name starting with
  `docker` — which catches `docker`, `docker-compose`, and `/usr/local/bin/docker-compose`;
- `os.Getenv("DOCKER_*")` / `os.LookupEnv("DOCKER_*")` — *reading* the variable is how a process goes
  looking for a daemon. Setting it is the opposite, and is now allowed.

The dependency-tree check is unchanged. The self-exemption is gone: `suite_test.go` names docker only
in string comparisons, so under a behavioural rule it needs no special case.

Verified by mutation — a probe file containing all three violation shapes was added to the package
and each was reported by name before the probe was removed.

**The lesson is about proxies.** The grep was a cheap stand-in for "does not use Docker", and for a
while it was indistinguishable from the real thing. It diverged the moment a file needed to mention
Docker in order not to use it — and at that point the proxy was actively fighting the property. When
a guard blocks a change that strengthens what it guards, the guard is wrong, not the change.

---

## F-010 — `docker-compose.bootstrap.yml` has never parsed, and the README told people to run it

**Found by** running `docker compose config` for GRVX-901's AC-7.
**Severity** high — the documented low-cost deployment path was unusable, and silently so.
**Status** fixed.

```
$ docker compose -f docker-compose.bootstrap.yml config
services.cube.environment.[1]: unexpected type map[string]interface {}
```

Confirmed pre-existing by stashing every change from this spec and re-running: same error, exit 1.
The file has never been valid for Compose v2.

The line:

```yaml
      - CUBEJS_DB_DUCKDB_DATABASE_PATH=:memory:
```

DuckDB's in-memory database is spelled `:memory:`, so the value ends in a colon. A colon at the end
of an unquoted YAML scalar makes the whole entry a **mapping key** — the parser reads
`{"CUBEJS_DB_DUCKDB_DATABASE_PATH=:memory": null}` where Compose wants a string, and rejects the file
before starting anything.

**Fixed** by quoting the entry. One character of syntax, and the only reason it went unnoticed is
that nothing ever ran `docker compose config` against this file in CI — the `docker-lint` job builds
Dockerfiles, and `docker-compose.yml` (the full stack) is the one covered elsewhere.

**What made it invisible.** The failure is total and instant: no service starts, so there is no
partial stack to debug and no log to read. A user following the README's bootstrap instructions saw
one line of YAML arcana and nothing else. The whole point of the `$20/month` path is that it is the
easy one, and it was the only one that did not work.

GRVX-910's timed onboarding gate is the durable fix — a CI check that actually boots this file would
have caught it on the commit that introduced it. Until then, `docker compose config` is asserted by
GRVX-901's AC-7.

---

## F-011 — `docker-compose.yml` defines `gateway` twice, so the full stack does not start either

**Found by** extending GRVX-901's compose validation to the second file.
**Severity** high — the other documented deployment path is also unusable.
**Status** open. Not fixed here: GRVX-901 §3 forbids touching `docker-compose.yml`, and the fix is a
choice between two service definitions, which is not this spec's to make.

```
$ docker compose -f docker-compose.yml config
failed to parse /home/user/gravix-dashboards/docker-compose.yml: yaml: construct errors:
  line 1: line 69: mapping key "gateway" already defined at line 10
```

Two `gateway` blocks, and they do not agree on how the gateway finds its database:

| | line 10 | line 69 |
|---|---|---|
| tenant db | `command: ["./gateway", "--tenant-db", "/app/data/gravix.db"]` | `environment: TENANT_DB_PATH=/app/data/gravix.db` |

This reads as a rewrite where the replacement was added and the original never removed. YAML makes
duplicate keys an error, so Compose rejects the file before reading either — which at least means
nobody has been running a stack silently assembled from the wrong half.

**Together with F-010, every documented Docker path in this repository was broken**: the bootstrap
stack on a quoting bug, the full stack on a duplicate key. Both fail instantly and totally, which is
why neither produced a bug report — there is no partial stack to complain about.

**Fixing it needs a decision, not an edit.** The two blocks differ in more than the database flag,
and picking one silently changes how the full stack is configured. Whoever fixes it should diff the
two blocks in full, keep one, and add `docker-compose.yml` to the CI validation step that
`.github/workflows/ci.yml` now runs for the bootstrap file.

---

## F-012 — the dashboard bundle budget is framed as one feature's allowance but enforces a permanent ceiling, and nothing could run it locally

**Found by** CI going red on GRVX-902 after the JS suite passed on the previous head.
**Severity** medium — as written it will fail every future dashboard spec, and the obvious reaction
is to raise the number, which retires the guard.
**Status** both halves now addressed — see the resolution at the end. One question is deliberately
left open for a product owner.

GRVX-809's AC-13 guard says what it is for:

> The budget is what the panel may add on top, not a total, so it keeps meaning as the dashboard
> grows for other reasons.

But the measurement is `gzip(app.js + styles.css + lib/*.js) - 48880`, where `48880` is a constant
captured at the commit before GRVX-809. So it does not measure the panel at all. It measures **total
dashboard growth since a fixed point in the past**, and every subsequent feature spends the panel's
allowance. GRVX-902 added twenty-two lines to `app.js` and the suite went red at 8223 bytes against
an 8192-byte budget — **over by 31 bytes**, for a feature that has nothing to do with the panel.

Trimming two over-written comments brought it to 8098, so it is green with **94 bytes of headroom**.
That is not a fix. GRVX-903, 907 and 908 all touch the dashboard, and the first of them will fail.

**The decision to make is which guard is wanted**, and both are defensible:

1. **Per-feature budget**, matching the stated intent — rebaseline `BASELINE_GZIP` at each merge so
   the number always means "what the change in front of you adds". Catches a single bloated feature;
   never catches slow accumulation.
2. **Total budget**, matching the current behaviour — rename it, drop the "not a total" comment, and
   set the ceiling from what the dashboard should cost to load rather than from where it happened to
   be before GRVX-809. Catches accumulation, which for a zero-config product that is judged in the
   first ten minutes is arguably the one worth catching.

What must not happen is bumping `BUDGET` by a few hundred bytes each time it fires, which is the
path of least resistance and ends with a guard that only ever passes.

**The local-runner half is fixed.** CI ran `node --test cube/model/schema/*.test.js
dashboards/lib/*.test.js` and nothing else did: no Makefile target, no mention in `CONTRIBUTING.md`.
There is now `make test-js`.

**This is the third time the same shape of problem has bitten in this work** — `make lint` was weaker
than CI's `lint` job, `go test -race` was never run locally, and now the JavaScript suite had no
local entry point at all. Each was found by breaking the build. The pattern is worth stating plainly:
**a gate that exists only in CI is a gate you discover by tripping it**, and the cost is a red build
on someone else's branch rather than a failing command on your own machine.

### Resolution, 2026-09-11 — GRVX-903

GRVX-903 tripped the budget on its first run, by 10,636 bytes, exactly as predicted above. Rather
than raise the number, the guard was changed to measure what its own comment always claimed it
measured.

`BASELINE_GZIP` is gone. The baseline now lives in `dashboards/bundle-baseline.json`, and a change
that legitimately grows the bundle updates that file in the same commit. The budget therefore means
"what the change in front of you adds" again, and every increase is a reviewable number in a diff
rather than a silent accumulation. Cumulative growth since the pre-GRVX-809 origin is printed on
every run (`+11130 since the commit before GRVX-809`) but never fails the test.

This is option 1 from the two above, and it was chosen because it is the one the guard's own
documentation already described — making code match its stated intent is a bug fix, not a product
decision. Proven by mutation: 30 KB of poorly-compressible code appended to `app.js` fails the test
with a message naming the file to update and the number to put in it.

**Still open, and genuinely a product call:** whether the dashboard should also carry a hard total
ceiling, and what it should be. That is a claim about load time, and the right number comes from
what the dashboard should cost a first-time user on a slow connection — not from wherever the bundle
happened to sit when a guard was written. Deliberately not invented here.

---

## F-013 — recorded services and templates briefly vanish from the registry while a flush is in flight

**Found by** CI, on GRVX-905's commit — `TestFactsEndpointAutoLearnsNumericID` reported
`got 0 templates: []` on a machine where the same test had passed locally every time.
**Severity** medium — intermittent, and the symptom is the one the feature exists to prevent.
**Status** fixed, with a regression test that fails without the fix.

`pkg/discovery`'s `Flush` takes the pending batch out of the map and then writes it to SQLite,
deliberately releasing the lock first so that `RecordFact` never waits on disk I/O. The gap that
leaves had not been considered: **for the duration of the write, those observations are in neither
the pending map nor the database.** A concurrent `ListServices` or `ListTemplates` reads both and
finds nothing.

Reproduced deterministically enough to measure: with a 1 ms flush interval, **1 read in 3,000**
returned an empty list for a service that had definitely been recorded.

**Why this matters more than the rate suggests.** The whole point of GRVX-902 is that a service
appears the moment it sends a fact, so a user can answer "is my data arriving?". The failure mode
here is that same list intermittently answering *no*. A user who refreshes at the wrong moment is
told the thing they just did has not happened.

**Fixed** with a `sync.RWMutex` that makes a flush atomic from a reader's point of view: readers hold
it across both the database query and the in-memory merge, and `Flush` holds it across the write.
`RecordFact` never touches it, so the ingestion hot path is still free of disk I/O and still cannot be
blocked by a flush — the property the original design was protecting.

The read lock has to span **both** halves of the read, not just one. Holding it only for the query
would let a flush land before the merge and count those rows twice; holding it only for the merge
would miss the in-flight batch entirely. `TestRecordedDataIsNeverInvisible` asserts both: nothing
vanishes across 3,000 reads, and the final count is exactly 3,000.

**Two things made this hard to see.** The window is a single small SQLite transaction, so it needs
either a fast flush interval or a loaded machine — CI is both. And the correctness suite's own
`TestFlushRequeuesOnFailure` covers the *failure* path of exactly this function, which made the
function look well tested; the success path's visibility gap was the thing nobody had asked about.

**The general lesson.** "Take the batch, release the lock, then do the slow thing" is the right shape
for keeping I/O off a hot path, and it silently introduces a window where the data exists nowhere.
Any code in this repository that follows that shape — swap a buffer, then persist it — deserves the
same question: what does a reader see in between?
