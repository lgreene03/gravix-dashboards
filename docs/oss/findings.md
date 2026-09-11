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
