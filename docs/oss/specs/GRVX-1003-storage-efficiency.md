# SPEC GRVX-1003: Reach ≤120 bytes per event at 30-day retention

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1003 | **Phase** | 10 | **Goal** | G4.4 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Storage cost is what a self-hoster pays directly; making the free tier wasteful to sell a cheaper paid one would be crippleware. |
| **Implementer role** | `perf-cost-engineer`, with `senior-engineer` |
| **Depends on** | GRVX-1001, GRVX-804 |
| **Blocks** | GRVX-1004, GRVX-1007 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Bring steady-state storage to **≤120 bytes per ingested event** at 30-day retention, measured
end-to-end across raw JSONL, rolled-up Parquet and compacted Parquet — without discarding any fact
and without loosening any correctness guarantee from Phase 8.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go` writes Parquet with `parquet.NewGenericWriter[MetricRow]` and `zstd`. GRVX-801 §5.4 requirement 2 pins the compression level to a package constant for determinism; changing it changes every content digest.
- `MetricRow` has 12 fields plus the two added by GRVX-804 (`LatencySketch []byte`, `SketchVersion string`).
- GRVX-804 AC-14 already budgets sketch bytes at ≤40 bytes/row at p95.
- `transforms/compaction/main.go` merges small Parquet files toward 128 MB targets.
- Raw JSONL in `data/raw/` is retained 30 days and is the source of truth for recompute — it cannot be deleted or lossily re-encoded without breaking `docs/00-system-truth.md` §4.
- `pkg/manifest` writes a `.manifest.json` per partition (GRVX-802); manifests count toward the footprint.
- `bench/` measures `BytesPerEventRaw`, `BytesPerEventRolledUp`, `BytesPerEventCompacted`.

## 3. Non-goals for this spec

- Do NOT delete, sample, or lossily encode raw facts. Recomputability depends on them.
- Do NOT change the compression level without also changing GRVX-801's `CompressionLevel` constant and re-verifying determinism. If the two conflict, determinism wins.
- Do NOT drop the exact per-bucket percentile scalars to save space. GRVX-804 keeps them deliberately.
- Do NOT reduce retention below 30 days.
- Do NOT introduce a storage format a third-party Parquet reader cannot open — that would break Axis 3 and GRVX-1101.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/encoding/encoding.go` | Column encoding selection and its budget |
| `pkg/encoding/encoding_test.go` | Tests |
| `bench/storage/footprint.go` | End-to-end footprint measurement |
| `bench/storage/footprint_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `transforms/request_metrics_minute/main.go` | Apply per-column encodings from §5.2 to the Parquet writer |
| `transforms/compaction/main.go` | Apply the same encodings on merge |
| `pkg/recompute/recompute.go` | Same, so cron and recompute output stay byte-identical |
| `scripts/perf_baseline.json` | Record the achieved bytes/event |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/**` | Raw JSONL format is the recompute source of truth |
| `pkg/sketch/**` | Sketch size is GRVX-804's budget |
| `cmd/purge/**` | Retention unchanged |

## 5. Interface contract

### 5.1 The budget, decomposed

| Component | Budget (bytes/event) | Rationale |
|---|---|---|
| Raw JSONL, compressed at rest | ≤70 | The dominant term; one fact is ~200 bytes of JSON, ~3× compressible |
| Rolled-up Parquet, amortised | ≤30 | One row covers many events; the divisor is events per bucket-key |
| Sketch column, amortised | ≤15 | GRVX-804's ≤40 bytes/row over ≥3 events/row |
| Manifests | ≤1 | One small JSON per partition per day |
| Slack | ≤4 | |
| **Total** | **≤120** | G4.4 |

Each component is measured and reported separately. A single total hides which term regressed.

### 5.2 Per-column encodings

```go
// Package encoding selects Parquet column encodings for Gravix metric rows.
// Encodings are pinned rather than left to the writer's defaults, because a
// library upgrade changing a default would change every file's size without
// any Gravix change, making a regression untraceable.
package encoding

// ColumnEncoding pairs a column with the encoding it must use.
type ColumnEncoding struct {
    Column   string
    Encoding string // "dict" | "rle" | "delta" | "plain" | "byte_stream_split"
}

// MetricRowEncodings returns the pinned encoding for every MetricRow column.
func MetricRowEncodings() []ColumnEncoding
```

Required assignments, chosen from the data's actual shape:

| Column | Encoding | Why |
|---|---|---|
| `tenant_id` | `dict` | Very low cardinality |
| `bucket_start` | `delta` | Monotonic within a partition |
| `service` | `dict` | Bounded by non-goal §5 |
| `method` | `dict` | At most ~8 values |
| `path_template` | `dict` | Bounded at <1000/day |
| `event_day` | `dict` | One value per partition |
| `request_count`, `error_count` | `rle` | Small integers, many repeats |
| `error_rate`, `p50/p95/p99_latency_ms` | `byte_stream_split` | Floats; splits mantissa bytes into compressible streams |
| `latency_sketch` | `plain` | Already-compressed binary; re-encoding wastes CPU |
| `sketch_version` | `dict` | One value per partition |

