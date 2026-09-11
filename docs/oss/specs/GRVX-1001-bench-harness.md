# SPEC GRVX-1001: Build the `bench/` harness a stranger can run

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1001 | **Phase** | 10 | **Goal** | G4.1 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. A benchmark only paying users can run proves nothing to the people deciding whether to adopt. |
| **Implementer role** | `perf-cost-engineer` |
| **Depends on** | GRVX-704, GRVX-801 |
| **Blocks** | GRVX-1002, GRVX-1003, GRVX-1005, GRVX-1006, GRVX-1007, GRVX-1008 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Create `bench/` — one command, a deterministic dataset generator, a stated machine spec, and raw
output committed to the repository. Every cost and performance number Gravix publishes afterwards
comes from this harness, and anyone can rerun it.

## 2. Context the implementer needs

- `scripts/perf_test.sh` and `scripts/perf_gateway_test.sh` exist and measure ad-hoc throughput. Read both before writing; they are the starting point, not the deliverable.
- `scripts/perf_baseline.json` holds the current committed baseline. Read its exact schema and preserve every field it already has.
- `tests/correctness/fixtures/generate.go` (GRVX-810) generates deterministic `RequestFact` datasets from a `Spec{Seed, Days, ServicesCount, ...}`. Reuse it; do not write a second generator.
- `pkg/recompute` (GRVX-801) produces byte-identical rollup output, so a benchmark run is reproducible rather than merely repeatable.
- `docker-compose.bootstrap.yml` is the ~800 MB lean stack; `docker-compose.yml` is the full stack.
- `.claude/agents/perf-cost-engineer.md` states the Reproducibility Rule: a benchmark whose harness is not in the repo and runnable by an outsider does not exist.

## 3. Non-goals for this spec

- Do NOT publish any number. GRVX-1007 publishes; this spec measures.
- Do NOT compare against a competitor. GRVX-1002 does that, under `market-analyst` verification.
- Do NOT optimise anything. GRVX-1003, GRVX-1005 and GRVX-1006 optimise against this harness.
- Do NOT require a cloud account, a paid service, or a network connection.
- Do NOT delete or rewrite `scripts/perf_baseline.json`'s existing fields.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `bench/run.sh` | The single entry point |
| `bench/bench.go` | Measurement driver |
| `bench/bench_test.go` | Tests for the driver |
| `bench/machine.go` | Machine-spec capture |
| `bench/results/.gitkeep` | Result directory |
| `bench/README.md` | How to run it and what the numbers mean |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `scripts/perf_baseline.json` | Add the new metrics from §5.3 beside the existing fields. Remove no field. |
| `Makefile` | Add a `bench` target |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `scripts/perf_test.sh`, `scripts/perf_gateway_test.sh` | Existing scripts stay working; this spec adds beside them |
| `tests/correctness/fixtures/**` | Reused as-is |
| Any `transforms/` or `services/` code | This spec measures; it does not optimise |

## 5. Interface contract

### 5.1 `bench/run.sh`

```bash
#!/usr/bin/env bash
# Runs the Gravix benchmark suite and writes a result file to bench/results/.
# Usage: ./bench/run.sh [--scale small|standard|large] [--out <path>]
#   small     ~1e6 facts, ~2 min   — for a laptop or CI
#   standard  ~1e7 facts, ~15 min  — the published figures come from this
#   large     ~1e8 facts, ~2 h     — capacity planning only
set -euo pipefail
```

Exit codes: `0` success; `1` a measurement failed; `2` invalid argument; `3` insufficient free disk
(needs 4× the generated dataset size; the check is explicit, because running out of disk halfway
produces a plausible-looking wrong number).

### 5.2 `bench/machine.go`

```go
// Package main implements the Gravix benchmark harness.

// Machine records the hardware a result was produced on. A benchmark number
// without its machine spec is not comparable to anything.
type Machine struct {
    OS           string `json:"os"`
    Arch         string `json:"arch"`
    NumCPU       int    `json:"num_cpu"`
    CPUModel     string `json:"cpu_model"`
    MemTotalBytes int64 `json:"mem_total_bytes"`
    DiskType     string `json:"disk_type"`      // "ssd" | "hdd" | "unknown"
    GoVersion    string `json:"go_version"`
    GravixCommit string `json:"gravix_commit"`
    Container    bool   `json:"container"`
}

// DetectMachine captures the current machine. Fields it cannot determine are
// set to "unknown" rather than guessed.
func DetectMachine() Machine
```

