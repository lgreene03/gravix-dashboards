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
**Status** FIXED in `9691d51`, four months after it was found. See the resolution at the end of this
entry. It was recorded open because GRVX-901 §3 forbids touching `docker-compose.yml`, and because
the fix is a choice between two service definitions rather than an edit.

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

### Resolution

Both instructions above are now carried out: the blocks were diffed in full, one was kept, and
`docker-compose.yml` joined the CI validation step in the same change.

The decision turned out not to be finely balanced. Diffed in full:

| | line 10 (removed) | line 69 (kept) |
|---|---|---|
| `JWT_SECRET` | `supersecretjwtkey12345!` hardcoded | `${JWT_SECRET:?…}` — required from the environment |
| `RAW_DATA_DIR`, `INGESTION_URL` | absent | set |
| `depends_on` | none | `cube: service_healthy` |
| tenant db | `--tenant-db` flag | `TENANT_DB_PATH` env |

The kept block is a superset on configuration and the only one that does not commit a signing secret
to a file users are told to copy. Both reach the same database — `services/gateway/main.go` falls
back to `TENANT_DB_PATH` when `--tenant-db` is empty — so keeping the environment form loses nothing.
Recording this in full because the finding said the choice was not the original spec's to make: it is
recorded here so the reasoning is reviewable rather than implied by a deletion.

**Why it survived four months.** Nothing in CI loaded the file. `timed-onboarding` runs the bootstrap
stack, `docker-smoke` is the only job that builds the full one and its result is discarded (F-026),
and the compose-validation step in `docker-lint` covered only the bootstrap file — excluded
deliberately, with a comment pointing here. The exclusion was the correct call at the time and it is
also why the fault could not surface on its own. It is now in that step, and
`cmd/onboarding_gate/compose_tenancy_test.go` decodes both files into a Go map, which rejects a
duplicate key where parsing into a `yaml.Node` accepts it silently.

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

## F-021 — the bootstrap stack's data volume arrives root-owned, and every container runs non-root

**Found by** GRVX-910's `timed-onboarding` gate, on the first run where the images actually built.
**Severity** high — `docker compose -f docker-compose.bootstrap.yml up -d --build` dies on the first
container. Nothing starts.
**Status** fixed in `docker-compose.bootstrap.yml`, guarded by
`TestBootstrapInitOwnsTheDataVolume`.

**The chain**, four facts that are each fine alone:

| | |
|---|---|
| `.gitignore:51` | `data/` is ignored, so a fresh clone has no `./data` |
| `docker-compose.bootstrap.yml` | every service binds `./data:/app/data` |
| Docker | a bind mount whose host path does not exist is created as **root:root** |
| `services/rollup/Dockerfile:50-51` | `chown -R gravix:gravix /app/data` then `USER gravix` — and the bind mount replaces that directory, ownership included |

So the container runs as `gravix` against a root-owned directory it cannot write. From the CI log:

```
Container gravix-bootstrap-init  service "bootstrap-init" didn't complete successfully: exit 1
```

**Reproduced with a control**, running the real binary as a non-root uid:

```
$ ls -ld ./data           # as Docker creates it
drwxr-xr-x root root data

$ setpriv --reuid=65534 … ./bootstrap_seed -db=./data/gravix.db …
bootstrap_seed: open tenant database: set WAL mode: unable to open database file: out of memory (14)
exit=1

$ chown -R 65534:65534 ./data && setpriv --reuid=65534 … ./bootstrap_seed …
dashboard login written to …/login.txt
exit=0
```

Ownership is the only variable between the two runs.

**A detail worth knowing on its own:** SQLite reports this permission failure as **"unable to open
database file: out of memory (14)"**. There is no memory problem. Anyone reading that line in a
compose log will go looking for one, which is a good reason to record the real cause here.

**The fix.** `bootstrap-init` — a one-shot container that already exists and already holds the most
privilege-adjacent job in the stack, minting credentials — now runs as root, chowns `/app/data` to
`gravix`, seeds, and chowns again. The long-running services stay non-root. This is the ordinary
init-container pattern.

The second chown is not redundant: the seeder runs as root, so `api_key.txt` and `login.txt` are
created root-owned at mode 0600, and `synthetic-traffic` reads the key as `gravix`. Without it the
stack fails one container later instead of one container earlier.

**Why nothing caught it.** The same reason as F-015, F-016 and F-019: **nobody had ever run this
stack.** `docker-smoke` builds the *full* stack and only on push to `main`. GRVX-901's Definition of
Done claimed four services healthy within 60 seconds, and no Docker daemon existed in the environment
where that box was ticked. The first time `docker compose up` ran against
`docker-compose.bootstrap.yml` was `timed-onboarding`'s first green build — which is this finding.

**Four independent breaks on one path.** F-015 (facts written where no reader looked), F-016 (no
credential Cube would accept), F-019 (the image would not build), and this. Each would have been
sufficient on its own to make the advertised quickstart fail, and each was invisible to every gate
that existed. The lesson is not about any of the four: it is that a documented command nobody
executes is a documented command that does not work, and the only fix is a gate that runs it.

## F-022 — the first committed benchmark result was produced from a dirty tree, and reported a number the code does not produce

**Found by** GRVX-1004, reconciling the cost model's storage input against the bench result it is
required to cite.
**Severity** medium — no number had been published outside the repository yet. Had one been, it would
have been unreproducible, which for this project is the worst kind of wrong.
**Status** fixed: the bad file is regenerated from a clean tree, and
`TestNoCommittedResultIsFromADirtyTree` now refuses any committed result whose commit is dirty.

**What happened.** `bench/results/20260911T220715Z-small.json`, committed as GRVX-1001's §9 evidence,
reported `bytes_per_event_raw: 210.7807132936508`. The committed code produces
`203.7807132936508` — reproducibly, across four independent runs (two explicit work directories, a
temporary one, and both `--runs 1` and `--runs 3`).

The two differ by **exactly 7.0**, with an identical fractional part. That is not two measurements of
different things; it is the same computation over a byte total that differed by exactly
7 × 1,008,000 bytes. The file was produced by work-in-progress code that was never committed and no
longer exists, so the precise cause is not recoverable — which is itself the point.

**The harness had already reported it.** `Machine.GravixCommit` appends `-dirty` when the working
tree has uncommitted changes, for exactly this reason:

```json
"gravix_commit": "c2f74fffe861f825a56aa59087c0bd7d6f67e994-dirty"
```

The mechanism worked. The person reading the file did not. That is worth recording as plainly as a
code defect, because a provenance field nobody reads is the same as no provenance field.

**Ruled out along the way**, each by measurement rather than reasoning:

- *Compaction deleting raw facts* — a probe generated 1,200 facts across 4 files, ran the real
  compaction job, and found 1 file with byte-identical content and the same 1,200 lines. No loss.
- *Run count* — `--runs 3` gives 203.78, same as `--runs 1`.
- *Work directory* — an explicit `--work-dir` and the default temporary directory agree.

**The guard.** `TestNoCommittedResultIsFromADirtyTree` reads every file in `bench/results/` and fails
on a dirty, unknown or empty commit, and on any result that does not validate. It caught both files
present when it was written. The rule it encodes is narrow and total: **a result in this repository
must be reproducible from a commit in this repository.**

**Downstream corrections:** GRVX-1001's §11 summary carried 210.8 and now carries 203.8.
`dashboards/tco-measurement.json` fed 213.68 to the cost model and now feeds 206.68 (raw 203.78 plus
rolled-up 2.90). GRVX-1003's §11.1 decomposition was measured independently, from the directory
rather than from the result file, and was already correct.

## F-023 — the dashboard cannot mount its config, because the mountpoint would have to be created inside a read-only mount

**Found by** GRVX-910's `timed-onboarding` gate, on the first run where the stack got far enough to
try starting the dashboard.
**Severity** high — four containers start successfully and then `docker compose up` fails. The stack
is closer to working than ever and still does not work.
**Status** fixed by committing `dashboards/dashboard_config.js` as a placeholder, guarded by
`TestFileMountsInsideReadOnlyMountsExist`.

**How far it got**, which is the good news in this finding:

```
Container gravix-bootstrap-init  Exited        ← seeded and exited 0
Container gravix-ingestion       Healthy
Container gravix-cube            Healthy
Container gravix-synthetic-traffic Started
Container gravix-dashboard       Starting
Error response from daemon: failed to create shim task: OCI runtime create failed:
  error mounting ".../data/dashboard_config.js" to rootfs at
  "/usr/share/nginx/html/dashboard_config.js": make mountpoint
  "/usr/share/nginx/html/dashboard_config.js": openat dashboard_config.js: read-only file system
```

F-015, F-016, F-019 and F-021 are all genuinely fixed: the images build, the volume is writable, the
tenant and its login are created, ingestion and Cube both report healthy.

**The mechanism.** Two mounts on the same service:

```yaml
- ./dashboards:/usr/share/nginx/html:ro                                  # first
- ./data/dashboard_config.js:/usr/share/nginx/html/dashboard_config.js:ro # second, inside the first
```

The second must create `dashboard_config.js` inside the first — which is `:ro`. Docker cannot, so the
container never starts. It works only if the file already exists at `dashboards/dashboard_config.js`,
and nothing tracked it: not in git, not on disk, not even gitignored.

**The intent was right and the mechanics were not.** The compose file's own comment explains the
single-file mount:

> One file, not the ./data directory: mounting the directory would serve api_key.txt over HTTP.

That reasoning is correct and worth keeping — it is the mitigation for half of SD-013. The fix
preserves it and adds the missing mountpoint rather than widening the mount.

**The placeholder is deliberately empty** (`window.GRAVIX_CONFIG = {}`). `app.js` merges that object
over its own built-in defaults, so a dashboard opened without the stack behaves exactly as it would
with no config at all, rather than appearing configured with values nobody provisioned.

**The guard generalises past this one file.** `TestFileMountsInsideReadOnlyMountsExist` walks every
service and, for each file mounted into a path inside one of that service's own read-only directory
mounts, requires the target to exist in the repository. Removing the placeholder reproduces the CI
failure as a one-second test failure naming the file and the reason.

**Fifth independent break on this path** — F-015, F-016, F-019, F-021, and this. The pattern has not
changed: each was invisible until something actually ran `docker compose up`, and each is a different
layer (object-store paths, credentials, image build, volume ownership, mount ordering). The gate has
now found three of the five on its own, which is three more than every other check in the repository
combined.

## F-024 — a retroactively-added percentile can be rolled up in a pre-aggregation, and every existing guard passes

**Found by** GRVX-1006 AC-3, checking whether the existing percentile protections actually hold
before adding another.
**Severity** medium — no such measure exists today, so nothing is wrong in the warehouse. The
protection Phase 8 built has a gap exactly where Phase 8's own follow-on feature writes.
**Status** fixed by two new guards in `cube/model/schema/RequestMetricsMinute.test.js`.

**What Phase 8 built, and it is good.** `cube/model/schema/RequestMetricsMinute.js` keeps the
per-bucket percentiles as `min` rather than `max`, with a comment explaining that a wrong number
which is *visibly* wrong gets reported while a plausible one gets believed. Two tests enforce it: one
requires any measure reading a percentile column to be annotated `meta.aggregatable: false`, and one
refuses to put such a measure in a pre-aggregation.

