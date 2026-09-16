# SPEC GRVX-808: Fix the Cube model to merge sketches instead of taking MAX of percentiles

| Field | Value |
|---|---|
| **Spec ID** | GRVX-808 |
| **Phase** | 8 | **Goal** | G2.8, G2.7 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. This spec replaces a wrong number with a right one. |
| **Implementer role** | `senior-engineer`, model owned by `semantic-modeler` |
| **Depends on** | GRVX-804 |
| **Blocks** | GRVX-809, GRVX-811, GRVX-812 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

The dashboard currently shows a multi-minute p95 computed as `MAX` of the per-minute p95s. This spec
replaces that with a merge of the stored t-digest sketches, and marks any measure that cannot be
correctly aggregated as non-aggregatable rather than silently approximating it.

## 2. Context the implementer needs

- `cube/model/schema/RequestMetricsMinute.js:44-63` — `p95Latency`, `p50Latency`, `p99Latency` are all
  `type: max` over the scalar columns. The file's own comment admits this is wrong and names the fix:
  *"Ideally we re-aggregate T-Digests."*
- `cube/model/schema/RequestMetricsMinute.js:37-42` — `errorRate` is `sum(error_count) / NULLIF(sum(request_count), 0)`, which **is** correct across buckets. Do not change it.
- `cube/model/schema/RequestMetricsMinute.js:1-8` — the model switches between DuckDB
  (`read_parquet(...)`) and Trino (`gravix.raw.request_metrics_minute`) on `CUBEJS_DB_TYPE`, and between
  single- and multi-tenant on `TENANT_DB_PATH`. All four combinations must keep working.
- `pkg/sketch` (GRVX-804) stores `latency_sketch BLOB` and `sketch_version` per row, with a documented
  merge that is associative and commutative.
- `contracts/request_metrics_minute.v2.yaml` (GRVX-804) sets `mergeability: sketch_merge` and a
  `merge_note` forbidding max/mean of the scalars.
- Neither DuckDB nor Trino has a native t-digest merge over an opaque BLOB written by our Go code.
  The merge must therefore happen somewhere that can run our code.

## 3. Non-goals for this spec

- Do NOT change `errorRate`, `requestCount` or `errorCount`. They are already correct.
- Do NOT invent a SQL-level t-digest implementation. Non-goal §6 forbids a custom query language, and
  a hand-rolled SQL sketch merge would be both slow and unverifiable.
