# SPEC GRVX-1006: Dashboard query p95 ≤400 ms warm, with an honest cold number

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1006 | **Phase** | 10 | **Goal** | G4.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. A slow free dashboard that becomes fast when you pay is the pattern §7.4 forbids. |
| **Implementer role** | `senior-engineer`, measured by `perf-cost-engineer` |
| **Depends on** | GRVX-1001, GRVX-808 |
| **Blocks** | GRVX-1007 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Bring warm dashboard query p95 to **≤400 ms** through Cube's in-memory result cache and cache
warming, with **no pre-aggregations**, and publish the **cold** p95 alongside it, per date range,
rather than only the flattering figure.

> **Amended 2026-10-01 (SD-024, DD-008).** This spec first required four Cube pre-aggregations. No
> shipped stack can build one: a rollup lives in Cube Store, not Redis, and Cube fails a query whose
> rollup it cannot build. The owner's delegation chose to drop them rather than add a container
> (contradicting F-035 and GRVX-1004's cost figure) or make `CUBEJS_DEV_MODE` load-bearing in
> production. "Warm" now means a result-cache hit; the cold path is made cheaper by date-range
> partition pruning; and the measurements behind the choice are in §11.6.

## 2. Context the implementer needs

- `cube/model/schema/RequestMetricsMinute.js` reads Parquet via DuckDB `read_parquet(...)` or Trino, switching on `CUBEJS_DB_TYPE`, and single/multi-tenant on `TENANT_DB_PATH`. All four combinations must keep working.
- `cube/cube.js` sets `contextToAppId`/`contextToOrchestratorId` for per-tenant cache namespaces (Horizon 1 Phase 5.1).
- `cube/model_flags.js` holds every engine-specific SQL fragment, because Cube evaluates model files in a sandbox with no `process` (F-037). Pruning and the refresh key live there.
- `bucket_start` is text in Parquet and in Trino, `YYYY-MM-DD HH:MM:SS` UTC. Cube's DuckDB dialect compares a time dimension as `timestamptz`, which costs about four times a plain timestamp per row; that, not Parquet I/O, dominates the cold path (§11.6).
- `deploy/gravix/templates/redis.yaml` deploys Redis; `CUBEJS_CACHE_AND_QUEUE_DRIVER=redis` is set when enabled. Redis is optional and the target must be met without it, since the bootstrap stack has none.
- `GET /api/v1/percentile` (GRVX-808) merges sketches in Go. Its own budget is p95 ≤400 ms for a 1,440-sketch window, and it is on the dashboard's critical path.
- `bench/` measures `QueryP95Ms` and `QueryColdP95Ms` separately (GRVX-1001 §5.4 rule 2).
- `docs/04-non-goals.md` §4: 5–15 minute visibility latency is expected. Query speed is unrelated to data freshness and the two must not be conflated in any published claim.

## 3. Non-goals for this spec

- Do NOT require Redis to hit the target. The bootstrap stack has none, and a target only reachable with an optional component is not the free product's target.
- Do NOT pre-aggregate away a dimension the dashboard offers. A fast query returning a coarser answer than requested is a correctness defect.
- Do NOT cache a percentile computed by any means other than sketch merge. GRVX-808 settled that.
- Do NOT publish only the warm number.
- Do NOT change data freshness. Non-goal §4 is unaffected by this spec.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `services/gateway/cache_warm.go` | Cache warming on a schedule |
| `services/gateway/cache_warm_test.go` | Tests |
| `bench/query/query.go` | Query latency driver, cold and warm |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cube/model/schema/RequestMetricsMinute.js` | Declare the refresh key and prune by the query's date range. Change no measure's semantics. |
| `cube/model_flags.js` | `dayRangeSql`, `refreshKeyFor`, and a `timestampSql` that casts on both engines (F-053) |
| `services/gateway/main.go` | Start the cache warmer. Change no existing route. |
| `scripts/perf_baseline.json` | Record cold and warm p95 |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `transforms/**` | Write path unchanged |
| `services/gateway/percentile_handler.go` | GRVX-808 owns its budget |
| `deploy/gravix/templates/redis.yaml` | Redis stays optional |

## 5. Interface contract

### 5.1 Result cache and partition pruning

No pre-aggregation is declared. Two model changes carry the target instead:

| Change | Where | Effect |
|---|---|---|
| Refresh key read from the Parquet footers (file count and compressed size) | `refreshKeyFor` in `cube/model_flags.js`, DuckDB only | A repeat query is a cache hit until a rollup writes; new data is visible seconds after it lands, not after a fixed interval |
| The query's date range pushed into the Parquet read as an `event_day` predicate | `dayRangeSql`, inside `FILTER_PARAMS` on `RequestMetricsMinute` | A one-day query scans one day's file instead of every day retained |

Trino keeps Cube's default refresh key and gets a no-op predicate: its table declares `event_day` as
an ordinary column, so pruning buys nothing there, and nothing was changed without a Trino stack to
measure it on.

A time-based key such as `every: 5 minute` is ruled out: it holds an empty first answer for up to
five minutes after data lands, and G3.1's onboarding budget has 134 s of headroom.

**If a pre-aggregation is ever added, no percentile appears in it.** Percentiles are non-aggregatable scalars per the
metric contracts (GRVX-803) and are served by sketch merge (GRVX-808). Pre-aggregating them would
reintroduce exactly the defect Phase 8 removed.

### 5.2 Cache warming

```go
// Package main, services/gateway.

// CacheWarmer periodically issues the queries the dashboard's default view makes,
// so a user's first request finds a warm cache instead of paying for the cold path.
type CacheWarmer struct{ /* unexported */ }