**The gap is the pattern.** The first test finds percentile columns with `/p\d\d_latency_ms/` —
exactly two digits, and only that spelling. `pkg/recompute`'s `MetricRow` also carries
`extra_quantile_label` and `extra_quantile_ms`, written by GRVX-806 when a percentile is added
retroactively. That column does not match.

**Measured.** A measure reading `extra_quantile_ms`, aggregating with `max`, placed in the
`endpointDaily` pre-aggregation, with no `meta` annotation at all:

```
$ node --test cube/model/schema/RequestMetricsMinute.test.js
# pass 14
# fail 0
```

All fourteen tests pass. That is the maximum of 1,440 one-minute percentiles, stored as if it were
the day's percentile — the precise defect the v1 model was deprecated for — reintroduced through a
column the regex did not know about, and cached in a pre-aggregation on top of it.

**The fix checks the invariant instead of the spelling.** A pre-aggregation rolls rows up to a
coarser grain, so every measure in one must survive that roll-up. `sum` and the count family do;
`min`, `max` and `avg` over a per-bucket scalar do not, whatever the column is called. There is no
function of per-bucket percentiles that yields the window's percentile — which is why GRVX-804 stores
a mergeable sketch and GRVX-808 serves percentiles from it.

`TestNoPercentileInPreAggregations` enforces that. `TestNoPercentileColumnIsRolledUp` adds a second,
independent angle on the same rule — a column pattern wide enough to cover `quantile` and
`percentile` as well as any `p<n>_latency_ms` — because the type check alone would miss a percentile
someone declared `sum`, and the column check alone would miss one in a blandly-named column.

Mutation-tested: `max`, `min` and `avg` over `extra_quantile_ms`, and `avg` over a
`p999_latency_ms`, all now fail with a message naming the pre-aggregation, the measure and the
aggregation.

**The general lesson, which is not about this regex.** A guard that matches on a *name* protects the
names its author knew about. Every one of those is a hostage to the next feature that adds a column.
Where an invariant can be stated in terms of behaviour — "this must survive a roll-up" — it covers
the cases nobody has thought of yet, and it is the same reason `TestSuiteNeedsNoDocker` (F-009) was
rewritten from a byte-scan to an AST check.

## F-025 — the full stack has F-021 too, and nothing there fixes it

**Found by** checking whether the two mount defects just fixed in the bootstrap stack also apply to
`docker-compose.yml`, rather than waiting to be told.
**Severity** high — `docker compose up -d --build` on the full stack, from a fresh clone, should fail
the same way the bootstrap stack did.
**Status** open, deliberately unfixed. See "Why this is recorded and not patched" below.

**F-021 applies.** Verified, not inferred:

| | |
|---|---|
| `.gitignore:51` | `data/` is ignored, so a fresh clone has no `./data` |
| `docker-compose.yml` | six services bind `./data:/app/data` — `gateway`, `ingestion`, `request-metrics-rollup`, `service-events-rollup`, `service-events-detail-rollup`, `purge` |
| `services/{gateway,ingestion,rollup}/Dockerfile` | each declares `USER gravix` |
| `docker-compose.yml` | no `user:` override anywhere, and the only `mkdir` is trino's, for its own catalog directory. **Nothing creates or chowns `./data`.** |

Docker creates a missing bind-mount source as `root:root`, so six non-root containers share a
directory none of them can write, and unlike the bootstrap stack there is no `bootstrap-init` to
repair it.

**F-023 does not apply.** The full stack's dashboard mounts `./dashboards:/usr/share/nginx/html:ro`
and `./storage/dashboard/nginx.conf:/etc/nginx/conf.d/default.conf:ro` — the second target is outside
the first mount, so no mountpoint has to be created inside a read-only one. It also does not mount
`dashboard_config.js` at all, so the placeholder committed for F-023 changes nothing here: the file
is served as `{}` where it previously 404'd, and `app.js` merges both to the same built-in defaults.

**Why nothing caught it.** `docker-smoke` is the only job that builds the full stack, and it is
`if: github.event_name == 'push' && github.ref == 'refs/heads/main'`.

*An earlier version of this finding said it "would have caught this". That was wrong, and checking it
produced **F-026**: `docker-smoke` is not in `ci-summary`'s `needs` and appears in no failure
condition, so it gates nothing at all. It has been failing on `main` since at least 2026-05-24 — the
last push to that branch — with `ci-summary` reporting success on the same run.*

**Why this is recorded and not patched.** The fix that works in the bootstrap stack is a one-shot
privileged init container that chowns the volume — and **that fix has not yet been observed working**.
`timed-onboarding` has not returned a verdict from any head containing it. Porting an unverified
repair into the production compose file, from an environment with no Docker daemon to test it in,
would be guessing twice: once about whether the pattern works, once about whether it works here.

The evidence above is complete enough to act on the moment the bootstrap verdict lands. If the chown
init container works there, the same three lines belong in `docker-compose.yml`. If it does not, this
finding wants a different fix and porting the broken one would have cost a cycle.

**The broader point.** Five bootstrap breaks, and now the full stack carries one of them too. The
common cause is unchanged and is not about any individual defect: `docker-smoke` runs on push to
`main` only, so the full stack has no pull-request build, exactly as the bootstrap stack had none
before GRVX-910. The argument that produced `timed-onboarding` — *a gate that only runs after merge
gates nothing* — applies unchanged to `docker-smoke`, and the evidence for it is now four findings
deep.

## F-026 — `ci-summary` reports success while `docker-smoke` and `docker-build` fail, and both have been failing on `main` for months

**Found by** verifying a claim made in F-025 — that `docker-smoke` "would have caught" the full
stack's volume-ownership defect — instead of leaving it standing.
**Severity** high. The full-stack smoke test and every container image build have been red on `main`
since at least 2026-05-24, and the check that is supposed to summarise CI has been green throughout.
**Status** open. Not fixed here: see below.

**The gate does not include them.** `ci-summary`'s `needs`, verbatim from
`.github/workflows/ci.yml`:

```
lint, vuln, test, build, e2e, sdk-tests, helm-validate, docker-lint,
correctness, recipe-examples, timed-onboarding
```

`docker-smoke`, `docker-build` and `image-scan` are absent, and `docker-smoke` appears exactly once
in the whole workflow — in its own job definition. It is in no `needs` list and no failure condition.
A red `docker-smoke` therefore blocks nothing, reports to nothing, and is visible only to someone who
opens the run and scrolls.

**Observed, on the last push to `main`** (run 26356622161, commit 5854204, 2026-05-24):

| Job | Conclusion |
|---|---|
| `docker-smoke` | **failure** |
| `docker-build (rollup, services/rollup/Dockerfile)` | **failure** |
| `docker-build (gateway)`, `(ingestion)`, `(load-generator)` | cancelled — fail-fast from the rollup build |
| `image-scan` | skipped |
| **`ci-summary`** | **success** |

Every other job passed. The summary said so, and it was not wrong by its own definition — it never
looked.

**How long.** `main` has 26 CI runs and its most recent push is 2026-05-24, roughly three and a half
months ago. The five most recent `main` runs all conclude `failure`. So the full stack's smoke test
and the rollup image build have been broken for that entire period, through every subsequent piece of
work, with no signal anywhere that said so.

**Two consequences worth separating:**

1. **No full-stack verification has run since May.** F-025's defect could not have been caught by
   `docker-smoke` in any sense — not because of F-019, which came later, but because the job's result
   is discarded.
2. **The published container images are stale.** `docker-build (rollup)` has failed since May, and
   the three other image builds were cancelled by its fail-fast, so `ghcr.io` has had no successful
   publish from `main` in that window.

**Why the 0-second failure cannot be diagnosed.** `docker-smoke`'s "Run full-stack smoke test" step
started and completed within the same second, so the script failed almost immediately rather than
after a stack boot. The logs are past GitHub's 90-day retention and return `410 Gone`, so the cause
is not recoverable from that run. `scripts/smoke_test.sh` is executable and has a correct shebang, so
it is not that. Reproducing it needs a Docker daemon, which the implementation environment does not
have.

**Why this is recorded and not fixed.** Adding `docker-smoke` to `ci-summary`'s `needs` is one line,
and it would immediately turn a green summary red on every pull request — correctly, but for a
failure nobody has diagnosed, on a branch whose own last run predates all current work. Doing that
without first knowing *why* it fails converts an invisible problem into a blocking one with no fix
attached, which is a decision about how to sequence the repair rather than an implementation detail.

**The order that makes sense:** `timed-onboarding` goes green, proving the bootstrap stack boots →
reproduce `docker-smoke`'s failure with a Docker daemon → fix it → *then* add it to `ci-summary`'s
`needs`, along with `docker-build`, so neither can rot again.

**The pattern, for the fifth time this phase.** F-015, F-016, F-019, F-021, F-023 and F-025 were all
invisible because nothing executed the thing they broke. This one is worse in kind: the check *did*
execute, it *did* fail, and the gate that aggregates CI simply did not ask. A job whose result is not
in a required check is a job that does not exist.

---

## F-027 — the rollup jobs and ingestion disagree about where data lives, so no metric is ever produced

**Found by:** `senior-engineer` diagnosing the `timed-onboarding` timeout on commit `2fab4f6`
**Owner:** `sre-release-manager`
**Severity:** critical — the bootstrap stack boots completely and shows an empty dashboard forever
**Status:** the producer half is FIXED and guarded. **The consumer claim below was wrong** — see
F-037. The rollup now writes `warehouse/<tenant-id>/…`, which the logs confirm, but Cube's
`isMultiTenant` conditional never evaluates true inside its schema compiler, so Cube globs the
single-tenant path and the two still do not meet. Half a fix; the half that is fixed is real.

`timed-onboarding` on `2fab4f6` — the first head carrying all five earlier bootstrap fixes — got the
whole stack up and then still failed:

```
23:08:28  gravix-bootstrap-init  Exited        ← seeded, exit 0
23:08:33  gravix-cube            Healthy
23:08:33  gravix-dashboard       Started       ← F-023 fixed
23:15:53  FAIL: no populated dashboard within 600s
```

Boot was no longer the problem. The poll was. Rotation is every 60s and the rollup every 300s, so a
number should have reached Cube around t=305 with 295s of budget to spare.

**The cause, in one line each:**

| Service | `TENANT_DB_PATH` | Consequence |
|---|---|---|
| `ingestion` | set | multi-tenant auth → `topicForTenant` → writes `raw/<tenant-id>/request_facts/…` |
| `request-metrics-rollup` | **unset** | single-tenant branch → reads `raw/request_facts/`, writes `warehouse/request_metrics_minute/` |
| `cube` | set | `isMultiTenant` → globs `warehouse/*/request_metrics_minute/**/*.parquet` |

Two independent severances in one pipeline. The rollup read a prefix nothing writes, so it produced
nothing; and had it produced anything, it would have written to a prefix Cube's glob does not match.
Each of the three services is individually correct. The stack is wired to two different layouts.