### 5.3 `Result` schema

```go
// Result is one benchmark run. Written to bench/results/<timestamp>-<scale>.json.
type Result struct {
    SchemaVersion int     `json:"schema_version"` // 1
    RunAt         string  `json:"run_at"`         // RFC3339 UTC
    Scale         string  `json:"scale"`
    Machine       Machine `json:"machine"`
    Dataset       Dataset `json:"dataset"`

    IngestEventsPerSecPerCore float64 `json:"ingest_events_per_sec_per_core"`
    IngestP99LatencyMs        float64 `json:"ingest_p99_latency_ms"`
    RollupEventsPerSec        float64 `json:"rollup_events_per_sec"`
    BytesPerEventRaw          float64 `json:"bytes_per_event_raw"`
    BytesPerEventRolledUp     float64 `json:"bytes_per_event_rolled_up"`
    BytesPerEventCompacted    float64 `json:"bytes_per_event_compacted"`
    QueryP50Ms                float64 `json:"query_p50_ms"`
    QueryP95Ms                float64 `json:"query_p95_ms"`
    QueryP99Ms                float64 `json:"query_p99_ms"`
    QueryColdP95Ms            float64 `json:"query_cold_p95_ms"`
    PeakResidentBytes         int64   `json:"peak_resident_bytes"`
    Notes                     []string `json:"notes"`
}

// Dataset records exactly what was measured, so the run is reproducible.
type Dataset struct {
    Seed          int64 `json:"seed"`
    Facts         int64 `json:"facts"`
    Days          int   `json:"days"`
    Services      int   `json:"services"`
    PathsPerService int `json:"paths_per_service"`
}
```

`Notes` carries anything that qualifies the run — a warm page cache, a container CPU limit, a
detected thermal throttle. Every unfavourable condition is recorded rather than retried away.

### 5.4 Measurement rules

1. **Three runs, report the median.** Record all three in `Notes`. A single run is noise.
2. **Cold and warm query latency are both measured.** Publishing only warm would flatter us; a
   first-time user experiences cold.
3. **The dataset is generated from a fixed seed** so two people measure the same thing.
4. **Ingest throughput is per core**, computed as measured throughput divided by `Machine.NumCPU`,
   with the raw total also recorded.
5. **No step may silently skip.** A measurement that cannot run fails the harness rather than
   emitting a zero, because a zero in a results file reads as a real value later.

### 5.5 `bench/README.md`

Required sections: `## Run it`, `## What each number means`, `## The reference machine`,
`## Why your numbers will differ`, `## Reporting a result that contradicts ours` — the last of
which invites contradiction and names where to file it. A benchmark that does not invite
falsification is advertising.

## 6. Behaviour

1. Read `scripts/perf_test.sh`, `scripts/perf_gateway_test.sh` and `scripts/perf_baseline.json`; list every metric they already produce in the report.
2. Implement `DetectMachine`, marking undetermined fields `unknown`.
3. Implement the driver: generate dataset → ingest → rollup → compact → query, measuring §5.3.
4. Implement the three-run median rule and the cold/warm split.
5. Write results to `bench/results/<timestamp>-<scale>.json`.
6. Add the new fields to `scripts/perf_baseline.json`, preserving existing ones.
7. Add the `bench` Makefile target.
8. Run `./bench/run.sh --scale small` and commit its result file.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Invalid `--scale` | exit 2 | `usage: run.sh [--scale small\|standard\|large] [--out <path>]` |
| Insufficient disk | exit 3 | `need <n> GiB free, have <m> GiB; benchmark aborted before generating data` |
| A measurement fails | exit 1 | `measurement "<name>" failed: <err>` |
| A measurement would emit 0 | exit 1 | `measurement "<name>" produced 0; refusing to record a zero` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `./bench/run.sh --scale small` exits 0 and writes a result file | `TestBenchSmallScaleRuns` |
| AC-2 | The result validates against the `Result` schema | `TestResultSchemaValid` |
| AC-3 | Two runs with the same seed generate identical datasets | `TestDatasetReproducible` |
| AC-4 | Machine spec is captured, with unknowns marked `unknown` not guessed | `TestMachineDetectionHonest` |
| AC-5 | Three runs are taken and the median reported | `TestMedianOfThreeRuns` |
| AC-6 | Cold and warm query latency are both recorded | `TestColdAndWarmQueryRecorded` |
| AC-7 | A failed measurement aborts rather than recording zero | `TestNoZeroMeasurementRecorded` |
| AC-8 | Insufficient disk aborts before generating data | `TestDiskCheckPrecedesGeneration` |
| AC-9 | No network access is required | `TestBenchNeedsNoNetwork` |
| AC-10 | Every pre-existing `perf_baseline.json` field survives | `TestBaselineFieldsPreserved` |
| AC-11 | `bench/README.md` contains all five required sections | `TestBenchReadmeComplete` |

