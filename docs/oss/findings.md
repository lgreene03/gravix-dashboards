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

---

## F-014 — the README's "Send your own data" example has never worked

**Found by** running it, while doing GRVX-907's Definition-of-Done manual POST.
**Severity** medium — it is the first command a new user runs by hand, and it fails.
**Status** fixed.

The README's quick-start told readers to send their first fact with:

```bash
curl … -d '{ "eventId": "'$(uuidgen | tr '[:upper:]' '[:lower:]')'", … }'
```

Two independent failures, both verified against a live ingestion service:

1. **`uuidgen` produces a UUID version 4, and Gravix requires version 7.**
   ```
   $ curl … -d '{"event_id":"9b2d4f6a-1c3e-4a5b-8d7f-2e4a6c8b0d1f", …}'
   {"code":400,"error":"invalid RequestFact: validation error: event_id must be UUIDv7 (got v4)"}
   ```
2. **`uuidgen` is not always installed** — it is absent from this repository's own container image,
   where the command expands to an empty id and fails differently:
   `{"code":400,"error":"… event_id is required"}`.

So the documented first command fails on every machine: with a `v4` error where `uuidgen` exists, and
a `required` error where it does not.

There was a third problem specific to the bootstrap stack: the key was read with
`$(grep API_KEY .env | cut -d= -f2)`, and GRVX-901 removed `API_KEY` from `.env.bootstrap.example`
because the bootstrap stack generates its key into `data/api_key.txt` instead. That one is mine,
introduced two specs earlier.

**Fixed.** The quick-start now leads with `gravix send fact`, which builds a valid fact and needs no
`uuidgen`; verified end to end (`✓ Fact sent successfully.`, exit 0). The by-hand curl is kept for
people who want it, with a literal v7 id, the key read correctly for both stacks, and an explicit
note that `uuidgen` produces a v4 and will be rejected.

**This is the same root cause as SD-017**, found within an hour of it, in a different file. The rule
that `event_id` must be v7 is enforced in two validators and stated in no example. Anything in this
repository that shows a reader how to construct a fact by hand should be checked against a running
ingestion service — the SDKs and the CLI get it right, and everything hand-written has so far got it
wrong.

---

## F-015 — in the bootstrap stack, ingestion writes facts where the rollup never looks, so no metric is ever produced

**Found by** trying to observe GRVX-908's countdown end to end.
**Severity** high — the low-cost deployment path ingests facts successfully and produces no metrics
at all. Every service reports healthy; the dashboard is simply empty forever.
**Status** fixed in `services/ingestion/main.go` by option 1 below, guarded by
`TestUploadedFactsLandWhereTheRollupReads` (`services/ingestion/main_test.go`). Fixed as its own
change rather than inside a spec: GRVX-908 §4.3 fenced both files, and GRVX-910 §4 fences them too.

**The mismatch.** Two facts in the same process:

| | |
|---|---|
| `services/ingestion/main.go:592` | `destKey := fmt.Sprintf("raw/%s/%s/%s/%s", topic, …)` — the object key already begins with `raw/` |
| `services/ingestion/main.go:822-843` | `rawDir := filepath.Join(*baseDir, "raw")`, then `storage.NewLocalStore(rawDir)` — the store is rooted at `<base>/raw` |

`LocalStore` joins its root to the key, so a fact written with `--base-dir /app/data` lands at
`/app/data/raw/**raw**/request_facts/…`. The rollup is told
`-input-dir ./data/raw/request_facts` (`docker-compose.bootstrap.yml`), one level up from where the
file actually is.

**Reproduced with the compose layout exactly**, using the real binaries:

```
$ ingestion --base-dir ./data &            # as docker-compose.bootstrap.yml runs it
$ curl -X POST …/api/v1/facts …            # ingest=201
$ find ./data -name '*.jsonl'
./data/raw/raw/request_facts/2026-09-11/20/batch_…jsonl      ← doubled

$ rollup -input-dir ./data/raw/request_facts -output-dir ./data/warehouse/request_metrics_minute
"msg":"no data found, partition cleared"
$ find ./data/warehouse -name '*.parquet'                     ← nothing
```

Then, changing **only the path** and running the identical command:

```
$ mv ./data/raw/raw/request_facts ./data/raw/request_facts
$ rollup -input-dir ./data/raw/request_facts -output-dir ./data/warehouse/request_metrics_minute
"msg":"uploaded metrics"   "row_count":1
$ find ./data/warehouse -name '*.parquet'
./data/warehouse/request_metrics_minute/event_day=2026-09-11/request_metrics_minute_20260911.parquet
```

**Why only the bootstrap stack.** With S3/MinIO the store's root is a bucket, so a key beginning
`raw/` is correct and nothing is doubled. The doubling exists only where the "bucket" is a directory
that has already been named `raw`. The full stack uses MinIO; the bootstrap stack is the local-disk
one. That is the third finding in a row (**F-010**, **F-011**, this) whose common cause is that the
cheap path is the one nobody runs.

**Why nothing caught it.** Every service is healthy, ingestion returns `201`, facts are on disk, and
`GET /api/v1/services` lists the service correctly — GRVX-902's discovery reads the registry, not the
warehouse. The only symptom is a chart that never fills. GRVX-901's Definition of Done asks for four
services healthy within 60 seconds, and they would all have been healthy.

**Two candidate fixes, and they are not equivalent:**

1. **Root the store at `<base>` instead of `<base>/raw`.** One line, and the key's existing `raw/`
   prefix then lands where every consumer already expects. Changes the on-disk layout, so an existing
   deployment's `data/raw/raw/…` becomes unreadable without a move.
2. **Point the rollup at `./data/raw/raw/request_facts`** in the compose file. Zero migration, and it
   enshrines a path nobody can read as intentional.

Option 1 is the better repair and the one with a migration cost; that trade is why this is a finding
rather than a drive-by fix.

**Resolution — option 1, and the migration cost turned out to be zero.** The local store is now
rooted at the data root via `localStoreRoot`, so the key's existing `raw/` prefix lands at
`<base>/raw/<topic>/` — the layout every reader in the repository already resolves: the rollup jobs'
`-input-dir` default, `cmd/purge`, `cmd/cli recompute`, and the gateway's DLQ endpoints, which list
`dlq/request_facts/` under `./data/raw` and were reading the wrong directory for the same reason.
One fix repairs both.

No deployment needs migrating. The doubling only ever occurred on the local-disk path, and the only
stack that uses it is the bootstrap one, which by this finding never produced a readable metric —
there is no correct history under `data/raw/raw/` to preserve. The full stack writes through
`S3Store` to MinIO and was never affected.

The guard asserts the round trip, not the constant: it wires the store exactly as `main()` does,
runs a real `uploadFile`, and requires the bytes to be readable at the rollup's own default
`-input-dir` path. It also asserts `<base>/raw/raw` does not exist, because "the file is somewhere
under the base directory" is satisfied by the bug. Reinstating the old rooting fails it with the
actual written path in the message.

**One thing in the product's favour:** GRVX-908's countdown turns this from a silent failure into an
observable one. Before it, the dashboard showed "send your first event" forever, which reads as "you
did it wrong". With it, the dashboard says traffic was received and the first chart is coming — and
then sits at *"Almost there — checking for data…"* indefinitely, which is unmistakably the product's
problem rather than the user's.

## F-016 — the bootstrap stack holds no credential Cube will accept, so the dashboard can never load data

**Found by** trying to write GRVX-910's poll request and asking what it should authenticate as.
**Severity** high — with F-015 fixed, metrics are now produced correctly and still cannot reach a
chart. Every service reports healthy. This is the second independent break on the same path.
**Status** fixed by option 1, chosen by the repository owner. `cmd/bootstrap_seed` now creates the
user and generates its password; guarded by `TestProvisionedLoginAuthenticates` and four sibling
tests in `cmd/bootstrap_seed/main_test.go`.

**The chain.** Four facts, each fine alone:

| | |
|---|---|
| `docker-compose.bootstrap.yml`, `cube` service | sets `JWT_SECRET=supersecretjwtkey12345!` inline |
| `cube/cube.js` `checkAuth` | when `JWT_SECRET` is set, requires a `Bearer` JWT signed with it, before ever considering `CUBEJS_API_SECRET` |
| `dashboards/app.js:29,173,851` | `IS_MULTI_TENANT = !!GATEWAY_URL`; the token comes from `sessionStorage.getItem('gravix_token')`, set only by a gateway login |
| `cmd/bootstrap_seed/main.go:92-136` | creates a tenant and an API key — and **no user** |

