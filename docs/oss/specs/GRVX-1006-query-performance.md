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

Bring warm dashboard query p95 to **≤400 ms** through pre-aggregation and cache warming, and
publish the **cold** p95 alongside it rather than only the flattering figure.

## 2. Context the implementer needs

- `cube/model/schema/RequestMetricsMinute.js` reads Parquet via DuckDB `read_parquet(...)` or Trino, switching on `CUBEJS_DB_TYPE`, and single/multi-tenant on `TENANT_DB_PATH`. All four combinations must keep working.
- `cube/cube.js` sets `contextToAppId`/`contextToOrchestratorId` for per-tenant pre-aggregation namespaces (Horizon 1 Phase 5.1).
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
| `cube/model/preaggregations.js` | Pre-aggregation definitions |
| `services/gateway/cache_warm.go` | Cache warming on a schedule |
| `services/gateway/cache_warm_test.go` | Tests |
| `bench/query/query.go` | Query latency driver, cold and warm |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cube/model/schema/RequestMetricsMinute.js` | Reference the pre-aggregations. Change no measure's semantics. |
| `services/gateway/main.go` | Start the cache warmer. Change no existing route. |
| `scripts/perf_baseline.json` | Record cold and warm p95 |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `transforms/**` | Write path unchanged |
| `services/gateway/percentile_handler.go` | GRVX-808 owns its budget |
| `deploy/gravix/templates/redis.yaml` | Redis stays optional |

## 5. Interface contract

### 5.1 Pre-aggregation set

Define exactly these rollups, at the grains the dashboard actually requests:

| Name | Grain | Dimensions | Measures | Refresh |
|---|---|---|---|---|
| `serviceHourly` | hour | `service` | `requestCount`, `errorCount` | every 5 min |
| `serviceDaily` | day | `service` | `requestCount`, `errorCount` | every 30 min |
| `pathHourly` | hour | `service`, `pathTemplate` | `requestCount`, `errorCount` | every 5 min |
| `methodDaily` | day | `service`, `method` | `requestCount`, `errorCount` | every 30 min |

**No percentile appears in any pre-aggregation.** Percentiles are non-aggregatable scalars per the
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
    Interval    time.Duration // default 4m, under the 5m pre-aggregation refresh
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
| `query_p95_ms` | Warm cache, pre-aggregation hit |
| `query_cold_p95_ms` | Cold cache, first request after start |
| `query_p95_no_preagg_ms` | Warm cache, a query no pre-aggregation covers |
| `query_p95_percentile_endpoint_ms` | A windowed percentile through sketch merge |

Publishing only the first would be true and misleading. The fourth is the one a user hits when they
ask the question Gravix is actually best at answering.

## 6. Behaviour

1. Measure all four latencies today; record the baseline.
2. Define the §5.1 pre-aggregations; confirm none covers a percentile.
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
| AC-1 | Warm p95 ≤400 ms without Redis | `TestWarmQueryP95WithoutRedis` |
| AC-2 | Cold p95 measured and published | `TestColdQueryP95Published` |
| AC-3 | No pre-aggregation covers a percentile | `TestNoPercentileInPreAggregations` |
| AC-4 | An uncovered query's p95 is measured and published | `TestUncoveredQueryLatencyPublished` |
| AC-5 | The percentile endpoint's p95 is measured and published | `TestPercentileEndpointLatencyPublished` |
| AC-6 | Warming does not increase user query p95 | `TestWarmingDoesNotStarveUsers` |
| AC-7 | A cycle over `MaxDuration` is abandoned, not queued | `TestWarmCycleAbandonedOnTimeout` |
| AC-8 | All four Cube configurations resolve | `TestAllFourCubeConfigsAfterPreAgg` |
| AC-9 | No measure's semantics changed | `TestMeasureSemanticsUnchanged` |
| AC-10 | Data freshness is unchanged | `TestFreshnessUnaffected` |

## 8. Verification

```bash
# 1. The target, on the stack that has no Redis
./bench/run.sh --scale standard && jq '{warm:.query_p95_ms, cold:.query_cold_p95_ms}' bench/results/*.json | tail -5
# expect: warm <= 400

# 2. Percentiles stay out of pre-aggregations
go test ./services/gateway/... -run TestNoPercentileInPreAggregations -v
grep -c "p50_latency\|p95_latency\|p99_latency" cube/model/preaggregations.js || true
# expect: PASS, and 0

# 3. Warming is polite
go test ./services/gateway/... -run 'TestWarmingDoesNotStarveUsers|TestWarmCycleAbandonedOnTimeout' -v
# expect: PASS

# 4. All four configurations
go test ./services/gateway/... -run TestAllFourCubeConfigsAfterPreAgg -v
# expect: PASS

# 5. Semantics and freshness unchanged
go test ./services/gateway/... -run 'TestMeasureSemanticsUnchanged|TestFreshnessUnaffected' -v
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
| Warming measurably slowing user queries | Reduce `Interval` or `MaxDuration` and re-measure. A warmer that slows the thing it warms is a regression. |