`service-events-rollup` and `purge` had the same omission. For `purge` the consequence is quieter and
worse: it walks the non-tenant prefixes, finds nothing to delete, and the disk grows past the 30-day
retention the README promises, with no error anywhere.

**The fix.** `TENANT_DB_PATH=/app/data/gravix.db` on all three jobs, plus
`depends_on: bootstrap-init: service_completed_successfully` so no job can open the SQLite file while
`bootstrap_seed` is still creating it. `docker-compose.yml` carried the identical defect on all four
of its data-plane jobs, and is fixed the same way. Note that this makes the `-input-dir`/`-output-dir`
flags in both files inert: multi-tenant mode derives both paths per tenant and ignores them. The flags
are left in place, commented, because they remain the documented single-tenant defaults.

**The guard, and why it names no service.** `cmd/onboarding_gate/compose_tenancy_test.go` holds two
tests. Neither contains a list of services that need the variable — a check that matches on a name
protects only the names its author knew about, and the next rollup job added to this stack would be
unprotected on the day it lands. The requirement is derived instead, in three hops:

1. the compose file says which Dockerfile builds each service,
2. the Dockerfile's `go build -o <binary> <package>` lines say where each binary's source is,
3. the package's own AST says whether a string literal `TENANT_DB_PATH` appears in it.

Any binary that answer applies to must be told which mode it is in, by environment variable or by an
explicit path flag. The second test states the invariant directly: within one stack, tenancy mode must
be unanimous.

Three further details are deliberate:

- **AST, not grep.** The first version searched file text and reported `cmd/bootstrap_seed` as
  tenancy-aware, because a *comment* there names the variable. That would have forced a meaningless
  environment variable onto a service that takes its database path as an explicit flag. A guard whose
  false positives are silenced by editing production code is worse than no guard.
- **A declared path must be inside a mount.** `TENANT_DB_PATH=/var/lib/gravix.db` on a service that
  mounts only `/app/data` fails exactly as if the variable were absent, and looks correct in review.
- **Finding nothing is a failure, not a pass.** Both tests fail if the derivation resolves zero
  tenancy-aware binaries. Seven guards in this phase passed while the thing they guarded was broken;
  a guard that silently finds nothing to check is the purest form of that.

**Mutation-tested, all three red then green on revert:** dropping the variable from one rollup fails
both tests; pointing it outside every mount fails the reachability assertion; restoring a duplicate
service key fails the load.

**The pattern, for the seventh time this phase.** F-015, F-016, F-019, F-021, F-023, F-025 and now
F-027 were all invisible because nothing executed the thing they broke. `timed-onboarding` found three
of them itself, including this one, which is the argument for the gate existing.

---

## F-028 — WITHDRAWN: a re-discovery of F-011

I recorded the duplicate `gateway:` key in `docker-compose.yml` as a new finding while adding the
F-027 guard across both compose files. It is not new. **F-011 recorded the same defect, in the same
file, at the same two line numbers**, and `.github/workflows/ci.yml` carried a comment naming F-011
as the reason `docker-compose.yml` was excluded from the compose-validation step.

I did not read either before assigning a number. The evidence was in the file I was editing.

The fix and its reasoning are real and stand; they belong to F-011, which is updated above rather
than duplicated here. This entry is left in place rather than deleted because the register is
append-only, and because a withdrawn number is itself worth seeing: it is what re-deriving a known
finding from scratch looks like, and the cost was a duplicate register entry plus a commit message
citing a number that does not mean what it says.

**Correction to commit `9691d51`.** Its message and `cmd/onboarding_gate/compose_tenancy_test.go`
both cite "F-028" for the duplicate-key defect. Read F-011. The commit is pushed and its message
cannot be amended without a force-push this environment denies; the code comment is corrected.

---

## F-029 — the full stack's gateway and Cube sign and verify with different secrets, so every token is rejected

**Found by** `senior-engineer`, re-reading `docker-compose.yml` after making it loadable for F-011.
**Owner** `security-engineer` for the second half; the first half is fixed here.
**Severity** high — an empty dashboard with every container healthy, and a public signing secret.
**Status** FIXED in full. The mismatch was fixed here; the bootstrap stack's shared public secret was
resolved by **F-032**, which found it also crash-looped the gateway and so collapsed the three
options below to the one that works. Option 1 is implemented — see F-032.

### The mismatch, and why fixing F-011 caused it

`docker-compose.yml` had the two halves of one auth path disagreeing:

| service | `JWT_SECRET` |
|---|---|
| `gateway` (line 54) | `${JWT_SECRET:?…}` — required from the environment |
| `cube` (line 205) | `supersecretjwtkey12345!` — a literal |

The gateway signs the dashboard's token with its value; `cube/cube.js` `checkAuth` verifies with its
own and reads `securityContext.tenant_id` out of the result, which `queryRewrite` turns into a
mandatory filter. Two different values mean Cube rejects **every** token the gateway issues, unless
the operator sets `JWT_SECRET` to the one value they must never use.

This was unreachable while F-011 stood: the duplicate `gateway:` key stopped Compose loading the file
at all, so neither half ran. **Fixing the duplicate is what made the mismatch reachable.** Worth
stating plainly — a fix that makes a file load without checking that it then *works* has moved the
failure, not removed it. The removed `gateway` block carried the same literal as `cube`, so the two
agreed in the half of the file that was discarded, and disagreed in the half that was kept. That is
exactly the kind of thing F-011 meant when it said the choice "silently changes how the full stack is
configured", and it is why that entry now records the diff in full.

**Fixed** by giving `cube` the same expression the gateway uses. Guarded by
`TestOneStackResolvesOneSigningSecret`, which compares the *expression* rather than the resolved
value: two services reading the same variable agree whatever the operator sets it to, while two
literals that match today drift the moment one is edited. Mutation-tested — reverting `cube` to the
literal, and pointing it at a differently-named variable, each fail; control green either side.

### Still open: the secret itself is public

`supersecretjwtkey12345!` remains in `docker-compose.bootstrap.yml` twice, on `gateway` and `cube`.
There the two agree, so the stack works — with a signing key that is published in this repository.
Anyone can mint a token for any `tenant_id` and read that tenant's metrics through Cube, because
`checkAuth` trusts the `tenant_id` in the token and `queryRewrite` filters on exactly that.

**This is not fixed here because the fix is a decision, not an edit.** The bootstrap stack's whole
premise is that `docker compose up` works with no `.env` editing, so `${JWT_SECRET:?…}` — the fix
applied to the full stack — would break the property GRVX-909 exists to provide. The options:

1. `bootstrap_seed` generates a random secret and writes it where both services read it, the way it
   already does for the API key and the dashboard password. Keeps zero-config; one more file to
   thread through two services.
2. `${JWT_SECRET:-<generated at first boot>}`, with the generation done by the init container.
3. Require it in `.env.bootstrap.example` and accept that the bootstrap stack no longer starts
   unedited.

Option 1 matches what `bootstrap_seed` already does for two other secrets and is the obvious
candidate, but it changes the file layout the init container owns and is worth someone's deliberate
yes. Recorded rather than improvised, per the register's own rule.

**A note on how this was found.** I nearly filed it as a new finding without checking, having made
exactly that mistake an hour earlier with F-028. Searching first showed the literal is *cited* in
F-016 and in `spec-defects.md` as context for the credential chain — but no entry owns it, and the
gateway/cube mismatch appears nowhere. So the number is new and the search is recorded here so the
next person does not have to repeat it.

---

## F-030 — a test file inside Cube's model directory stopped Cube serving any data, from Phase 8 until now

**Found by** the `timed-onboarding` diagnostics added for this purpose, on commit `9691d51`.
**Owner** `semantic-modeler`
**Severity** critical — Cube compiled no schema at all, so every query errored, for roughly a month
of subsequent work.
**Status** FIXED and guarded with Cube's own parser.

### What the log said

With F-027 fixed, the rollup did its job and said so:

```
"multi-tenant mode", "active_tenants":1
"processing tenant", "tenant_id":"ccab18fb-3932-446a-accf-d1bb54924052"
"uploaded metrics", "row_count":430,
  "dest_key":"warehouse/ccab18fb-…/request_metrics_minute/event_day=2026-09-11/request_metrics_minute_20260911.parquet"
```

430 rows, on the per-tenant path Cube's glob reads. The data was there. Cube's log, the next block
down:

```
{"message":"Compiling schema error", …
 "error":"Error: Compile errors:\n\nschema/RequestMetricsMinute.test.js:17\n
 const here = dirname(fileURLToPath(import.meta.url));\n
 SyntaxError: Cannot use 'import.meta' outside a module\n
     at new Script (node:vm:99:7)
     …
     at DataSchemaCompiler.compileJsFile (…/DataSchemaCompiler.js:256:10)"}
```

### The mechanism

`docker-compose.bootstrap.yml` mounts `./cube/model` at `/cube/conf/model`, and Cube's
`DataSchemaCompiler` compiles **every** `.js` file it finds there with `new vm.Script(src)`. That is
not an ES module context, so `import.meta` is a hard `SyntaxError` — and `throwIfAnyErrors` fails the
**entire** compile on one bad file. No cube is defined, so `RequestMetricsMinute.requestCount` does
not exist and every query returns an error.

`cube/model/schema/RequestMetricsMinute.test.js` was added in `9d21c66` (GRVX-808, Phase 8) and sat
beside the model it tests. So Cube has not served a row since Phase 8.

### Why fourteen tests in that very file did not catch it

They load the model by reading it and evaluating it with a stubbed `cube()` under `node --test`,
where the file is a module and `import.meta` is legal. The tests assert things *about* the model and
never ask Cube whether it can compile the directory. They passed continuously while the file they
live in made the model uncompilable.

This is the same shape as the AC-8 hole found earlier today, and the ninth instance this phase: **a
guard written against the mechanism that produces a property passes for every route to breaking the
property that does not go through that mechanism.** Here the property is "Cube can serve this model"
and the mechanism tested was "the model's definitions are correct". The file's own presence was
outside what it examined.

### The fix

Moved to `tests/cube/RequestMetricsMinute.test.js`, outside the mount, with `Makefile`,
`.github/workflows/ci.yml` and `CONTRIBUTING.md` updated. All 109 JS tests pass from the new
location.

`tests/cube/model_compiles.test.js` guards it by running **Cube's own parser**: it derives the
mounted host directory from the compose files, walks it, and calls `new vm.Script` on every `.js` —
the exact call in the stack trace above. A check for `import`/`export`/`import.meta` keywords would
protect the constructs its author thought of; this rejects precisely what Cube rejects, including
constructs nobody has written yet. It also fails if it finds no mount or no file, because a guard
that silently checks nothing is the failure this register keeps recording.

Mutation-tested four ways, control green either side:

| Mutation | Result |
|---|---|
| the ESM test file back in `cube/model/` | fails |
| a *model* file using `import` (no "test" in its name) | fails |
| an unbalanced brace in a model file | fails |
| the mount path renamed, so the guard watches nothing | fails |

### The postscript, now fixed