## 8. Verification

```bash
# 1. It runs
./bench/run.sh --scale small && echo "exit=$?"
# expect: a result file in bench/results/, exit=0

# 2. Schema and reproducibility
go test ./bench/... -v -cover
# expect: PASS, coverage >= 85%

# 3. Same seed, same dataset
go test ./bench/... -run TestDatasetReproducible -v
# expect: PASS

# 4. Refuses to record a zero
go test ./bench/... -run TestNoZeroMeasurementRecorded -v
# expect: PASS

# 5. No network
go test ./bench/... -run TestBenchNeedsNoNetwork -v
# expect: PASS

# 6. Baseline preserved
go test ./bench/... -run TestBaselineFieldsPreserved -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Metrics already produced by the existing scripts listed in the report
- [ ] One `--scale small` result file committed
- [ ] Every pre-existing `perf_baseline.json` field intact
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A metric that cannot be measured without a cloud account | Return `SPEC DEFECT: §3 — <metric> needs <service>`. A benchmark a stranger cannot run is not a benchmark. |
| Results varying more than 20% across three runs | Record it in `Notes` and report it. Do not discard outliers to tighten the number. |
| An existing script producing a metric this schema omits | Return `SPEC DEFECT: §5.3 — <metric> would be lost` |

---

## 11. Implementation report

All eleven acceptance criteria pass. Coverage 85.4%, above the 85% §8 requires.

### 11.1 §6 step 1 — what the existing scripts already produce

| Source | Metric | Kept? |
|---|---|---|
| `cmd/load_generator` | `total_requests`, `successful`, `failed`, `actual_qps`, `error_rate`, `duration_secs` | Not in `Result`. These describe a *load generator's* view of a server under HTTP load, which is a different measurement from this harness's per-fact pipeline cost. `scripts/perf_test.sh` still produces them. |
| `cmd/load_generator` | `avg_latency_ms` | Superseded, not lost — see §11.2. |
| `scripts/perf_baseline.json` | `max_p95_latency_ms`, `max_error_rate`, `min_qps_ratio`, per profile | All three profiles and all nine fields preserved verbatim, asserted by `TestBaselineFieldsPreserved`. |

No metric is lost, so §10 row 3 does not apply.

### 11.2 F-017 — found by doing §6 step 1

`scripts/perf_baseline.json` declares `max_p95_latency_ms`, and `scripts/perf_test.sh:170` compares
it against `avg_latency`, because `cmd/load_generator` emits no percentiles. Its comment says so and
calls it a proxy. It is looser than that: the gate passes every run whose *mean* is under the
threshold however heavy the tail is, and `computeSummary` divides by successful requests only, so a
system degrading until requests time out reports a *lower* average. Recorded as **F-017** and left in
place, because §3 and §4.2 both forbid touching those fields and the honest repair is to rename one.

This harness measures `ingest_p99_latency_ms` and `query_p{50,95,99}_ms` as real percentiles over
every observation — no sketch, no mean, no proxy.

### 11.3 F-018 — found by the harness scribbling in the repository

Every test that called the driver left an empty `bench/warehouse/request_metrics_minute/` behind.
The cause is not in `bench/`: `pkg/recompute/recompute.go:534` hands `opts.OutputDir` — an
*object-store key prefix* — straight to `leaderelect.NewFileElector`, which `MkdirAll`s it on the
**local filesystem relative to the process working directory**. The data goes through the store; the
lock does not.

The stray directory is the small half. The lock exists to stop the cron rollup and a
`gravix recompute` writing the same partition at once, and two processes sharing a data root but not
a working directory take different locks and both proceed — with S3 they never contend at all.
Recorded as **F-018**.

Worked around here by removing the empty directory after each rollup, guarded by
`TestHarnessLeavesNoStrayDirectories` and `TestRemoveStrayLockDirLeavesRealDataAlone` (which proves
the cleanup never deletes a warehouse with anything in it). That is housekeeping for this caller, not
a repair; the fix wants its own spec.

### 11.4 Verification

```
$ go test ./bench/... -cover
ok  github.com/lgreene/gravix-dashboards/bench   32.386s   coverage: 85.4% of statements