`POST /api/gateway/login` (`services/gateway/gateway_auth.go:213`) needs an email and a bcrypt
password hash. With no user there is nothing to log in as, so `dashboardApiToken` stays `''`,
`cubeHeaders()` sends no `Authorization`, and `checkAuth` throws on the first query.

**Reproduced with the real code, both halves:**

```
$ go run ./cmd/bootstrap_seed -db …/gravix.db -api-key-file …/api_key.txt -dashboard-config …/dashboard_config.js
provisioned tenant "local" (id=5ee3cd4a-…)
api key written to …/api_key.txt

$ sqlite3 …/gravix.db
users: 0        tenants: 1      api_keys: 1

$ cat …/dashboard_config.js
window.GRAVIX_CONFIG = {
  ingestionApiUrl: "http://localhost:8090",
  gatewayUrl: "http://localhost:8091",       ← sets IS_MULTI_TENANT, so a JWT is required
  apiKey: "grvx_…"
};
```

```
$ JWT_SECRET=supersecretjwtkey12345!  node -e '…require("./cube.js").checkAuth({}, auth)…'
no Authorization header (what the bootstrap dashboard sends) => REJECTED: No authorization token provided
empty string                                                => REJECTED: No authorization token provided
the api key bootstrap_seed wrote                            => REJECTED: Invalid or expired token
Bearer + that api key                                       => REJECTED: Invalid or expired token
```

The API key the stack does mint is the wrong kind of credential entirely: it authenticates *writes*
to ingestion, and Cube wants a signed JWT carrying a `tenant_id` (which `queryRewrite` then uses to
force a tenant filter).

**Why nothing caught it.** The same reason as F-015, one layer up. `docker-compose.yml` — the stack
CI actually smoke-tests — runs the same `cube.js`, but its dashboard is reached through a gateway
login by a user that the full seeding path creates. Only the bootstrap stack seeds a tenant without a
user, and only the bootstrap stack is the one a new reader is told to run first.

**Three candidate fixes, and they are not equivalent:**

1. **`bootstrap_seed` creates a user** with a generated password, printed once and written to
   `data/` beside the API key. Keeps authentication on, costs the user one copy-paste, and is the
   only option that leaves the bootstrap stack's security posture the same as the full stack's.
2. **Drop `JWT_SECRET` from the `cube` service** so `checkAuth` falls through to the
   `CUBEJS_API_SECRET` branch, which with no secret set returns `{}` and allows everything. One line,
   and it ships an unauthenticated metrics API on port 4000 as the default first-run experience.
3. **`bootstrap_seed` mints a long-lived JWT** into `dashboard_config.js`. No login step at all, and
   it puts a bearer token in a file served over HTTP — the same exposure already recorded as
   **SD-013** for the API key in that file.

Option 1 is the recommendation. Options 2 and 3 both make the zero-config path faster by removing
authentication from it, which is the trade the charter's data-ownership axis exists to refuse.

**Resolution — option 1.** `bootstrap_seed` creates an admin user for the bootstrap tenant with a
256-bit random password, writes `email`/`password` to `data/login.txt` at mode 0600 beside the API
key, and prints both to stdout — the compose log being where someone watching a first boot is already
looking, and the file being inside a container they may not have a shell in. No default password
ships anywhere, and no credential is added to `dashboard_config.js`, so **SD-013** is untouched.

Verified end to end against the real `checkAuth`, not by inspection:

```
$ go run ./cmd/bootstrap_seed -db …/gravix.db -api-key-file …/api_key.txt \
    -dashboard-config …/dashboard_config.js -login-file …/login.txt
provisioned tenant "local" (id=02448c61-…)
dashboard login written to …/login.txt
  email:    local@gravix.invalid
  password: YzfMVpURU8hXIn1XPLezfklOIkwXZxGDgMDBYbqoCFU

$ stat -c '%a' …/login.txt          → 600
$ sqlite3 …/gravix.db               → users: 1

$ JWT_SECRET=supersecretjwtkey12345!  node -e '…checkAuth("Bearer " + jwt.sign({tenant_id}, secret))…'
Cube checkAuth => ALLOWED {"securityContext":{"tenant_id":"02448c61-…"}}
queryRewrite    => [{"member":"RequestMetricsMinute.tenantId","operator":"equals","values":["02448c61-…"]}]
```