### 5.3 Compression level

`zstd.SpeedFastest` is pinned by GRVX-801 for determinism. This spec may propose a different level
**only** by changing GRVX-801's `CompressionLevel` constant in the same change and re-running
`TestRecomputeDeterminism`. Measure both levels and report the size/CPU trade before choosing;
do not change it silently.

### 5.4 Measurement

`bench/storage/footprint.go` reports each §5.1 component separately, at 1, 7 and 30 days, with the
30-day figure the one that counts. Steady state matters: day 1 is unrepresentative because
compaction has not run.

## 6. Behaviour

1. Measure the current footprint per component with `bench/` and record the baseline in the report.
2. Implement `pkg/encoding` with the §5.2 assignments.
3. Apply the encodings in all three writers — rollup, recompute and compaction — from the single shared function, so they cannot drift.
4. Re-run `TestRecomputeDeterminism` and `TestRecomputeDeterminismWithSketch`. Any failure means the encoding change broke reproducibility and must be reverted, not worked around.
5. Measure both compression levels; record size and CPU for each; change the constant only if the trade is favourable, and update GRVX-801's constant in the same change.
6. Measure the 30-day steady-state footprint per component.
7. If the total exceeds 120, report the shortfall per component rather than adjusting the target.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Determinism test fails after an encoding change | revert, exit 1 | `encoding change broke recompute determinism; reverted` |
| A third-party Parquet reader cannot open the output | exit 1 | `output unreadable by <reader>; Axis 3 requires open-format access` |
| Budget not met | report per component, do not adjust the target | `storage: <n> bytes/event, budget 120; over by <m> in <component>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | 30-day steady-state footprint ≤120 bytes/event | `TestStorageBudgetMet` |
| AC-2 | Each §5.1 component is measured and reported separately | `TestFootprintComponentsReported` |
| AC-3 | Encodings are applied identically by rollup, recompute and compaction | `TestEncodingsConsistentAcrossWriters` |
| AC-4 | Recompute determinism still holds | `TestRecomputeDeterminismWithEncodings` |
| AC-5 | Output is readable by a stock Parquet reader that is not Gravix | `TestOutputReadableByThirdParty` |
| AC-6 | No raw fact is deleted or lossily re-encoded | `TestRawFactsUnmodified` |
| AC-7 | Encodings are pinned, not left to writer defaults | `TestEncodingsArePinned` |
| AC-8 | Compression level matches GRVX-801's constant | `TestCompressionLevelMatchesDeterminismConstant` |
| AC-9 | Measurement is at 30-day steady state, not day 1 | `TestSteadyStateMeasurement` |

## 8. Verification

```bash
# 1. The budget
go test ./bench/storage/... -run TestStorageBudgetMet -v
# expect: PASS, with the per-component breakdown printed

# 2. Determinism survives
go test ./pkg/recompute/... -run 'TestRecomputeDeterminism' -v
# expect: PASS

# 3. Third-party readability — Axis 3 depends on it
go test ./bench/storage/... -run TestOutputReadableByThirdParty -v
duckdb -c "SELECT count(*) FROM read_parquet('data/warehouse/**/*.parquet');" 2>/dev/null || echo "duckdb absent, test covers it"
# expect: PASS

# 4. Facts untouched
go test ./bench/storage/... -run TestRawFactsUnmodified -v
# expect: PASS

# 5. Encodings consistent and pinned
go test ./pkg/encoding/... -v -cover
# expect: PASS, coverage >= 95%

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Before and after footprint recorded per component
- [ ] Both compression levels measured, with the size/CPU trade recorded
- [ ] Determinism re-verified after every encoding change
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The 120-byte budget unreachable without losing correctness | Report the achievable number per component and return `SPEC DEFECT: §5.1`. Never trade a correctness guarantee for bytes; the budget is `perf-cost-engineer`'s to renegotiate with `cpo`. |
| An encoding that shrinks files but breaks determinism | Revert it. GRVX-801 outranks this spec. |
| Output no longer readable by a third-party reader | STOP. This breaks thesis Axis 3 and GRVX-1101. |

---

## 11. Implementation report

**`SPEC DEFECT: §5.1 — the ≤70 bytes/event raw term assumes JSONL is "compressed at rest", and
nothing in the repository compresses it; §4 provides no file where that could be added.`**

Returned under §10 row 1, with the measured decomposition below rather than an adjusted target.

### 11.1 §6 step 1 — the current footprint, measured

`./bench/run.sh --scale small`, 1,008,000 facts, then every file under the work directory sized by
component:

| Component | bytes | bytes/event | Budget | |
|---|---:|---:|---:|---|
| Raw JSONL, **as stored today** | 205,410,959 | **203.78** | ≤70 | **over by 133.78** |
| Rolled-up Parquet (incl. sketch) | 2,914,137 | 2.89 | ≤30 + ≤15 | 42.11 under |
| Manifests | 11,624 | 0.01 | ≤1 | 0.99 under |
| **Total** | | **206.68** | **≤120** | **over by 86.68** |

**The entire overage is one term, and it is not the one this spec optimises.** Parquet sits at 2.89
against a 45-byte allowance — roughly one twentieth of its budget. The per-column encodings in §5.2
would optimise that 2.89. Even reducing it to zero leaves the total at 203.79, still 70% over.

### 11.2 The budget is reachable, by the one change §4 forbids

Raw JSONL is stored as plain text. Nothing compresses it — not ingestion
(`services/ingestion/main.go` writes the buffer through `LocalStore` verbatim), and not compaction
(`compactJSONLGroup` concatenates lines into a `bytes.Buffer` and `Put`s it uncompressed).

Compressing the same bytes with `gzip -6`:

| | bytes | bytes/event |
|---|---:|---:|
| Raw JSONL, gzip -6 | 32,946,467 | **32.68** |
| **Total with compressed raw** | | **35.59** |

35.59 against a budget of 120. The raw term alone lands at 32.68 against its own ≤70. The budget is
not ambitious — it is met with room to spare by the single change nobody has made.

### 11.3 Why it cannot be made under this spec

§5.1 describes the raw term as "Raw JSONL, **compressed at rest**". That capability does not exist,
and §4 gives nowhere to build it:

- **§4.3 forbids `services/ingestion/**`**, "Raw JSONL format is the recompute source of truth" —
  which is where compression at write would go.
- **§4.2 lists `transforms/compaction/main.go`**, but only "Apply the same encodings on merge",
  meaning Parquet column encodings. Compressing JSONL there is a different change.
- **No reader is in §4 at all.** `pkg/storage`, `pkg/etl`'s `ReadLines`, and `pkg/recompute`'s fact
  scan would each need to handle a compressed member, or every consumer breaks the moment a `.gz`
  appears. Adding compression without them is a change that destroys the data's readability, which
  §3 and thesis Axis 3 both forbid.

So the work is real, worth doing, and roughly six times more valuable than everything §5.2 asks for —
and it is a different spec.

### 11.4 What was implemented

Nothing. Per CLAUDE.md, a spec insufficient to execute returns a `SPEC DEFECT` rather than
improvising, and implementing §5.2's encodings would have meant shipping a change that optimises 2.4%
of the footprint, reports the budget still missed by 86 bytes, and leaves the reader to discover that
the dominant term was never in scope.

### 11.5 Also found — CD-005, a correctness defect

While reading the three Parquet writers for §5.2: `pkg/recompute` pins `CompressionLevel =
zstd.SpeedFastest` explicitly for determinism, and `transforms/compaction` (three writers) plus both
service-events transforms use `zstd.SpeedDefault`. The same rows at the two levels differ:

```
recompute  (SpeedFastest):  6627 bytes  sha256:c92b444de45fa463
compaction (SpeedDefault):  6421 bytes  sha256:de2f65a2c8f1f747
```

Once compaction touches a partition, recompute can never report it `Unchanged` again, `Revised` fires
on every run for data that has not moved, and the digest `gravix explain` prints is not stable across
compaction. Recorded as **CD-005** against GRVX-801.

This is AC-8's subject (`TestCompressionLevelMatchesDeterminismConstant`), so it is in this spec's
scope to *detect* — and it fails today. It is not in scope to fix: §5.3 forbids changing the level
without changing GRVX-801's constant, and choosing which level wins is a size-versus-CPU trade
belonging to GRVX-801's owner.

### 11.6 Recommended sequencing

1. **Fix CD-005** under GRVX-801: one shared constant, and a test that recomputes, compacts, and
   recomputes again expecting `Rebuilt == 0`.
2. **A new spec for raw-JSONL compression at rest**, scoped to include the readers — the ~171
   bytes/event this spec's budget actually turns on.
3. **Then GRVX-1003's §5.2 encodings**, which are worth doing on their merits and will move the total
   by at most 2.89 bytes/event.

### 11.7 Definition of done

Every box stays unticked; the spec was not executed.

- [ ] All nine acceptance criteria pass — AC-8 fails today (CD-005); the rest were not attempted
- [x] Footprint recorded per component, before any change (§11.1)
- [ ] Both compression levels measured with the size/CPU trade — partially: sizes measured for
      CD-005, CPU not, because the level is not this spec's to change
- [ ] `docs-engineer` delta merged — nothing shipped to document