`docker-compose.bootstrap.yml` mounts `./dashboards` as nginx's document root, so
`dashboards/lib/*.test.js` was served over HTTP at `/lib/*.test.js` on every deployed bootstrap
stack. Nothing broke — and the bytes are public on GitHub anyway — but a dashboard handing out its
own test suite is not what this project should look like.

**Fixed at the serving layer, not by moving the files.** `storage/dashboard/nginx.conf` gains a
`location ~* \.test\.js$` block returning 404. That was chosen over relocating the five files to
`tests/dashboards/` deliberately: the property is *"the dashboard does not serve tests"*, and a rule
holds for files nobody has written yet where a one-time move does not. It is also five files of path
rewriting avoided on a branch that is already red.

**The ordering is load-bearing and invisible.** nginx evaluates regex locations in file order and
takes the first match, so the same deny rule placed *after* the existing `\.(css|js)$` block never
runs — and the config still loads, still reads correctly, and still serves the tests. A reviewer
sees a deny rule and has no reason to suspect position matters.

`TestDashboardDoesNotServeTestFiles` therefore checks three things: the rule exists, it precedes the
`\.(css|js)$` block, and its pattern matches every `*.test.js` in the tree while matching none of
the application scripts. It fails if it finds zero of either kind, since a check that matches nothing
proves nothing.

Mutation-tested, control green either side, each edit verified as applied: removing the rule fails,
moving it after the `css|js` block fails, and broadening the pattern to `\.js$` — which would block
`app.js` and break the dashboard — fails.

One mutation of mine did *not* fail, and it was my mutation that was wrong rather than the guard:
`test\.?js$` still requires the literal `test`, so it never matched application code and was not the
broadening I had described. Re-run with `\.js$`, the guard caught it. Worth recording because a
mutation that does not do what its label claims is indistinguishable from a guard that does not
work.

### What the gate has now found

`timed-onboarding` (GRVX-910) has returned five findings on five runs and has not yet passed:
F-019, F-021, F-023, F-027 and F-030. Every one was a real, total break of the documented
`docker compose up` path, and every one was invisible to the rest of the suite. Its Definition of
Done stays unticked and §11.4's elapsed time stays unmeasured, because it has not passed.

---

## F-031 — the prove-it gate goes red at every UTC midnight, and the docs page published a digest reproducible for one day

**Found by** the pre-push verification sweep, minutes after the date rolled to 2026-09-12.
**Owner** `semantic-modeler`
**Severity** high — `test-correctness` is in `ci-summary`'s `needs`, so this blocks every pull
request, every day, until someone regenerates a page by hand.
**Status** FIXED.

### How it surfaced

`make test-correctness` passed at 23:4x UTC and failed at 00:0x, on the same commit, with no change
in between. Verified by stashing all working changes and re-running against the pushed `be0a3a2`:
still red. So it was the clock, not the diff.

`TestDocsPageClaimsAreScriptOutput` runs `scripts/prove_it.sh` live and requires every line of the
published transcript to be a line the script actually printed. Four lines stopped matching:

```
data file:     warehouse/request_metrics_minute/event_day=YYYY-MM-DD/request_metrics_minute_20260903.parquet
idempotency:   request_metrics_minute:v2:_single:20260903
digest:        sha256:db3b4faca1add9c1396021f2bf633ca9c47922fb66a70ef9efbe6de354defafc
revision:      1  (revised YYYY-MM-DDTHH:MM:SSZ, previously sha256:9b4a9ce6653529ea1fd8407a74975b61bd81e0baa7fdd037a4702797a78087f9)
```

### Why

`scripts/prove_it.sh:53-54` anchors the dataset to the seven days ending yesterday. Its own comment
explains why, and the reason is sound:

> It has to be recent rather than fixed: `gravix evolve` refuses a window older than fact retention,
> quite rightly, and a demonstration that tripped that guard would be demonstrating the guard.

So on 2026-09-11 the window was 09-03…09-10 and the partition day was `20260903`. At the next UTC
midnight it became `20260904`, and every digest over that partition changed with it.

The test already normalises volatile text before comparing — work directory, `runtime: Ns`,
timestamps, and dates. The page is written against those placeholders, which is why it shows
`event_day=YYYY-MM-DD`. **The normaliser covered `\d{4}-\d{2}-\d{2}` and nothing else.** The same
dates in their compact `\d{8}` spelling — in the Parquet filename and the idempotency key — went
through untouched, as did the digests. Two spellings of one value, one of them handled.

### Fixed, without touching the script

The floating window is correct and was left alone. The normaliser now also covers:

- `([_:])(20\d{6})\b` → `${1}YYYYMMDD`, anchored to the `_` or `:` before it so an eight-digit row
  count is not mistaken for a date;
- `sha256:[0-9a-f]{64}` → `sha256:<64 hex>`.

### The published-number half

Masking the digest in the comparison is only correct because **the page should never have promised
one**. A reader who ran the demo the next day got a different digest with no explanation, from a
project whose central claim is recomputability — the worst possible place to look non-deterministic.

The page now carries a note saying the dates and digests will differ on their run, why the window
floats, and what the demo actually proves: that a recompute reproduces the digest *that same run*
just produced, which the `PROVED` block asserts against the reader's own output. It was never a
promise that they would see this page's digest.

### What the guard still catches

Loosening a comparison is worth checking rather than asserting. Mutation-tested, control green
either side:

| Mutation | Result |
|---|---|
| page claims metric version `v3` instead of `v2` | fails |
| page claims `rows written: 9999` | fails |
| page claims `partitions: 8   rebuilt: 8` | fails |
| page says `lakehouse/` instead of `warehouse/` | fails |

Only the two genuinely day-dependent values are masked.

**A method note, for the second time.** My first mutation run reported a PASS for `row count:` →
`row counts:`. The page contains no such line, so `sed` changed nothing and the "PASS" meant the
harness had tested an unmodified file. The same false negative is recorded in GRVX-1004 §11.4 for
the same reason. The harness now takes an `md5sum` before and after each edit and reports SKIP rather
than PASS when the file is unchanged. **A mutation test that cannot tell "the guard held" from "I
mutated nothing" is not evidence**, and it fails in the direction that flatters the guard.

---

## F-032 — the bootstrap gateway crash-looped on every boot, so the dashboard's login could never succeed

**Found by** the `timed-onboarding` diagnostics on `d344411`, in the `docker compose ps -a` table.
**Owner** `sre-release-manager`
**Severity** critical — the gateway has never started in the bootstrap stack.
**Status** FIXED, with three guards.

### What the table said

```
gravix-gateway   gravix-dashboards-gateway   "./gateway ./gateway…"   Restarting (1) 46 seconds ago
```

Two separate faults are visible in that one line.

### Fault 1: the signing secret was 23 characters, and the gateway requires 32

`services/gateway/main.go:221` exits 1 when `JWT_SECRET` is shorter than 32 characters.
`docker-compose.bootstrap.yml` set `JWT_SECRET=supersecretjwtkey12345!` — **23 characters**. So the
gateway exited 1, was restarted, and exited 1 again, forever. Deterministic, on every boot, since the
value was introduced.

The poll's first step is `POST /api/gateway/login`, so no token was ever obtained and
`RequestMetricsMinute.requestCount` was never queried. Every earlier finding this gate produced —
F-019, F-021, F-023, F-027, F-030 — was in front of this one in the pipeline, which is why each had
to be cleared before this became visible.

**This resolves F-029's open half.** That entry left the choice of how to handle the published secret
to an owner, listing three options. The choice has collapsed: a longer hardcoded literal is no better
functionally and strictly worse for security, so option 1 — `bootstrap_seed` generates it — is the
only one that both starts the stack and keeps zero-config. Implemented:

- `cmd/bootstrap_seed` writes a 256-bit URL-safe secret to `data/jwt_secret.txt`, mode 0600, and
  **reuses an existing one**. Regenerating would invalidate every issued token and, worse, could be
  written while Cube still held the old value — a stack that disagrees with itself about its own
  signing key presents as an empty dashboard, not as an authentication error.
- `services/gateway/main.go` accepts `JWT_SECRET_FILE` / `--jwt-secret-file`.
- `cube/cube.js` reads the same file. A missing file **throws** rather than falling back to
  `JWT_SECRET`: falling back would leave Cube verifying with a different value than the gateway signs
  with, which is the silent-empty-dashboard failure again.

The rejection message now reports the length and where the value came from. It previously named only
the rule, so a crash loop caused by a 23-character string said nothing about which string or which
source — and cost a full CI cycle to identify.

### Fault 2: `command` repeated the binary the image already runs

The image declares `ENTRYPOINT ["./gateway"]`; the compose service set
`command: ["./gateway", "--tenant-db", ...]`. Compose **appends** `command` to `ENTRYPOINT`, so the
process ran as `./gateway ./gateway --tenant-db /app/data/gravix.db`. Go's `flag` package stops at
the first positional argument, so `--tenant-db` was silently dropped.

Nothing broke: `TENANT_DB_PATH` covered it. `ingestion` had the identical defect in **both** compose
files, where the dropped `--base-dir` happened to equal the built-in default. A flag that is ignored
while its value is right by coincidence is a defect that surfaces on the day someone changes the
value.

### Guards

| Test | What it asserts |
|---|---|
| `TestServiceCommandDoesNotRepeatTheEntrypointBinary` | derives ENTRYPOINT and CMD from each service's Dockerfile and checks `command` against the right rule for each |
| `TestNoSigningSecretIsHardcodedInCompose` | no `*SECRET*`/`*PASSWORD*`/`*TOKEN*`/`*API_KEY*` variable may hold a literal — it must interpolate or be a `_FILE` path |
| `TestProvisionWritesAUsableJWTSecret` | the generated secret is 0600 and at least the 32 characters the gateway enforces |
| `TestReprovisionKeepsTheSameJWTSecret` | a second provision does not replace it |

**ENTRYPOINT and CMD are checked separately, and that distinction was found the hard way.** The first
version of the command guard used one regex for both and reported `load-generator` as defective.
Compose *appends* to ENTRYPOINT but *replaces* CMD, so naming the binary is a bug against the first
and mandatory against the second — `load-generator`'s image has only CMD, and "fixing" it would have
broken a working service. The guard now enforces the opposite rule in each case.

Mutation-tested, control green either side: reintroducing the duplication on the gateway and on
ingestion, restoring the short literal, giving Cube a *long* literal (still published, still caught),
writing the old literal instead of a random secret, mode 0644, and regenerating on every provision —
each fails.

### The diagnostic that missed it

`diagnose()` printed the logs of `bootstrap-init`, `ingestion`, `request-metrics-rollup`, `cube` and
`synthetic-traffic`. All five were working. **The gateway was not in the list**, and it is the first
dependency of the thing being measured. The function printed a page of healthy services while the
failure sat one column over in a table nobody was reading closely.

Fixed: `gateway` is now first in that list, `dashboard` was added too, and the function counts
`Restarting` containers and prints an explicit line naming them, because `docker compose ps` reports
a crash loop without drawing any attention to it.

---

## F-033 — ingestion writes every recovered batch twice, once to a path nothing reads