**Two traps the guards exist for.** The first surfaced immediately: an existing test,
`TestProvisionReusesExistingTenantWhenKeyFileMissing`, went red on a `UNIQUE constraint failed:
users.email`. Losing `api_key.txt` while keeping the database re-runs `provision` against a user that
already exists, so the password is reset rather than created.

The second is the reason `TestReprovisionedLoginAuthenticates` verifies the password rather than the
row: that reset goes through `UserRepo.UpdatePassword`, which stores **verbatim**, while
`UserRepo.Create` bcrypts anything that is not already a hash. Passing the plaintext to the former
writes a real password into the database in the clear and leaves a login file that looks correct and
cannot be used. Reinstating that mistake fails the test with `hashedSecret too short to be a bcrypted
password` and the first four characters of what was stored. The asymmetry between the two methods is
worth knowing about anywhere else it is used.

**This blocked GRVX-910**, whose §5.2 step 4 polls Cube's `load` endpoint and needs a credential.
Recorded as **SD-019** and returned as `SPEC DEFECT: §2` under that spec's own §10; the poll can now
inherit this login.

## F-017 — the performance gate compares an average against a threshold named `max_p95_latency_ms`

**Found by** GRVX-1001 §6 step 1, which requires listing every metric the existing performance
scripts already produce before writing a new harness.
**Severity** medium — no public number depends on it today, but it is a gate that reports PASS on
runs it was written to fail, and Phase 10 is about to build published figures in this area.
**Status** open. Not fixed here: GRVX-1001 §3 and §4.2 both forbid removing or rewriting any
existing `scripts/perf_baseline.json` field, and the threshold's *name* is the defect.

**The mismatch.** `scripts/perf_baseline.json` declares, for each profile:

```json
"baseline_50qps": { "max_p95_latency_ms": 200, … }
```

`scripts/perf_test.sh:170` compares that threshold against `avg_latency`, and says so:

```bash
# Note: the load generator outputs avg_latency_ms, not p95. We use avg as a
# proxy here since the generator doesn't track percentiles.
# Check latency (avg as proxy for p95)
if awk "BEGIN { exit !( $avg_latency > $max_p95_lat ) }"; then
```

The generator has no percentile to give: `cmd/load_generator/main.go:49` emits `avg_latency_ms` and
nothing else.

**Why "proxy" understates it.** The comment's own justification — "if avg exceeds the threshold it is
definitely a breach" — is sound but describes the direction the gate is *not* for. A gate exists to
catch breaches, and this one passes every run whose mean is under the threshold no matter how heavy
the tail is. For a latency distribution with any meaningful tail, the p95 is several times the mean,
so a `max_p95_latency_ms: 200` gate does not fail until the real p95 is far past 200 ms. The
thresholds being "set generously enough to accommodate this approximation" makes it looser still.

**A second defect in the same number.** `computeSummary` (`cmd/load_generator/main.go:60-62`) divides
total latency by the count of **successful** requests only:

```go
if s > 0 {
    avgLatMs = float64(lat) / float64(s) / 1e6
}
```

Failed requests contribute no latency and are not in the denominator, so a run that degrades until
its slowest requests time out reports a *lower* average than one that degrades slightly less. The
error-rate check is a separate gate and does not compensate: the latency figure itself improves as
the system gets worse.

**Why this is not fixed in GRVX-1001.** The spec fences it twice — §3 "Do NOT delete or rewrite
`scripts/perf_baseline.json`'s existing fields" and §4.2 "Remove no field" — and the honest repair is
to rename the field, which is the thing forbidden. GRVX-1001's new `Result` schema measures
`QueryP50Ms`/`QueryP95Ms`/`QueryP99Ms` and `IngestP99LatencyMs` as real percentiles, so the new
harness is correct by construction; this finding is about the old gate that keeps running beside it.

**The repair, when someone owns it:** teach `cmd/load_generator` to keep a latency sketch (the
repository already has one in `pkg/sketch`, used by the rollup), emit real percentiles, include
failed requests in the latency population or state explicitly that it does not, and then rename the
baseline fields to match what is actually compared. Until then `perf_test.sh`'s PASS means "the mean
of successful requests is under a number labelled p95".

## F-018 — `recompute` takes its lock on the local filesystem at a path relative to the working directory, while writing its data through the object store