- Do NOT remove the scalar percentile columns. Within one bucket they are exact and cheaper.
- Do NOT keep `type: max` on any percentile, even as a fallback measure with a different name. A wrong
  number that is still reachable will be read by someone.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/gatewaycore/percentile_handler.go` | HTTP endpoint merging sketches for a window |
| `pkg/gatewaycore/percentile_handler_test.go` | Tests |
| `tests/cube/RequestMetricsMinute.test.js` | Model assertions |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cube/model/schema/RequestMetricsMinute.js` | Replace the three `type: max` percentile measures per §5.2; add `latencySketch` as a dimension; add the `bucketPercentileP95` single-bucket measure |
| `dashboards/lib/cube-client.js` | Route window-percentile requests to the new endpoint |
| `services/gateway/main.go` | Register the new route. Change no existing route. |
| `docs/openapi.yaml` | Document the new endpoint |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/sketch/**` | Merge semantics are settled by GRVX-804 |
| `transforms/**` | Write path unchanged |
| `contracts/**` | Contracts already describe the correct behaviour |

## 5. Interface contract

### 5.1 The architectural decision, stated so it is not relitigated

The sketch merge happens in **Go, in the gateway**, not in SQL. Cube and the SQL engines serve the
sketch bytes; the gateway merges them with the same `pkg/sketch` code that wrote them. The reasons:

1. The merge must use the identical implementation that produced the sketches, or the error bound
   proven in GRVX-804 does not transfer.
2. Implementing t-digest merging in DuckDB SQL and again in Trino SQL means two more implementations
   to prove correct, in two dialects.
3. Non-goal §6 forbids inventing a query language; a SQL macro library for sketch arithmetic drifts
   toward exactly that.

The cost is one extra hop for windowed percentiles. That is accepted, and `perf-cost-engineer` owns
the p95 ≤400 ms budget (G4.5) which this must not breach.

### 5.2 Cube model changes

Remove `p50Latency`, `p95Latency`, `p99Latency` entirely. Replace with:

```javascript
    // Exact within a single one-minute bucket. Cube cannot merge sketches, so
    // these measures are valid ONLY when the query granularity is one minute.
    // For any wider window use GET /api/v1/percentile, which merges the
    // latency_sketch column in Go. See contracts/request_metrics_minute.v2.yaml.
    bucketP50LatencyMs: {
      sql: `p50_latency_ms`,
      type: `min`,
      title: `P50 Latency (single bucket only)`,
      meta: { aggregatable: false, correctOnlyAtGranularity: `minute` }
    },
```
and the same shape for `bucketP95LatencyMs` and `bucketP99LatencyMs`.

`type: min` is chosen over `max` deliberately: if someone queries these across a wider window despite
the warning, `min` produces an obviously-too-low number that gets noticed and reported, whereas `max`
produces a plausible-looking number that gets believed. Both are wrong; only one is *visibly* wrong.
Record this reasoning in a code comment so it is not "corrected" later.

Add the sketch as a dimension so the endpoint can query it through Cube:

```javascript
    latencySketch: {
      sql: `latency_sketch`,
      type: `string`,
      title: `Latency Sketch (internal)`,
      shown: false
    },
```

### 5.3 `GET /api/v1/percentile`

| Parameter | Type | Required | Constraint |
|---|---|---|---|
| `metric` | string | yes | must be `request_metrics_minute` |
| `quantile` | float | yes | `0 < q < 1` |
| `from` | RFC3339 | yes | — |
| `to` | RFC3339 | yes | after `from`; window ≤31 days |
| `service` | string | no | dimension filter |
| `method` | string | no | dimension filter |
| `path_template` | string | no | dimension filter |
| `granularity` | string | no | one of `minute`, `hour`, `day`, `all`; default `all` |

Response `200`:

```json
{
  "metric": "request_metrics_minute",
  "quantile": 0.95,
  "granularity": "hour",
  "exactness": "sketch",
  "error_bound": "relative error <= 1% for q in [0.5, 0.99]",
  "buckets": [
    {"bucket_start": "2026-09-09T14:00:00Z", "value": 142.3, "observations": 72240, "sketches_merged": 60}
  ]
}
```

Status codes: `200` success; `400` invalid parameter, with the offending name; `401` unauthenticated;
`404` unknown metric; `422` a partition in the window predates sketch storage, naming the days;
`429` rate limited; `500` merge failure.

The `422` matters: it is the honest answer for pre-`v2` data rather than quietly falling back to the
wrong `max` computation.

### 5.4 Dashboard change

`dashboards/lib/cube-client.js` sends any percentile request whose granularity is coarser than one
minute to `/api/v1/percentile`. Minute-granularity requests may still use the Cube measures. The
dashboard must never display a percentile derived from `min` or `max` across buckets.

## 6. Behaviour

1. Read `cube/model/schema/RequestMetricsMinute.js` in full; record every measure and dimension.
2. Remove the three `type: max` percentile measures. Add the three `bucket*` measures with the
   `meta.aggregatable: false` annotation and the reasoning comment from §5.2.
3. Add the hidden `latencySketch` dimension.
4. Implement `GET /api/v1/percentile`: query Cube for the sketch column across the window, group by
   the requested granularity, `sketch.MergeAll` each group, and `Quantile` the result.
5. Return `422` for any window containing a partition whose `sketch_version` is empty.
6. Update the dashboard client per §5.4.
7. Verify all four Cube configurations still work: DuckDB single-tenant, DuckDB multi-tenant, Trino
   single-tenant, Trino multi-tenant.
8. Measure the endpoint's p95 for a 24-hour, 1,440-sketch window and record it. If it exceeds 400 ms,
   report it to `perf-cost-engineer` rather than shipping a budget breach silently.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `quantile` outside (0,1) | 400 | `invalid parameter "quantile": must be strictly between 0 and 1` |
| Window >31 days | 400 | `invalid parameter "to": window must not exceed 31 days` |
| Unknown metric | 404 | `unknown metric "<name>"` |
| A partition predates sketches | 422, list days | `partitions <days> predate sketch storage; run gravix recompute for those days` |
| Sketch version mismatch | 500 | `sketch version mismatch: file <a>, server <b>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | No Cube measure uses `type: max` over a percentile column | `TestNoMaxOverPercentiles` |
| AC-2 | The three `bucket*` measures carry `meta.aggregatable: false` | `TestBucketMeasuresMarkedNonAggregatable` |
| AC-3 | A 60-minute p95 from the endpoint matches the true p95 within the sketch bound | `TestWindowPercentileAccurate` |
| AC-4 | The endpoint's answer differs materially from the old `max` answer on skewed data | `TestEndpointBeatsOldMaxBehaviour` |
| AC-5 | `errorRate`, `requestCount`, `errorCount` are unchanged | `TestCorrectMeasuresUnchanged` |
| AC-6 | Pre-sketch partitions return 422 naming the days | `TestPreSketchWindowReturns422` |
| AC-7 | Invalid quantile returns 400 naming the parameter | `TestInvalidQuantileReturns400` |
| AC-8 | A window over 31 days returns 400 | `TestWindowLimitEnforced` |
| AC-9 | All four DuckDB/Trino × single/multi-tenant configurations resolve | `TestAllFourCubeConfigs` |
| AC-10 | `latencySketch` is hidden from the dashboard's measure list | `TestSketchDimensionHidden` |
| AC-11 | The dashboard never requests a coarse percentile from Cube | `TestDashboardRoutesCoarsePercentiles` |
| AC-12 | Endpoint p95 for a 1,440-sketch merge is recorded, and ≤400 ms | `TestPercentileEndpointLatencyBudget` |

## 8. Verification

```bash
# 1. The wrong measure is gone
grep -n "type: \`max\`" cube/model/schema/RequestMetricsMinute.js || echo "no max over percentiles"
# expect: no max over percentiles

# 2. Correctness of the replacement
go test ./services/gateway/... -run 'TestWindowPercentileAccurate|TestEndpointBeatsOldMaxBehaviour' -v
# expect: PASS, printing both errors for comparison

# 3. Correct measures untouched
go test ./services/gateway/... -run TestCorrectMeasuresUnchanged -v
# expect: PASS

# 4. Honest 422 for pre-sketch data
go test ./services/gateway/... -run TestPreSketchWindowReturns422 -v
# expect: PASS

# 5. All four Cube configurations
go test ./services/gateway/... -run TestAllFourCubeConfigs -v
# expect: PASS

# 6. Latency budget
go test ./services/gateway/... -run TestPercentileEndpointLatencyBudget -v
# expect: PASS, with the measured p95 printed

# 7. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 8.1 Verification record — run 2026-09-11

Real output, in the order §8 lists the commands.

```
# 1. The wrong measure is gone
$ grep -n "type: \`max\`" cube/model/schema/RequestMetricsMinute.js || echo "no max over percentiles"
no max over percentiles

# 2. Correctness of the replacement
--- PASS: TestWindowPercentileAccurate (0.12s)
    true p95 = 76.9302, merged sketch = 76.8294, relative error = 0.001311
--- PASS: TestEndpointBeatsOldMaxBehaviour (0.11s)
    true p95 = 72.2330 | old max-of-p95 = 139.8450 (error 0.9360) | merged sketch = 72.4337 (error 0.002779)

# 3. Correct measures untouched
--- PASS: TestCorrectMeasuresUnchanged (0.00s)

# 4. Honest 422 for pre-sketch data
--- PASS: TestPreSketchWindowReturns422 (0.12s)

# 5. All four Cube configurations
--- PASS: TestAllFourCubeConfigs (0.00s)
$ node --test tests/cube/RequestMetricsMinute.test.js
# tests 14 / # pass 14 / # fail 0

# 6. Latency budget
--- PASS: TestPercentileEndpointLatencyBudget (0.53s)
    merging 1,440 sketches: p95 = 68.174902ms (runs: [58.53133ms 63.407316ms 66.280013ms 67.698966ms 68.174902ms])

# 7. Full suite
$ go test ./... 2>&1 | tail -20
ok  (41 packages, no failures)

# 8. Open-core integrity
$ make check-boundary && make build-oss && make test-oss
boundary: 0 violations
./scripts/build_oss.sh build        # succeeded
ok  github.com/lgreene/gravix-dashboards/services/gateway  20.239s
```

### The number that matters

On heavy-tailed (Pareto, α=1.5) latency over a 60-minute window:

| | value | error vs. the true p95 |
|---|---|---|
| True p95 over all 12,000 observations | 72.23 ms | — |
| **Old behaviour:** max of the 60 per-minute p95s | 139.85 ms | **+93.6%** |
| **New behaviour:** merged t-digest | 72.43 ms | **+0.28%** |

The old number was not slightly off. It was very nearly double, and it looked like a plausible latency
the whole time. GRVX-804 measured 62% on its own data; on this distribution it is 94%.

### Budget

G4.5 allows 400 ms p95 for this endpoint. A 1,440-sketch merge — a full day at minute granularity —
measures **68 ms p95** over five runs, 17% of the budget. Nothing to report to `perf-cost-engineer`.

### Deviations

Four, all recorded in `docs/oss/spec-defects.md` as **SD-009**: the sketch is read from the object
store rather than through Cube (§6.4); the per-endpoint table renders `—` rather than a percentile
because the endpoint has no `group_by` yet (§5.4); two pre-aggregations named measures this spec
deletes, and would have cached a rolled-up percentile had they merely been renamed; and §4.2's file
list omitted `services/gateway/gateway_alerts.go`, whose latency alerts read a deleted measure and were
themselves firing on max-of-per-minute-p95s. Read SD-009 before GRVX-809 and GRVX-811, which inherit
the first two.

One pre-existing bug found and recorded as **F-005**: `/api/v1/metrics` maps its percentile parameters
onto Cube members that have never existed, so those three metrics have never worked. Not fixed here —
the file is outside §4.2, and the fix is an interface decision.

## 9. Definition of done

- [x] All twelve acceptance criteria pass with their named tests
- [x] Every Verification command run, real output pasted into the report (§8.1)
- [x] Every pre-existing Cube measure and dimension recorded before and after
- [x] No `type: max` over any percentile column anywhere
- [x] The measured endpoint p95 recorded (68 ms, budget 400 ms); no breach to report
- [x] The `type: min` reasoning present as a code comment
- [x] `docs/openapi.yaml` documents the endpoint, its 422 and its 503
- [x] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The merge cannot meet the 400 ms budget for a 1,440-sketch window | Report the measured number to `perf-cost-engineer` and return `SPEC DEFECT: §5.1 — merge costs <n>ms`. Do not fall back to `max`; a fast wrong answer is not an improvement. |
| A dashboard chart still reads a `bucket*` measure across a wide window | Return `SPEC DEFECT: §5.4 — <chart>` |
| Pressure to implement the sketch merge in SQL | Refuse, citing §5.1 and non-goal §6. Route to `semantic-modeler`. |