**Found by** reading the ingestion log in the same diagnostic output.
**Owner** `senior-engineering-lead`
**Severity** **high, revised up from medium.** The duplicate storage is the lesser half. A batch that
is recovered *only* by this sweep — the crash-recovery case it exists for — lands under a prefix the
multi-tenant rollup never scans, so those facts are durably written and never read again. That is
silent loss, on the one path whose entire purpose is preventing loss, in a project whose first
principle is that facts are immutable and authoritative.
**Status** FIXED and guarded.

### The cause

`services/ingestion/main.go`'s `startupScan` inferred the topic from the parent directory's *name*:

```go
dir := filepath.Dir(path)
topic := filepath.Base(dir)
```

Rotation writes to `filepath.Join(ds.bufferDir, topicForTenant(id, topic))`, so the on-disk layout is
`buffer/<tenant-id>/request_facts/`. `filepath.Base` of the parent yields `request_facts` and drops
the tenant. And it is not only a startup scan: `retryLoop` calls it **every five minutes**, so any
file present when the sweep runs is mis-filed.

### The fix

Resolve the topic *relative to the buffer root* — `filepath.Rel(ds.bufferDir, dir)` — which yields
`<tenant-id>/request_facts` in multi-tenant mode and plain `request_facts` in the legacy layout,
without needing to know which is in use. A path that escapes the buffer root is skipped with an
error rather than guessed at.

A side effect worth naming: both uploaders now target the **same** key, so the duplicate-write half
becomes idempotent. Before the fix one batch produced two objects under two prefixes; now it produces
one.

### The guards

`TestStartupScanRecoversUnderTheTenantPrefix` asserts on the destination **key**, because that is the
property — a recovered object has to be somewhere the reader looks — and asserts exactly one object
per batch. `TestStartupScanStillWorksWithoutATenant` covers the legacy layout, so the fix cannot be
"correct" by breaking single-tenant recovery. Mutation-tested: restoring `filepath.Base` fails,
control green either side, with the edit verified as applied.

### Original note, kept

Recorded first as medium and deferred; the severity was wrong because the analysis stopped at the
duplicate write and did not ask what happens when the sweep is the *only* uploader.

Three consecutive lines for one batch file:

```
INFO  found orphaned batch file    path=data/buffer/<tenant>/request_facts/batch_….jsonl
INFO  uploaded to storage          key=raw/<tenant>/request_facts/2026-09-12/00/batch_….jsonl
WARN  uploaded but failed to remove local file   error=… no such file or directory
INFO  uploaded to storage          key=raw/request_facts/2026-09-12/00/batch_….jsonl
```

The same file is uploaded twice: once under the tenant prefix and once under the bare, non-tenant
prefix, with the failed local removal in between showing the two paths racing over one file. The
second copy lands where the rollup — in multi-tenant mode since F-027 — never looks, so it is pure
waste that also inflates any bytes-per-event measurement taken from disk.

It bears on GRVX-1003's storage figures and on the cost claims in GRVX-1004, since both measure what
is on disk. Whoever picks it up should start at the orphan-recovery sweep in
`services/ingestion/main.go`, which appears to compute its topic without the tenant that the normal
rotation path applies via `topicForTenant`.

---

## F-034 — the poll never attempted a login, because the credential is unreadable from the host

**Found by** the `timed-onboarding` diagnostics on `8536ea3`, one line above the container table.
**Owner** `product-designer` for the usability half; the harness half is fixed here.
**Severity** high — the gate spent 600s reporting "no data" without ever trying to authenticate.
**Status** harness FIXED. The usability question is **open** and needs a decision.

### What the log said

```
data/login.txt:
  (absent — bootstrap-init never wrote it)
```

And, fourteen lines below, from `bootstrap-init` itself:

```
jwt signing secret written to /app/data/jwt_secret.txt
dashboard login written to /app/data/login.txt
  password: zlUUI-lWGnTOci1n0MxIm1uH159NjcLAH-97vdR5UEI
```

Both are true. `bootstrap_seed` writes `login.txt` mode **0600** — it is a password — and
`bootstrap-init` chowns `/app/data` to the image's `gravix` user to fix F-021. That user comes from
`adduser -S`, so its uid is whatever Alpine assigned and is never the uid running the test script. A
0600 file owned by another uid fails `[ -r ]`, so:

```bash
fetch_token() {
    [ -r "$LOGIN_FILE" ] || return 0      # ← returned here, every iteration, for 600s
```

The poll never issued `POST /api/gateway/login`. The gateway was healthy the whole time.

**F-021's fix is what made this appear.** Before the chown, `./data` was root-owned and the script
could not read it either — but the stack did not boot at all then, so the guard was never the thing
that mattered. Each fix in this chain has exposed the next.

### Fixed in the harness

`read_login_file` now reads the credential through a service that runs as that user —
`docker compose exec -T gateway cat /app/data/login.txt` — and falls back to the host file for anyone
running the script outside CI with matching ownership. Verified against stubs for all three routes:
container-readable, host-readable, neither.

### The reporting defect behind it, which cost the run

`diagnose()` printed:

```
token obtained:              no
```

"No" covers two completely different failures: *a login was attempted and refused*, and *no login was
ever attempted*. They need opposite investigations, and the line could not tell them apart. It now
reports the reason:

```
token obtained:  no — the generated login could not be read, from the gateway container or the host
token obtained:  no — the gateway login was attempted; see the response above
```

Both verified against stubs. This is the second time in two runs that the diagnostic, not the stack,
was what hid the answer — the first was `gateway` missing from the service list in F-032. A
diagnostic is code, and it needs the same suspicion as the code it inspects.

### Open: a first-time user cannot read their own password either

This is not only a test-harness problem. The README points at `data/login.txt`, and after
`docker compose up` on a fresh VPS that file is mode 0600 owned by a uid that does not exist on the
host. **The person who just installed Gravix cannot read their own dashboard password without
`sudo`.** The working path today is `docker compose logs bootstrap-init`, which prints it — but that
is not what the documentation says.

The options, none of which should be chosen by an implementer:

1. Write `login.txt` 0644. It is a local development credential on a single-tenant box, and the
   directory is already the operator's. Simple; weakens a deliberate choice.
2. Keep 0600 and change the documentation to `docker compose logs bootstrap-init` as the primary
   route. No security change; the file becomes a secondary artefact.
3. Have `bootstrap-init` chown the data directory to the **host** uid, passed in as an environment
   variable. Correct on a single-user box, more moving parts, and wrong in a shared deployment.

Option 2 is the smallest honest change and option 1 is the most convenient; the choice is about how
the bootstrap stack expects to be operated, which is a product question.

### No unit-level guard, and why

The property is "the poll attempts a login when the stack is up", and proving it needs a running
stack — there is no Docker daemon in the implementation environment. A test asserting that
`fetch_token` does not contain a particular `[ -r ]` line would match on text, protect only the
spelling its author knew, and pass while the property was broken; that is the pattern this register
has now recorded nine times. The gate itself is the test. What changed is that its failure now names
the stage, so the next occurrence is one read rather than a full cycle.

---

## F-035 — Cube answers every query with a Cube Store error, because the bootstrap stack runs no Cube Store

**Found by** the poll's own authenticated query, on `a2a5c68`.
**Owner** `semantic-modeler`
**Severity** critical — no query can succeed, so the dashboard is empty however correct the data is.
**Status** FIXED, with the trade-off stated.

### The evidence, finally from the query path

Earlier runs showed this error, but every instance carried `requestId: "scheduler-…"` — the
background refresh scheduler and the pre-aggregation loader. Whether an *ad-hoc* query also failed was
unobservable, because no token existed to make one with. F-034's fix produced the token, and the
answer arrived:

```
last gateway login response: {"email":"local@gravix.invalid","email_verified":true,"plan":"free",
                              "role":"admin","tenant_id":"d46186c8-…","token":"<redacted>",
                              "user_id":"06ecff74-…"}

last Cube response: {"error":"Error: Cube Store was specified as queue/cache driver. Please set
                     CUBEJS_CUBESTORE_HOST and CUBEJS_CUBESTORE_PORT variables."}
```

The gateway authenticates and mints a JWT. Cube rejects the query that carries it — not on
authentication, but before reaching the data at all. The stack trace in the container log shows why:
`QueryCache.loadRefreshKeys` is reached from `OrchestratorApi.executeQuery`, so the cache driver sits
on the ordinary query path and not only the scheduler's.

`CUBEJS_DEV_MODE` defaults to `false` in this compose file, and in production mode Cube v0.35 defaults
its queue/cache driver to Cube Store — a separate service the bootstrap stack does not run.

### Fixed

`CUBEJS_CACHE_AND_QUEUE_DRIVER=memory` on the `cube` service: the documented single-node driver, no
extra container, nothing against the ~800MB RAM budget or the $20/month figure. Adding a `cubestore`
service was the alternative and was rejected — it would contradict the premise the whole stack exists
to demonstrate.

`CUBEJS_SCHEDULED_REFRESH_TIMER=false` alongside it, and `cube/cube.js` now reads that variable
instead of hardcoding `scheduledRefreshTimer: 300`. A literal there overrode the environment, so the
bootstrap stack could not turn the scheduler off; with no pre-aggregations to refresh its only output
was the error above, every 300 seconds. Verified: unset → 300, `"false"` → `false`, `"600"` → 300 (only
the explicit string disables it, so an unrecognised value keeps the safe default).

### The trade-off, stated rather than buried

**Pre-aggregations will not build in the bootstrap stack.** They need Cube Store to hold their rollup
tables, and `cube/model/schema/` declares three (`endpointDaily`, `recentEvents`, `dailySummary`).

That is the right call here — DuckDB reads the Parquet directly, and a rollup buys a single-node demo
nothing — but it is a real difference from the full stack, and two consequences follow:

1. **GRVX-1006's latency figures will be measured without pre-aggregations.** Any number taken from
   this stack is a cold-read-from-Parquet number. Quoting it as a pre-aggregated figure would be the
   same category of error as F-020 and F-022.
2. The model files are shared, so the full stack keeps its pre-aggregations. Nothing in the model
   changed; only where it runs.

**Residual uncertainty, admitted.** Whether Cube errors when a query *matches* a pre-aggregation it
cannot build is not verified — the implementation environment has no Docker daemon, and the poll's
query (`requestCount`, no dimensions, no time dimension) could in principle be routed to
`endpointDaily`. If the next run shows a pre-aggregation error rather than a result, the answer is to
stop declaring those rollups for this stack, and that will be a separate finding with its own
evidence. One informed change beats a stack of guesses; this one is informed by the query response
above.

### A reporting correction

`diagnose()` said:

```
token obtained:              no — the gateway login was attempted; see the response above
```

Misleading. A token *was* minted; the poll loop then discarded it, because it clears the token
whenever a Cube query stops working — reasonable for an expired credential, wrong when Cube is broken
for an unrelated reason. The line pointed at the login, which was fine. It now reports:

```
token obtained:              yes earlier, then discarded because a Cube query failed
                             (so the login works; read the Cube response above)
```

Verified against a stub, including that no token material reaches the log.