**Found by** GRVX-1001. The benchmark harness kept creating an empty
`bench/warehouse/request_metrics_minute/` inside the repository, and every test that called the
driver reproduced it.
**Severity** medium-high. The stray directory is cosmetic; what it exposes is not. The lock exists to
stop the cron rollup and a `gravix recompute` writing the same partition at the same time, and in the
two deployments where that can actually happen it does not.
**Status** open. Not fixed here: GRVX-1001 measures and does not change behaviour, and the repair is
a change to locking semantics that wants its own spec.

**The mismatch**, in one place — `pkg/recompute/recompute.go:534`:

```go
dir := MetricDirFor(opts.OutputDir, tenant, opts.Metric)   // e.g. "warehouse/request_metrics_minute"
elector := leaderelect.NewFileElector(dir, LockName)
```

`opts.OutputDir` is an **object-store key prefix**. Everywhere else in `Run` it is resolved through
`opts.Store`, so with `NewLocalStore("/app/data")` it means `/app/data/warehouse/…` and with an S3
store it means a key in a bucket. Here it is handed straight to `leaderelect.NewFileElector`, whose
`Acquire` does `os.MkdirAll(e.dir, 0755)` (`pkg/leaderelect/leaderelect.go:65`) against the **local
filesystem, relative to the process working directory**.

**Reproduced**, with the store rooted in a temporary directory well away from the repository:

```
$ generate(tmpdir, …)                      # writes under $TMPDIR
$ rollupOnce(tmpdir, …)                    # store rooted at $TMPDIR
rollupOnce created ./warehouse in the working directory
```

The data went where it was told. The lock did not.

**Why the stray directory is the small half:**

1. **Two processes sharing a data root but not a working directory do not share a lock.** The
   bootstrap stack's rollup container runs from `/app`; a `gravix recompute` run by a human from
   anywhere else takes a lock at a different absolute path. Both acquire, both proceed, and they are
   writing the same partitions. The lock reports success in exactly the case it exists to refuse.
2. **With S3 the lock is meaningless across machines.** The store is a bucket every replica shares;
   the lock is a file on whichever local disk each replica happens to have. Two replicas never
   contend, whatever their working directories.
3. **A read-only working directory breaks recompute for a reason unrelated to its data.** `MkdirAll`
   fails, `Acquire` returns an error, and `Run` aborts with "acquiring lock in
   warehouse/request_metrics_minute" — while the output store it was asked to write is perfectly
   writable.

**Why nothing caught it.** Every existing test runs with the working directory at the package
directory and a store rooted in a `t.TempDir()`, so the lock lands beside the test binary and works
by accident. It is the same shape as **F-015**: a path that resolves correctly through the store and
incorrectly outside it, with nothing comparing the two.

**The repair, when someone owns it:** the lock belongs in the same namespace as the data it guards —
an object in the store, taken through `opts.Store` with a conditional write, so that two processes
sharing a bucket contend and two sharing nothing do not. Failing that, it must at minimum be resolved
to an absolute path derived from the store rather than from `os.Getwd()`, and the fact that it only
guards a single machine has to be documented rather than implied.

**Worked around in the benchmark** by passing an absolute `OutputDir`, which makes the lock path
absolute too. That is a workaround in one caller, not a fix: every other caller — the cron rollup and
`cmd/cli`'s `recompute`, both of which pass relative defaults — still takes a working-directory lock.

## F-019 — the bootstrap stack has not built at all since Alpine bumped tzdata

**Found by** GRVX-910's `timed-onboarding` gate, on its first ever run. The gate did what it was
built to do on the first attempt, which is the argument for having added it.
**Severity** high — `docker compose -f docker-compose.bootstrap.yml up -d --build`, the command
`README.md` gives a first-time reader, fails outright. Nothing starts.
**Status** fixed in `services/rollup/Dockerfile`.

**The failure**, from the CI log:

```
#16 [service-events-rollup stage-1 4/10] RUN apk add --no-cache tzdata=2025c-r0
#16 0.658 ERROR: unable to select packages:
#16 0.659   tzdata-2026c-r0:
#16 0.659     breaks: world[tzdata=2025c-r0]
target bootstrap-init: failed to solve: process "/bin/sh -c apk add --no-cache tzdata=2025c-r0"
  did not complete successfully: exit code: 1
```