// WarmerConfig bounds the warming.
type WarmerConfig struct {
    Interval    time.Duration // default 4m; the cache is invalidated by each rollup write
    Queries     []WarmQuery   // the default dashboard view's queries
    MaxDuration time.Duration // abandon a cycle that exceeds this; default 60s
    Enabled     bool          // default true; false on the bootstrap stack if configured
}

// Start begins warming until ctx ends.
func (w *CacheWarmer) Start(ctx context.Context) error

// Stats reports the last cycle.
func (w *CacheWarmer) Stats() WarmStats
```

Warming must never delay a user query: it runs at a lower priority and abandons its cycle at
`MaxDuration` rather than competing for the connection pool.

### 5.3 Published latency, both numbers

`scripts/perf_baseline.json` and the benchmark page carry all four:

| Field | Meaning |
|---|---|
| `query_p95_ms` | Warm: a result-cache hit for a query already asked since the last rollup write |
| `query_cold_p95_ms` | Cold cache, first request after start |
| `query_p95_uncached_ms` | A cache miss — a query nobody has asked since the last write — reported per date range (1 day, 7 days, all retained) |
| `query_p95_percentile_endpoint_ms` | A windowed percentile through sketch merge |

Publishing only the first would be true and misleading. The fourth is the one a user hits when they
ask the question Gravix is actually best at answering.

## 6. Behaviour

1. Measure all four latencies today; record the baseline.
2. Apply §5.1's refresh key and pruning; confirm no pre-aggregation is declared.
3. Implement the warmer per §5.2.
4. Re-measure all four on the bootstrap stack **without** Redis, and on the full stack **with** it. Report both.
5. Verify all four Cube configurations still resolve.
6. If ≤400 ms is unreachable without Redis, report the no-Redis number as the headline and the with-Redis number beside it — never the reverse.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A warming cycle exceeds `MaxDuration` | abandon, log once, continue next cycle | `cache warm cycle abandoned after <d>` |
| A pre-aggregation would cover a percentile | build-time failure | `preaggregation "<name>" includes a percentile measure; percentiles are served by sketch merge` |
| Warming starves user queries | fail the test | `warming increased user query p95 by <n>ms` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Warm (result-cache hit) p95 ≤400 ms without Redis | `TestWarmQueryP95WithoutRedis` |
| AC-2 | Cold p95 measured and published | `TestColdQueryP95Published` |
| AC-3 | No pre-aggregation covers a percentile | `TestNoPercentileInPreAggregations` |
| AC-4 | A cache miss's p95 is measured and published per date range | `TestUncoveredQueryLatencyPublished` |
| AC-5 | The percentile endpoint's p95 is measured and published | `TestPercentileEndpointLatencyPublished` |
| AC-6 | Warming does not increase user query p95 | `TestWarmingDoesNotStarveUsers` |
| AC-7 | A cycle over `MaxDuration` is abandoned, not queued | `TestWarmCycleAbandonedOnTimeout` |
| AC-8 | All four Cube configurations resolve | `TestModelSQLRespondsToTheEngine`, `TestModelSQLRespondsToTenancy`, `the model loads in all four configurations` |
| AC-9 | No measure's semantics changed | `TestCubeDateRangePruningPreservesResults` |
| AC-10 | Data freshness is unchanged | `TestCubeRefreshKeyMovesOnlyWithTheData` |

## 8. Verification

```bash
# 1. The target, on the stack that has no Redis
./bench/run.sh --scale standard && jq '{warm:.query_p95_ms, cold:.query_cold_p95_ms}' bench/results/*.json | tail -5
# expect: warm <= 400

# 2. No pre-aggregation is declared, and percentiles could not enter one
go test ./cmd/onboarding_gate/ -run TestModelsDeclareNoPreAggregations -v
make test-js
# expect: PASS

# 3. Warming is polite
go test ./services/gateway/... -run 'TestWarmingDoesNotStarveUsers|TestWarmCycleAbandonedOnTimeout' -v
# expect: PASS

# 4. All four configurations
go test ./cmd/onboarding_gate/ -run 'TestModelSQLRespondsTo|TestDuckDBModelPrunes|TestTimeColumnsAreCast' -v
# expect: PASS

# 5. Semantics and freshness unchanged
go test -tags=slow ./tests/e2e/ -run 'TestCubeDateRangePruningPreservesResults|TestCubeRefreshKeyMovesOnlyWithTheData' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] All four latency figures recorded, with and without Redis
- [ ] No percentile in any pre-aggregation
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| 400 ms unreachable without Redis | Publish the no-Redis number as the headline and report it. Never make the optional component's number the headline; the bootstrap stack is the free product. |
| A pre-aggregation that would need a percentile to hit the target | Return `SPEC DEFECT: §5.1`. Speed does not buy an exception to GRVX-808. |
| A cold figure the cache cannot hide, such as a week-long range on the half-CPU stack | Publish it beside the warm figure. Making it faster by storing `bucket_start` as a timestamp changes a published column type and every content digest, so it is a design-tier RFC, not this spec. |
| Warming measurably slowing user queries | Reduce `Interval` or `MaxDuration` and re-measure. A warmer that slows the thing it warms is a regression. |

---

## 11. Implementation report (partial)

**AC-3 is complete. AC-1, AC-2, AC-4, AC-5 and AC-8 are blocked on a running stack, and the stack
does not yet boot.** AC-6, AC-7, AC-9 and AC-10 are not attempted, for the reason in §11.3.

### 11.1 AC-3, and the gap it closed

Checking whether the existing percentile protections held before adding another found that they do
not, in one place. `'no measure aggregates a percentile column with max'` matches percentile columns
with `/p\d\d_latency_ms/` — exactly two digits, that spelling only. `pkg/recompute`'s `MetricRow`
also carries `extra_quantile_ms`, written by GRVX-806 for a retroactively added percentile, and that
does not match.

Measured: a `max` over `extra_quantile_ms` in the `endpointDaily` pre-aggregation, unannotated —
**all fourteen tests pass**. The max of 1,440 one-minute percentiles, stored as the day's percentile,
cached. Recorded as **F-024**.

`TestNoPercentileInPreAggregations` now checks the invariant rather than the spelling: a
pre-aggregation coarser than a minute may contain only measures that survive a roll-up — `sum` and
the count family. `min`, `max` and `avg` over a per-bucket scalar do not, whatever the column is
named. A second test, `TestNoPercentileColumnIsRolledUp`, covers the same rule by column pattern,
because the type check alone would miss a percentile declared `sum`.

Mutation-tested: `max`, `min` and `avg` over `extra_quantile_ms`, and `avg` over `p999_latency_ms`,
all now fail with the pre-aggregation, measure and aggregation named.

```
$ make test-js
# pass 108   # fail 0
```

### 11.2 §6 step 1 — the four latencies are not measured

§5.3 requires four figures, and none has been measured:

| Field | Status |
|---|---|
| `query_p95_ms` | not measured through Cube |
| `query_cold_p95_ms` | not measured through Cube |
| `query_p95_no_preagg_ms` | not measured |
| `query_p95_percentile_endpoint_ms` | not measured |

`bench/` reports `QueryP95Ms` ≈ 155 ms and `QueryColdP95Ms` ≈ 145 ms, and **those are not these
numbers**: GRVX-1001 §11.5 states that its query stage reads Parquet rows in Go, with no Cube, no
semantic layer and no HTTP. It is a floor for the data access, not a dashboard query latency. Quoting
it as if it were `query_p95_ms` would be the same class of error as F-020 and F-022.

Measuring the real four needs Cube running, which needs the bootstrap stack. No Docker daemon exists
in the implementation environment, and the stack itself has been found broken five separate ways this
phase (**F-015**, **F-016**, **F-019**, **F-021**, **F-023**), the last fix still unverified.

### 11.3 Why the pre-aggregation set was not restructured

§4.1 asks for a new `cube/model/preaggregations.js` and §4.2 for `RequestMetricsMinute.js` to
reference it. §5.1 specifies four rollups; the model currently defines two (`metricsHourly`,
`endpointDaily`), one of which matches `serviceHourly` and one of which is not in §5.1's set at all.

Restructuring them was not done, because AC-8 requires all four Cube configurations
(DuckDB/Trino × single/multi-tenant) to still resolve afterwards, and that cannot be verified without
running Cube. Rewriting the dashboard's entire query path against a schema nobody can execute, on the
strength of a static test that only parses the definitions, is how a query returns a coarser answer
than it was asked for — which §3 calls a correctness defect. The same discipline as GRVX-1003's
`SPEC DEFECT`: the work is real and it needs the thing that is missing.

**Sequencing:** `timed-onboarding` goes green → the four latencies are measurable → §5.1's rollups
land with AC-8 verifying them → the warmer (§5.2) is built against measured numbers rather than
guessed ones.

### 11.4 Definition of done

- [x] AC-3 — `TestNoPercentileInPreAggregations`, plus a second independent guard, mutation-tested
- [ ] AC-1, AC-2, AC-4, AC-5, AC-8 — blocked: no running Cube
- [ ] AC-6, AC-7, AC-9, AC-10 — not attempted; the warmer follows the measurements
- [ ] All four latency figures, with and without Redis — blocked
- [x] No percentile in any pre-aggregation — now enforced by behaviour, not by name
- [x] Zero new skipped tests

### 11.5 `SPEC DEFECT: §5.1` — the rollups cannot be built on the stack this spec targets

Filed as **SD-024**. §5.1 mandates four pre-aggregations and AC-1 is premised on them, but §2 and §3
name **Redis** as the optional component the target must be met without. Redis is not where a rollup
lives: `CUBEJS_CACHE_AND_QUEUE_DRIVER` picks the queue and cache driver, while a rollup table is
materialised through an `externalDriverFactory` selected by `CUBEJS_EXT_DB_TYPE` and the
`CUBEJS_EXT_DB_*` / `CUBEJS_CUBESTORE_*` variables. None of those is set in any compose file or under
`deploy/`, and no stack defines a `cubestore` service.

F-036 established that Cube does not fall back to the source when it cannot build a matching rollup —
it fails the query. So adding §5.1's rollups to the bootstrap stack would not miss the ≤400 ms
target; it would break the dashboard.

The three resolutions each cost something already decided elsewhere (another container contradicts
F-035 and GRVX-1004's cost figure; dev mode makes a development flag load-bearing for production
performance; meeting the target without rollups makes §5.1's table wrong rather than unexecutable).
§10's escalation table anticipated a shortfall against Redis and has no row for this. See SD-024.

**Unchanged by this:** AC-3 stays complete; §5.3's four figures remain the right shape; and every
latency this stack produces is still a cold read from Parquet, so publishing one as pre-aggregated
would repeat F-020 and F-022.

**Still blocked besides:** §6 steps 1 and 4 need a running Cube, and the implementation environment
has no Docker daemon. `timed-onboarding` going green cleared the stack blocker §11.3 named, not this
one.


### 11.6 SD-024 resolved: measured, decided, and what is still open

A Docker daemon became available on 2026-10-01, so the measurements §11.2 could not take were taken.

**Method.** The warehouse is `./bench/run.sh --scale standard`'s own output: seven days, 1,924,877
metric rows, 35 MB of Parquet. Cube `v0.35` ran with the bootstrap stack's exact settings —
`CUBEJS_DB_TYPE=duckdb`, the `memory` cache driver, no Redis, no Cube Store, `--cpus 0.5`,
`--memory 512m` — against the repository's own `cube/` directory. One deviation, stated: Cube's DuckDB
driver installs the `httpfs` extension from `extensions.duckdb.org` at start-up, which this
environment's network policy blocks, so the lab image comments those two lines out. Local Parquet
never uses `httpfs`. The queries are the dashboard's default view: the summary cards, the endpoints
table, the top-errors table and the hourly time series. A cache miss is forced by adding an
always-true filter with a fresh value each time; `renewQuery` did not force one.

**Warm: a result-cache hit (AC-1).**

| Query | 1-day range p50 | p95 | No range p50 | p95 |
|---|---|---|---|---|
| summary | 9 ms | 51 ms | 6 ms | 55 ms |
| endpoints | 10 ms | 58 ms | 7 ms | 53 ms |
| top errors | 8 ms | 100 ms | 7 ms | 66 ms |
| hourly series | 10 ms | 71 ms | 19 ms | 75 ms |

**Cold: a cache miss (AC-4), half a CPU.**

| Query | 1 day p95 | 7 days p95 | No range p95 | Before pruning, no range |
|---|---|---|---|---|
| summary | 193 ms | 868 ms | 317 ms | 305 ms |
| endpoints | 299 ms | 1,640 ms | 699 ms | 722 ms |
| top errors | 294 ms | 1,297 ms | 801 ms | 715 ms |
| hourly series | 593 ms | 3,670 ms | 3,309 ms | 3,612 ms |

The first query after start (AC-2) took 1,396 ms, most of it DuckDB initialisation. New data was
visible **6 s** after a partition file appeared (AC-10).

**What the numbers say.** The warm target is met with room to spare. The cold path is not, beyond a
single day. Profiling the SQL Cube generates shows why: Cube's DuckDB dialect compares and truncates
a time dimension as `timestamptz`, and on a text column that costs about four times a plain
timestamp per row — 2.0 s against 0.5 s for the week's hourly series in the DuckDB CLI at half a CPU.
Parquet I/O is not the bottleneck. A seven-day range is slower than no range because the range adds
a per-row `timestamptz` comparison on top of the scan; pruning cannot help when every day is in range.

**Found on the way: F-053.** On `main`, every date-ranged query on the DuckDB stack failed — "Cannot
compare values of type VARCHAR and type TIMESTAMP WITH TIME ZONE" — because `timestampSql` returned
the bare text column for DuckDB. The dashboard turns a 400 into an empty chart, so choosing a date
showed no data. Fixed here; `TestTimeColumnsAreCastOnBothEngines` guards it.

**Not done, and why.**

| Item | State |
|---|---|
| AC-1 warm ≤400 ms | **Met in the lab** (table above); `TestWarmQueryP95WithoutRedis` and the `bench/query` driver that would make it a CI number are not written |
| AC-2, AC-4 published | Measured here; not yet in `scripts/perf_baseline.json` or the benchmark page |
| AC-5 percentile endpoint | Not measured |
| AC-6, AC-7 warmer | Not built. `cache_warm.go` needs the gateway to mint a Cube JWT, which it can |
| AC-8, AC-9, AC-10 | **Done** — the named tests above, each mutation-tested |
| Cold path beyond one day | Needs `bucket_start` stored as a timestamp. That changes a published column type and every content digest: design tier, not decided here |