**Third run in a row where the diagnostic needed fixing alongside the stack** — `gateway` missing from
the service list (F-032), a bare "no" covering two opposite failures (F-034), and now a "no" that was
actively wrong. Worth stating as a pattern: a diagnostic is written when the failure is not yet
understood, so its first version encodes the author's wrong model of what can go wrong. It earns
trust the same way the code does, by being wrong in front of you and corrected.

### An ordering flaw in the diagnostic, noted

The summary lines — login response, token, Cube response — print at the **top** of the diagnose block,
which puts them furthest from the end of the log. Reading this failure needed three widening fetches
(118, 158, 190 lines) and the fourth exceeded the tool's response limit and had to be grepped from a
file. Logs are read from the end. The most important lines should be last, next to the `FAIL`. Not
changed in this commit — it is presentation, and this commit is already carrying a stack fix — but it
is the next thing to do to this script.

---

## F-036 — Cube routes a query to a rollup it cannot build, and fails it rather than reading the source

**Found by** the `timed-onboarding` poll on `18cede3`, immediately after F-035's fix took effect.
**Owner** `semantic-modeler`
**Severity** critical — the last link in the chain; every query still failed.
**Status** superseded. The finding itself — Cube fails a query that matches a rollup it cannot
build, rather than reading the source — stands, and it is why the models declare no
pre-aggregations today. **The fix below was wrong twice over.** It took effect for the wrong
reason (`process.env` is invisible to Cube's compiler, so `hasExternalStore` was false regardless
of the cache driver — F-037), and the `cond ? {…} : {}` form it introduced could never have
compiled in the first place, because a ConditionalExpression breaks the transpiler's member
resolution — F-038. The guard written for it passed only because it injected `process` into its own
sandbox. Read F-037 and F-038; the table of verified states below is not real.

### The error moved, which is how we know the previous fix worked

F-035 set `CUBEJS_CACHE_AND_QUEUE_DRIVER=memory`. The Cube Store error disappeared and a different
one replaced it:

```
last Cube response: {"error":"Error: externalDriverFactory is not provided. Please use
                     CUBEJS_DEV_MODE=true or provide Cube Store connection env variables
                     for production usage."}
```

This is precisely the residual uncertainty F-035 recorded and could not resolve without a Docker
daemon: **whether Cube errors when a query matches a pre-aggregation it cannot build.** It does.
`externalDriverFactory` is the mechanism that writes a rollup table, Cube Store provides it, and this
stack deliberately runs none.

The important behaviour, worth stating because it is not obvious: **Cube does not degrade
gracefully.** Faced with a rollup it cannot materialise, it fails the query rather than falling back
to the source it could have read directly. The poll's query — `requestCount`, no dimensions, no time
dimension — is a subset of `endpointDaily`, so Cube preferred the rollup and stopped.

The same output confirms two earlier fixes held: the gateway returned a JWT, and the corrected
reporting line read `token obtained: yes earlier, then discarded because a Cube query failed`, which
is exactly what happened.

### Fixed

`cube/model/schema/` now declares its three rollups only when an external store exists:

```js
const hasExternalStore = (typeof process !== 'undefined' && process.env &&
    !(process.env.CUBEJS_CACHE_AND_QUEUE_DRIVER === 'memory' && !process.env.CUBEJS_CUBESTORE_HOST));
…
preAggregations: hasExternalStore ? { … } : {}
```

**The condition is derived from the capability, not read from an on/off flag.** A flag can be set to
disagree with the infrastructure — someone turns rollups "on" in a stack with no store and gets this
same failure back. No external store means no pre-aggregations; there is no third state, and encoding
it this way makes the contradiction unrepresentable.

Verified across all three configurations:

| Environment | rollups declared |
|---|---|
| default (full stack) | 2, 1, 1 |
| `memory` driver, no Cube Store (bootstrap) | 0, 0, 0 |
| `memory` driver **with** `CUBEJS_CUBESTORE_HOST` | 2, 1, 1 |

That third row is the one that matters for the guard. A condition keyed on the cache driver alone
would pass the first two and silently strip rollups from a stack that has a perfectly good store.

### The guard

`tests/cube/model_compiles.test.js` checks all three states, and fails if no cube declares a
pre-aggregation in any of them — a condition that is always true and one that is always false both
look like success from one side only.

Mutation-tested, control green either side, with the harness verifying each edit actually applied:

| Mutation | Result |
|---|---|
| rollups declared unconditionally (the original defect) | fails |
| rollups withheld unconditionally (full stack loses them too) | fails |
| condition keyed on the driver alone, ignoring a named Cube Store | fails |

### Consequence for GRVX-1006, restated

The bootstrap stack now serves queries straight from Parquet via DuckDB. Any latency figure measured
here is a **cold read**, not a pre-aggregated one. Quoting it as the latter would be the same category
of error as F-020 and F-022 — a number that is real but not the number the claim needs. The full
stack keeps its rollups, so a pre-aggregated figure has to come from there.

### Nine runs, nine findings

F-019, F-021, F-023, F-027, F-030, F-032, F-034, F-035 and now F-036. Every one was a total break of
the documented `docker compose up` path, and every one was hidden behind the one before it — this
gate could only ever see the first unfixed link in the chain. That is an argument for the gate
existing, and equally an argument against trusting a stack nothing has executed end to end.

---

## F-037 — `process.env` is invisible to Cube's schema compiler, so every environment conditional in the model has always taken its fallback branch

**Found by** the `timed-onboarding` poll on `238a58c`.
**Owner** `semantic-modeler`
**Severity** critical, and it subsumes part of two earlier findings.
**Status** **FIXED.** The decision the earlier draft of this entry deferred turned out not to be a
decision: reading Cube v0.35's compiler settled it, and none of the three options below was the
answer. See "How it was actually fixed" at the end.

### The evidence

F-036's fix removed the pre-aggregation error and revealed this:

```
SELECT sum("request_metrics_minute".request_count) "request_metrics_minute__request_count"
  FROM gravix.raw.request_metrics_minute AS "request_metrics_minute" LIMIT 10000

Error: Binder Error: Catalog "gravix" does not exist!
```

`gravix.raw.request_metrics_minute` is the **Trino** table reference. DuckDB is the database. The
model chooses between them:

```js
const isDuckDB = (typeof process !== 'undefined' && process.env && process.env.CUBEJS_DB_TYPE === 'duckdb');
const requestMetricsSql = isDuckDB
  ? `SELECT * FROM read_parquet('/cube/data/warehouse/…')`
  : `SELECT * FROM gravix.raw.request_metrics_minute`;
```

`CUBEJS_DB_TYPE=duckdb` **is** set on the `cube` service — line 147 of
`docker-compose.bootstrap.yml`. The Trino branch was taken anyway. Therefore
`process.env.CUBEJS_DB_TYPE` did not read `duckdb` inside the compiler: Cube compiles model files
with `vm.runInNewContext`, and that sandbox does not carry the process environment.

Two pieces of corroboration were already in the repository:

1. The `typeof process !== 'undefined'` guards exist at all. Nobody writes that against Node's real
   `process`; whoever wrote them suspected the sandbox.
2. `tests/cube/model_compiles.test.js`, which I wrote earlier tonight, has to **inject** `process`
   into its sandbox (`if (p === 'process') return { env };`) or the conditionals do not see anything.
   My own test harness documented the defect while I was using it to test something else.

### What this subsumes

**Every** environment conditional in `cube/model/schema/` has always evaluated false:

| Conditional | Intended | Actual, always |
|---|---|---|
| `isDuckDB` | DuckDB `read_parquet` SQL | Trino SQL → `Catalog "gravix" does not exist` |
| `isMultiTenant` | glob `warehouse/*/request_metrics_minute/` | glob `warehouse/request_metrics_minute/` |
| `hasExternalStore` (added in F-036) | rollups only with a store | rollups never declared |

**F-036's fix worked for the wrong reason, and I am correcting that here.** The rollups disappeared
because `process` is unavailable in the sandbox, not because `CUBEJS_CACHE_AND_QUEUE_DRIVER=memory`
was read. The guard I wrote for it passes because it injects `process` — it tests a code path Cube
never executes. This is precisely the failure I have named eight times in this register tonight, and
this instance is mine.

**F-027 needs qualifying too.** That finding said Cube's multi-tenant glob reads
`warehouse/*/request_metrics_minute/` and the rollup was writing to the non-tenant path. The rollup
half was real and the fix was right — it now writes `warehouse/<tenant-id>/…`, which the logs confirm.
But `isMultiTenant` is false in the compiler, so **Cube is globbing the single-tenant path**, and the
two still do not meet. F-027 is half a fix: the producer is correct, the consumer was never reading
where I said it was.

### Why this is not fixed here

The model must vary by backend — `read_parquet` is DuckDB-only, `gravix.raw.*` is Trino-only — and it
cannot read the environment. The three ways out are not equivalent, and one of them costs something
this project's central claim depends on:

1. **`COMPILE_CONTEXT`.** Cube's documented mechanism for passing configuration from `cube.js` (a
   normal, unsandboxed Node module that *can* read `process.env`) into schema files. The right shape,
   and unverifiable here: v0.35's exact semantics need a running Cube, and I have now been wrong twice
   tonight about Cube internals (F-035's residual uncertainty, and F-036's fix above). A third guess
   at 03:00 is not diligence.
2. **A separate model directory per stack.** Fully verifiable without Docker, because the SQL becomes
   a literal. It duplicates the three model files — and **a duplicated semantic model is two
   definitions of one metric, which is the exact thing this project claims to get right.** Drift here
   would undermine the correctness axis, not merely annoy a maintainer.
3. **Generate the variant at build time** from the canonical model. Avoids both problems and adds a
   code-generation step to a project whose dashboards deliberately have no build step.

Option 1 is almost certainly correct and needs one person with a Docker daemon to confirm in ten
minutes. Option 2 is the safe fallback and has a real cost that should be paid deliberately, not by
an implementer at the end of a long session. Per `CLAUDE.md`, a choice that trades off a stated
project claim is not an implementation detail.

### The guard that should exist, and why it is absent

The invariant is: **nothing in `cube/model/` may change its SQL or its table reference based on
`process.env`**, because the compiler does not provide it. That is directly testable — load each
model with and without `process` in the sandbox and require identical output.

It is not added, because it fails today and a red test is not a deliverable. It should land with
whichever option above is chosen, and it is the test that would have caught this on the day the
conditional was written.

### How it was actually fixed

The three options above were written without being able to run Cube. They did not need a Docker
daemon to resolve — they needed the compiler's source, which is on npm:

```
npm pack @cubejs-backend/server-core@0.35.81
npm install @cubejs-backend/schema-compiler@0.35.81
```

`DataSchemaCompiler.compileJsFile` is fourteen lines and settles the whole question:

```js
vm.runInNewContext(file.content, {
  view, cube, context, addExport, setExport, asyncModule,
  require: (extensionName) => { … },
  COMPILE_CONTEXT: this.standalone ? this.standaloneCompileContextProxy()
                                   : this.cloneCompileContextWithGetterAlias(this.compileContext || {}),
}, { filename: file.fileName, timeout: 15000 });
```