**Why a version pin caused it.** Alpine's package repository serves only the **current** version of
each package; there is no archive of superseded ones. So `apk add tzdata=2025c-r0` does not reproduce
an old build — it works until upstream publishes a new tzdata and then fails forever. The pin was not
protecting the build, it was scheduling its death. The date it happened is simply the date Alpine
moved to 2026c-r0.

Both `docker-compose.yml` and `docker-compose.bootstrap.yml` build four services from this one
Dockerfile (`request-metrics-rollup`, `service-events-rollup`, `purge`, and — since GRVX-901 —
`bootstrap-init`), so the failure takes out both stacks, not just the lean one.

**The fix.** Unpinned, with `# hadolint ignore=DL3018` and the reasoning in the Dockerfile itself.
For tzdata specifically, currency is the property you want: a stale copy makes cron jobs fire at the
wrong hour after a timezone rule changes. Build reproducibility comes from the base image, which is
already pinned by digest.

**Why nothing caught it.** `docker-smoke` builds the full stack, but it is `if: github.event_name ==
'push' && github.ref == 'refs/heads/main'` — it never runs on a pull request, and nobody had pushed
to `main` since Alpine moved. `docker-lint` runs hadolint, which checks that a version *is* pinned
and has no way to know the pinned version still exists. Every other job builds with `go build`, not
Docker. So the one stack a new user is told to run had no pull-request-time build at all.

That is precisely the gap GRVX-910 §2 argued for closing:

> a budget that only gates pushes to `main` gates nothing — by the time it fails on `main` the
> regression is already merged.

The observation generalises past budgets. `timed-onboarding` runs on every pull request and builds
the bootstrap stack, so this class of failure is now caught before merge rather than by a user.

**Related, and not fixed here:** `docker-smoke` retains its push-to-main-only condition, so the
*full* stack still has no pull-request build. Worth revisiting once `timed-onboarding` has a few
green runs to show what it costs in minutes.

## F-020 — capacity planning's per-event sizes are wrong, and its "compression ratio" describes the wrong mechanism

**Found by** GRVX-1004 §6 step 1, which requires reconciling `docs/capacity-planning.md` against the
measured figures before building a cost calculator on top of it.
**Severity** medium — the storage numbers are conservative, so nobody under-provisions, but the
mechanism is described wrongly and that misleads about what Parquet can be used for.
**Status** open. Not fixed here: GRVX-1004 §4.2 admits `docs/capacity-planning.md` only to "link the
calculator; remove any figure the model now supersedes", and rewriting the mechanism paragraph is a
larger edit than that.

**The three figures**, against the GRVX-1001 bench run (1,008,000 facts, committed result file):

| `docs/capacity-planning.md` | Documented | Measured | |
|---|---:|---:|---|
| JSONL per event | ~300 B | **203.78 B** | over by 47% |
| Parquet per event | ~30-50 B | **2.89 B** | over by 10-17× |
| "Compression Ratio: 6-10x vs JSONL" | 6-10× | **70×** | and it is not compression |

**The mechanism is the real problem.** The table is headed *Compression Ratio* and the Parquet row
says "6-10x vs JSONL — Zstd-compressed, columnar". That frames the size difference as an encoding
win, which invites a reader to assume Parquet holds the same information more tightly. It does not.
The rollup **aggregates**: many facts collapse into one minute-bucket row per
service/method/path_template. Most of the 70× is fewer rows, not smaller ones.

Two things follow that the current wording hides:

1. **Per-event data cannot be recovered from the warehouse.** Raw JSONL is the only per-event record,
   which is exactly why `docs/00-system-truth.md` §4 forbids deleting it. A reader who believes
   Parquet is a compressed copy might reasonably conclude the raw files are redundant.
2. **The ratio does not hold at any scale.** It is a function of events per bucket-key. A deployment
   with one service and one path gets a far better ratio than one with twenty services; the bench
   run's 70× reflects its own fixture shape, not a property of the format.

**Why the numbers are safe but still wrong.** Over-estimating storage means nobody runs out of disk,
so this has never bitten anyone. It has also never been checked: the figures are described in the
document itself as planning estimates ("Use 300 bytes as a planning average"), and until GRVX-1001
there was no harness that could have produced the real ones.

**The repair:** replace the compression-ratio column with two separate statements — the per-event
JSONL size (measured), and the aggregation factor with an explicit note that it depends on events per
bucket-key and is not recoverable per event. Then re-derive the plan-tier tables from the measured
figure rather than from 300 bytes.