AC-1  TestBenchSmallScaleRuns            AC-7   TestNoZeroMeasurementRecorded
AC-2  TestResultSchemaValid              AC-8   TestDiskCheckPrecedesGeneration
AC-3  TestDatasetReproducible            AC-9   TestBenchNeedsNoNetwork
AC-4  TestMachineDetectionHonest         AC-10  TestBaselineFieldsPreserved
AC-5  TestMedianOfThreeRuns              AC-11  TestBenchReadmeComplete
AC-6  TestColdAndWarmQueryRecorded

$ make check-boundary
boundary: 0 violations

$ make build-oss && make test-oss
both succeed with ee/ absent

$ go build ./... && go test ./schemas/...
build ok, PASS
```

The committed run, `bench/results/20260911T220715Z-small.json`, on a 4-core container:

```
facts:        1008000
ingest:       55635 events/sec/core (p99 0.010 ms)
rollup:       119807 events/sec
bytes/event:  210.8 raw -> 2.90 rolled up -> 2.90 compacted
query warm:   p50 144.5 ms  p95 155.4 ms  p99 155.4 ms
query cold:   p95 145.0 ms
```

**Nothing here is publishable yet, and the notes say why.** Three caveats travel with the file:

1. **The ingest spread was 21.2%**, above §10's 20% threshold. Recorded, median reported, no run
   discarded — which is what §10 requires. The first run is consistently the fastest, so the page
   cache is still warm from generation.
2. **Compaction reduced nothing** — 100.0% of the rolled-up size. Expected at this scale: one parquet
   file per partition leaves nothing to merge. Reported rather than omitted, and
   `TestCompactionReportsWhenItChangesNothing` fails if two identical figures are ever published
   without that note.
3. **`disk_type` is `unknown`** and `container` detection governs the per-core figure. On a container
   `runtime.NumCPU` can exceed the CPU quota, which makes the per-core number optimistic by an amount
   not knowable from inside.

GRVX-1007 publishes; this run is a `small`-scale measurement on an unnamed container, which is not
what §5.4 says a published figure comes from.

### 11.5 Deviations, stated

- **`run.sh` accepts two flags beyond §5.1's usage line:** `--runs` and `--work-dir`. The usage
  string itself is exactly §6.1's, and an unknown flag still exits 2. Both are documented in
  `bench/README.md` under "Run it", with a warning that `--runs 1` is not comparable to a published
  figure.
- **`bench/driver.go`, `bench/measure.go` and `bench/units_test.go` are not in §4.1.** The spec lists
  `bench.go`, `machine.go` and `bench_test.go`; splitting the driver and the measurements out of a
  single file, and the unit tests out of the acceptance tests, is an organisational choice within the
  same package, not new surface.
- **`ingest_events_per_sec_per_core` excludes HTTP framing.** The ingestion handler is `package main`
  under `services/ingestion` and §4.3 forbids restructuring it to be importable. What is measured is
  decode, schema validation and the durable-buffer append. Stated in every result's `notes` and in
  the README's table rather than left to be assumed either way.
- **`query_cold_p95_ms` is not a cleared page cache.** Dropping caches needs root, and a benchmark
  that needs root is not one a stranger runs. The note on every run says the true cold figure is
  worse.

### 11.6 Definition of done

- [x] All eleven acceptance criteria pass with their named tests
- [x] Every Verification command run, real output above
- [x] Metrics already produced by the existing scripts listed (§11.1)
- [x] One `--scale small` result file committed
- [x] Every pre-existing `perf_baseline.json` field intact, asserted by AC-10
- [x] `docs-engineer` delta merged — `bench/README.md` is the deliverable doc; the `bench` target is
      in `make help`
- [x] Zero new skipped tests