The sandbox is an explicit, closed object. There is no `process` in it, and a fresh V8 context does
not supply one, because `process` is a Node global rather than a V8 intrinsic. That is the defect,
confirmed at the source rather than inferred from a symptom.

**Option 1 (`COMPILE_CONTEXT`) was wrong**, and would have been a bad fix even where it works.
`compileContext` is the per-request context — `{ securityContext }` — threaded from
`CompilerApi`. It carries no environment, and `extendContext` goes to the API gateway rather than to
the compiler. Putting the choice of database engine there would make a cube's SQL a function of who
is asking, which is the opposite of what the correctness axis needs.

**Options 2 and 3 were unnecessary.** The sandbox's own `require` is the way out. For a specifier
that does not resolve to another model file, Cube falls through to Node's `require`
(`allowNodeRequire` defaults to `true` in `server-core`), resolving it against
`repository.localPath()` — and Node's `require` runs the module in the ordinary Node context, where
`process` is live. No duplicated model, no code generation.

So `cube/model_flags.js` sits beside `cube.js` in `/cube/conf`, deliberately **outside**
`/cube/conf/model`: a file inside the model directory is found by `resolveModuleFile` and compiled in
the sandbox again, which would reintroduce this finding in silence. Each model now begins:

```js
const { tableSql, timestampSql } = require('../model_flags.js');
```

Verified by compiling the real models with the real `prepareCompiler`, one environment per process:

| Environment | `RequestMetricsMinute.sql` |
|---|---|
| before, either stack | `SELECT * FROM gravix.raw.request_metrics_minute` |
| DuckDB + `TENANT_DB_PATH` | `SELECT * FROM read_parquet('/cube/data/warehouse/*/request_metrics_minute/**/*.parquet', union_by_name=true)` |
| DuckDB, no tenant path | `SELECT * FROM read_parquet('/cube/data/warehouse/request_metrics_minute/**/*.parquet', union_by_name=true)` |
| Trino | `SELECT * FROM gravix.raw.request_metrics_minute` |

### The guard, and why it is not the one this entry proposed

The earlier draft proposed: *load each model with and without `process` in the sandbox and require
identical output.* **That guard is wrong and would have been worse than none.** It asserts
insensitivity to `process`, which is exactly what a model hardcoded to one engine also satisfies —
it would have passed, in perpetuity, on the broken code it was written to catch.

The requirement is not "ignores `process`". It is **"the SQL responds to the environment"**. So
`cmd/onboarding_gate/cube_model_env_test.go` compiles the models the way Cube does — a sandbox with
Cube's globals and no `process` — under two engines and two tenancy settings, and requires the SQL to
differ. `tests/cube/model_compiles.test.js` asserts the same requirement in JavaScript, replacing the
F-036 guard that injected a fake `process` and so passed on the defect.

Mutation-tested; all eight killed:

| Mutant | Result |
|---|---|
| guarded `process.env` back in a model (the original F-037) | killed |
| unguarded `process.env` in a model | killed |
| `model_flags.js` moved inside `cube/model/` (silently re-sandboxed) | killed |
| the mount dropped from one compose file | killed |
| tenant prefix dropped from the warehouse glob | killed |
| engine branch hardcoded | killed |
| a pre-aggregation reintroduced | killed |
| a Cube Store configured without revisiting the models | killed |

### Confirmed by CI

`timed-onboarding` returned its first green verdict in the history of this repository:

```
PASS: time to populated dashboard 466s (budget: 600s)
```

Twice, independently — on `497c757` (the fix) and on `82745b0` (which adds only a docs correction),
in separate runs on separate runners. `ci-summary`, the aggregate gate, is green with it.

The whole documented `docker compose up` path now works end to end: bootstrap-init seeds and exits,
ingestion reports healthy in 6s, Cube is healthy in 12s, and the dashboard holds real numbers 5
minutes later. Eleven total breaks of that path were found and fixed to get here — F-019, F-021,
F-023, F-027, F-030, F-032, F-034, F-035, F-036, F-037, F-038 — each hidden behind the one before it,
because a gate can only ever see the first unfixed link.

The one remaining red check on the PR is `check` (DCO), which is unrelated to any of this and needs
the repository owner.

---

## F-038 — the pre-aggregation gate could never have compiled, and no stack can build a rollup anyway

**Found by** fixing F-037: making `hasExternalStore` live for the first time turned a silent
mis-compile into a hard compile error.
**Owner** `semantic-modeler`
**Severity** high — latent, and it made F-036's fix unfalsifiable.
**Status** fixed; pre-aggregations are removed from the models, with their definitions preserved in
comments.

### Two independent defects, both hidden behind F-037

**First: the gate's shape was incompatible with Cube's transpiler.** F-036 wrote

```js
preAggregations: hasExternalStore ? { endpointDaily: { measures: [requestCount, …] } } : {}
```

Cube resolves a cube's member references in `CubePropContextTranspiler`, which finds the fields it
must rewrite by walking the `ObjectProperty` chain from a property up to the cube's top-level object
and matching the resulting path against `/^(preAggregations|…)\.[_a-zA-Z]\w*\.(…|measures|…)$/`. A
`ConditionalExpression` in that chain breaks the walk, so the path never matches, `measures` is never
rewritten, and `requestCount` reaches the sandbox as a bare undefined identifier:

```
schema/RequestMetricsMinute.js:141
      measures: [requestCount, errorCount],
                 ^
ReferenceError: requestCount is not defined
```

That error fails the **entire** schema compile — every cube in every file. It had never been seen
because `hasExternalStore` was always falsy (F-037), so the object behind the ternary was never
evaluated. Two defects, each concealing the other.

Qualifying the references (`RequestMetricsMinute.requestCount`) does not help — the cube's own name is
not in the sandbox either. Only removing the ternary does: with `preAggregations` as a plain object
literal all four rollups compile and register.

**Second: no stack in this repository runs a Cube Store in its default configuration.** Neither
`docker-compose.yml` nor `docker-compose.bootstrap.yml` nor anything under `deploy/` defines a
`cubestore` service. A pre-aggregation is materialised in Cube Store through an
`externalDriverFactory`, and F-036 established that Cube does not degrade gracefully without one: it
prefers the rollup and fails the query rather than reading the source.

So the gate was not merely broken, it was vestigial: in the configuration these files ship, declaring
a rollup is only a way to fail the queries it was meant to accelerate.

### Correction — "could never be built" was an overclaim, and the first guard repeated the same mistake

The paragraph above originally read *"there is no configuration this repository ships in which a
declared rollup could be built"*, and the guard written for it looked for `CUBEJS_CUBESTORE_HOST` and
nothing else. Both were wrong, and wrong in the way this register keeps recording: **a guard that
matches on a name protects the names its author knew about.** Cube's own
`OptsHandler.initializeCoreOptions` names eleven triggers:

```js
const externalDbType = opts.externalDbType
  || process.env.CUBEJS_EXT_DB_TYPE
  || ((getEnv('devMode') || definedExtDBVariables.length > 0) && 'cubestore')
  || undefined;
```

where `definedExtDBVariables` is any of `CUBEJS_EXT_DB_{URL,HOST,NAME,PORT,USER,PASS}` or
`CUBEJS_CUBESTORE_{HOST,PORT,USER,PASS}`.

**Dev mode is the one that matters, and it was missed entirely.** With `CUBEJS_DEV_MODE` truthy the
external type defaults to `cubestore`, and in the official `cubejs/cube` image Cube *starts an
embedded Cube Store on port 3030 itself* — no service, no host variable, nothing in a compose file to
grep for:

```js
if (externalDbType === 'cubestore' && this.isDevMode() && !opts.serverless) {
  const cubeStoreHandler = new cubeStorePackage.CubeStoreHandler({ … });
  console.log(`🔥 Cube Store (${version}) is assigned to 3030 port.`);
  if (isDockerImage()) { cubeStoreHandler.acquire()… }
  externalDriverFactory = () => new cubeStorePackage.CubeStoreDevDriver(cubeStoreHandler);
}
```

Both compose files set `CUBEJS_DEV_MODE=${CUBEJS_DEV_MODE:-false}`, so the default has no store and
the decision to declare no pre-aggregations stands unchanged. But a reader who exports
`CUBEJS_DEV_MODE=true` **does** get one, and telling them a rollup could never be built would have
been false.

`TestModelsDeclareNoPreAggregations` now derives the condition from all eleven triggers plus a
hardcoded `CUBEJS_DEV_MODE=true`, rather than from one variable name. Re-mutation-tested: seven
mutants, one per route, all killed — six of which the original guard would have let through.

Also worth separating, because GRVX-1006 §2 conflates them: **Redis is not an external
pre-aggregation store.** `CUBEJS_CACHE_AND_QUEUE_DRIVER` selects the queue and cache driver;
`CUBEJS_EXT_DB_TYPE` and the variables above select where rollup tables live. Turning Redis on does
not make a pre-aggregation buildable.

### Fixed

`preAggregations: {}` in all three models, with each rollup's definition preserved verbatim in a
comment beside it, together with the two reasons it is not active and what restoring it would require.
`hasExternalStore` is gone from `model_flags.js` rather than left exported and unused, because an
unused export is an invitation to rebuild the gate that could not work.

`TestModelsDeclareNoPreAggregations` asserts both halves — no rollup in any model, and no
`CUBEJS_CUBESTORE_HOST` in either compose file — so adding a Cube Store fails the test and forces the
models to be revisited deliberately rather than left to drift.

### This settles a GRVX-1006 hazard

The standing warning was right and is now a tested fact: **this stack has no pre-aggregations, so
every latency figure it produces is a cold read from Parquet.** Quoting one as pre-aggregated would
repeat F-020 and F-022.


### Ten runs, ten findings

F-019, F-021, F-023, F-027, F-030, F-032, F-034, F-035, F-036, F-037, F-038. The gate has done exactly what
it was built to do — it can only ever see the first unfixed link, and it has surfaced ten of them in
a path that `docker compose up` was documented to make work. It has also now caught one of my own
fixes passing for the wrong reason, which is the argument for running the real thing rather than
trusting a guard that tests a path production never takes.

---

## F-039 — compaction destroys the latency sketches and replaces percentiles with the mean of percentiles

**Found by** `qa-engineer` executing GRVX-1101, while writing the published column reference the
guide requires.
**Owner** `semantic-modeler`, with `perf-cost-engineer` on the retention consequences.
**Severity** high — it silently deletes data on the project's first superiority axis, and it makes a
published correctness claim false for any day old enough to have been compacted.
**Status** open. Not fixed here: `transforms/compaction/main.go` is on GRVX-1101 §4.3's do-not-touch
list, and the fix is a schema decision, not a patch.

### Two defects, one function

**First: five columns are dropped.** `pkg/recompute.MetricRow` — what the rollup writes — declares
seventeen parquet columns. `transforms/compaction/main.go:29-41` declares its own `MetricRow` with
twelve. Compaction reads with `parquet.NewGenericReader[MetricRow]` and writes with
`parquet.NewGenericWriter[MetricRow]`, both bound to the twelve-field struct
(`transforms/compaction/main.go:384,397`). parquet-go ignores file columns the target struct does not
name, so `latency_sketch`, `sketch_version`, `user_agent_family`, `extra_quantile_label` and
`extra_quantile_ms` are read as nothing, written as nothing, and gone the moment the original
partition is replaced.

`latency_sketch` is the one that hurts. Its doc comment in `pkg/recompute/recompute.go` states its
purpose exactly:

> The scalars above stay: within one bucket they are exact and cheaper to read. The sketch is what
> makes a correct percentile over many buckets possible at all.

After compaction, that is no longer possible at all. The capability Phase 8 built is deleted by a
retention job.

**Second: the merge averages percentiles.** `mergeMetricRows`
(`transforms/compaction/main.go:87-152`) combines buckets by summing counts — correct — and then:

```go
p50 = s50 / float64(len(grp))
p95 = s95 / float64(len(grp))
p99 = s99 / float64(len(grp))
```

The arithmetic mean of per-bucket p95s is not the p95 of the union, and the error is unbounded: a
quiet bucket with one slow request contributes its p95 with the same weight as a busy one. This is
the precise error the sketch was introduced to eliminate, committed by the job that deletes the
sketch.

### Why it was not caught

Nothing compares the two structs. They are independent declarations in different packages that
happen to share a name, so adding columns to `pkg/recompute.MetricRow` — which Phase 8 did — left
compaction's copy behind with no compile error and no failing test. GRVX-1101 now carries
`TestFixtureSchemaMatchesProduction`, which catches drift between the bare-Parquet fixture and
`pkg/recompute.MetricRow`; no equivalent guard exists between the rollup and compaction, and one
should.

### What is affected

- **Any percentile read over a compacted day** is the mean of means, not a percentile.
- **`docs-site/docs/bare-parquet-access.md`** documents this, under a warning, because the
  alternative was publishing a column table that is wrong for half the warehouse.
- **GRVX-1006's percentile work and the correctness axis claims** need re-reading against this: a
  claim that Gravix computes percentiles correctly is true for fresh data and false for compacted
  data, and no published claim currently makes that distinction.
- **Recomputability is the mitigation, not the fix.** The facts in `data/raw/` are untouched, so a
  compacted day can be rebuilt — until raw data passes its own thirty-day purge, after which the
  sketches are unrecoverable.

### What the fix has to decide

Whether compaction preserves `latency_sketch` and merges sketches (correct, larger files), or whether
compacted days are documented as scalar-only with percentiles dropped rather than averaged. Averaging
them is not one of the options: a wrong number is worse than an absent one, and this project's whole
argument is that it knows the difference.

---

## F-040 — the committed protobuf Go code was two fields behind its own `.proto`, and `tenant_id` was rejected on the wire

**Found by** `senior-engineer` executing GRVX-1102, whose §4.2 requires regenerating
`gen/gravix/v1/gravix.pb.go`.
**Owner** `senior-engineering-lead` (the schema contract) with `docs-engineer` (the command in
`CLAUDE.md`).
**Severity** medium — no compile error, no failing test, and a field the project's own schema
declares was rejected at the API boundary.
**Status** fixed as a side effect of GRVX-1102's mandated regeneration. The cause — the documented
command writing elsewhere — is **SD-028** and is not fixed.

### What had drifted

`proto/gravix.proto` declares `string tenant_id = 9` on `RequestFact` and `string tenant_id = 8` on
`ServiceEvent`. The committed `gen/gravix/v1/gravix.pb.go` had neither. Regenerating from the
unmodified `.proto` adds both, with their getters and the corresponding raw descriptor bytes.

Nothing in the Go code referenced `RequestFact.TenantId` or `ServiceEvent.TenantId`, which is why
`go build ./...` and the whole suite passed against generated code that did not match its source.
Tenancy is carried separately — through `topicForTenant` and the storage path — so the fields were
declared, never wired, and never missed.

### The part that was not cosmetic

`schemas.ParseRequestFact` decodes with `protojson.Unmarshal` and default options, and protojson
rejects unknown fields by default. Verified directly:

```
tenant_id: ACCEPTED (post-regeneration)
unknown field: REJECTED (protojson unmarshal error: proto: (line 1:171): unknown field "nope")
```

So before this regeneration, a client posting a `RequestFact` carrying the `tenant_id` field that
`proto/gravix.proto` publishes as part of the contract would have been rejected with
`unknown field "tenant_id"`. The schema said the field existed; the endpoint said it did not.

### Why it went unnoticed

`CLAUDE.md` names `proto/gravix.proto` the source of truth for these messages and gives a regeneration
command directly beneath it. That command writes to `gen/proto/`, which `.gitignore` hides, so anyone
following the documented procedure saw a clean `git status` and concluded the generated code was
already current (**SD-028**). No CI job regenerates protobuf and diffs it — `check-codegen` in
`.github/workflows/sdk-codegen.yml` covers only the TypeScript and Python SDK types.

### What should happen

Fix the command in `CLAUDE.md`, and add a protobuf arm to `check-codegen` that regenerates and fails
on a diff, exactly as it already does for the SDK types. A generated file that no job regenerates is
a file that will drift again; this one did so for two fields without anyone noticing.

---

## F-041 — `CHANGELOG.md` has been empty for the whole of Horizon 2, and no spec's §4 lets an implementer fix it

**Found by** `senior-engineer` executing GRVX-1105, which removes a working endpoint's behaviour.
**Owner** `sre-release-manager` (owns the changelog and semver policy) with `docs-engineer`.
**Severity** medium — it is how a user finds out about a breaking change by hitting it.
**Status** open. Not fixable inside any spec as written: `CHANGELOG.md` is outside §4.1/§4.2
everywhere, and §9's "No file outside §4.1/§4.2 modified" is a hard gate.

### What is wrong

`CHANGELOG.md` declares itself Keep a Changelog and semver:

> The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
> and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Its `## [Unreleased]` section is empty, and has been through every phase of Horizon 2 — Phase 7's
open-core split, Phase 8's correctness work, Phase 9, Phase 10, and now Phase 11. The last populated
entry is `## [1.0.0] - 2026-03-23`.

GRVX-1105 makes this concrete rather than theoretical. It **removes** the OTLP trace receiver at
`POST /v1/traces`: a deployment pointing an OpenTelemetry Collector's trace pipeline at that endpoint
starts receiving 400s on upgrade. Under semver that is a breaking change and belongs under
`### Removed` in `[Unreleased]`. There is nowhere for the implementer to write it.

The same gap swallowed new surface, not only removed surface: `POST /api/v1/remote_write` and
`POST /v1/metrics` (GRVX-1102, GRVX-1105) and the bare-Parquet guide (GRVX-1101) all shipped without
an entry.

### Why the spec corpus produces this

Each spec's §4.1/§4.2 lists exactly the files its implementer may touch, which is the constraint that
keeps product decisions out of implementation code — and it works. But no spec template asks whether
the change alters public surface, and none includes `CHANGELOG.md` when it does. The result is a
corpus that is rigorous about proving each change and silent about announcing any of them.

### What should happen

Add a `CHANGELOG.md` row to §4.2 of every spec that changes a public endpoint, CLI flag, config key,
or output format, and a Definition-of-Done checkbox for it — the same shape as the existing
`docs-engineer` delta line. A release gate that fails when `[Unreleased]` is empty on a tag would
make it self-enforcing, in the same spirit as `check-boundary`.

Until then, the breaking change in GRVX-1105 is recorded here and in that spec's §11.5, which is the
wrong place for a user to have to look.

---

## F-042 — the Spec Readiness Gate checks that paths are well-formed, not that they are right, and Phase 11 shows the cost

**Found by** `senior-engineer` and `qa-engineer` executing GRVX-1101, 1102, 1105 and 1107 in
sequence, and reading GRVX-1108.
**Owner** `senior-engineering-lead` (runs the gate) with `orchestrator` (could enforce it).
**Severity** medium — no defect reached production, but every one cost an implementer a detour, and
two of them would have produced a red build if followed literally.
**Status** open.

### The pattern

Every Phase 11 spec header records `Readiness Gate | 12/12 — PASS`. Four of the five I engaged with
carried a §2 or §4 defect anyway:

| Spec | Defect | Registered |
|---|---|---|
| GRVX-1101 | §6 mandates a published query whose glob matches no file a real warehouse writes; DuckDB fails it outright | SD-026 |
| GRVX-1101 | §2 states "compaction does not change column names or types"; it drops five | SD-025 |
| GRVX-1102 | §2/§4.2's `protoc` command writes to a gitignored path and does not regenerate the file §4.2 names | SD-028 |
| GRVX-1102 | §5.4 requires `tenant_id`; §6 step 7 says it is empty in legacy mode and then says to validate | SD-027 |
| GRVX-1107 | §2 names `services/gateway/enterprise.go` as holding the scheduled-export code; it holds none. §4 omits both files the spec cannot be finished without | SD-029 |
| GRVX-1108 | §4 omits `cmd/cli/cmd_recompute.go` and `cmd/cli/cmd_explain.go`, which AC-6 and AC-7 require changing (read, not yet executed) | — |

GRVX-1105 was executable as written; its §2 line numbers had drifted and §6 step 1 mispredicted an
unused import, neither blocking.

### Why the gate passes them

Check 1 reads:

> Every file to create or modify is named by **exact repo-relative path** — fails if any path is
> described rather than given

It tests the *form* of the paths, not their *truth* or their *coverage*.
`services/gateway/enterprise.go` is an exact repo-relative path; it is simply the wrong one, and the
gate has no way to notice. Nor does any check ask whether §4's list covers every file §6's steps and
§7's criteria require touching — which is how GRVX-1107 and GRVX-1108 both ended up mandating
behaviour in files their own §4 forbids.

Checks 2, 3, 6 and 11 are about the spec's prose being specific, and Phase 11's specs are specific.
Being specific and being correct are different properties, and only the first is gated.

### Two costs beyond the detour

**A red build.** GRVX-1107 §4 omits `cmd/cli/main.go`, where subcommands are dispatched from a
hard-coded switch. Following §4 literally leaves `cmd/cli/cmd_export.go` unreachable, and the `lint`
job runs `staticcheck`, which fails on U1000 dead code. A spec that passes 12/12 cannot currently be
executed to a green build.

**A published falsehood.** GRVX-1101 §6 requires the guide to publish a query verified in CI and
broken for every reader who runs it on their own data. The gate's check 5 — "Verification is
copy-pasteable shell" — was satisfied: the commands run. They run against a fixture.

### What would close it

Two mechanical checks, both scriptable in the spirit of `check-boundary`:

1. **Every repo-relative path a spec names in §2 or §4.2 exists in the repository** (§4.1 paths are
   exempt: they are being created). A path that does not resolve is a spec bug, caught before
   dispatch instead of by an implementer an hour in.
2. **Every file named in §6's steps or §7's criteria appears in §4.1 or §4.2.** This is the check
   that would have caught GRVX-1107 and GRVX-1108, and it needs no judgement.

Neither replaces check 12. Both convert "the Lead read it carefully" into something that fails a
build, which is the difference this project already relies on everywhere else.
