# SPEC GRVX-1005: Reach ≥20,000 events/sec/core ingest throughput

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1005 | **Phase** | 10 | **Goal** | G4.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Throttling the free tier's ingest to sell a faster one is the textbook crippleware pattern. |
| **Implementer role** | `senior-engineer`, measured by `perf-cost-engineer` |
| **Depends on** | GRVX-1001 |
| **Blocks** | GRVX-1007 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Raise sustained ingest to **≥20,000 events/sec/core** on the reference machine, without weakening
the durability guarantee that `docs/00-system-truth.md` §6 requires: the service buffers to disk
before acknowledging receipt.

## 2. Context the implementer needs

- `services/ingestion/main.go` validates each fact, appends JSONL to a durable sink, and rotates files to S3/MinIO. Read it in full and record the current hot path in the report.
- `docs/00-system-truth.md` §6 Fail-Safe: ingestion MUST buffer to disk before acknowledging. Any batching that acknowledges before `fsync` violates it.
- `schemas/request_fact.go` validates every fact and is held at 100% test coverage. Do not weaken validation to gain throughput.
- `services/ingestion/otlp.go` is a second entry path; both share the sink.
- `pkg/ratelimit/` applies per-plan limits. Those protect the system and are not the subject here.
- `bench/` (GRVX-1001) measures `IngestEventsPerSecPerCore` and `IngestP99LatencyMs` and is the arbiter.

## 3. Non-goals for this spec

- Do NOT acknowledge a request before its facts are durable. §6 is absolute.
- Do NOT weaken or skip schema validation. Accepting a malformed fact faster is not throughput.
- Do NOT drop facts under load. Backpressure is correct; silent loss is not.
- Do NOT change the wire format or the JSONL layout — `pkg/recompute` reads it.
- Do NOT add a plan-gated fast path. Charter §7.3 Q1.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `services/ingestion/batch.go` | Group-commit batching of durable writes |
| `services/ingestion/batch_test.go` | Tests, including the durability invariant |
| `bench/ingest/ingest.go` | Load driver for the harness |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/ingestion/main.go` | Route the durable write through the group-commit batcher |
| `services/ingestion/otlp.go` | Use the same batcher, so both paths share one durability guarantee |
| `scripts/perf_baseline.json` | Record the achieved throughput and p99 |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/**` | Validation is not a performance lever |
| `pkg/ratelimit/**` | Limits protect the system; unrelated |
| `pkg/storage/**` | Rotation to object storage is off the hot path |

## 5. Interface contract

### 5.1 Group commit

```go
// Package main, services/ingestion.

// Batcher accumulates facts from concurrent requests and fsyncs them together,
// so N requests cost one fsync instead of N. Each caller still blocks until its
// own facts are durable, which is what docs/00-system-truth.md §6 requires:
// the guarantee is preserved, the syscall is amortised.
type Batcher struct{ /* unexported */ }

// BatcherConfig bounds the batching.
type BatcherConfig struct {
    MaxBatchSize  int           // facts per fsync; default 512
    MaxBatchDelay time.Duration // longest a caller waits for peers; default 2ms
    QueueDepth    int           // facts queued before backpressure; default 8192
}

// NewBatcher returns a Batcher writing to sink.
func NewBatcher(sink io.Writer, syncer Syncer, cfg BatcherConfig) *Batcher

// Append enqueues facts and blocks until they are durable, or until ctx ends.
// It returns only after the fsync covering these facts has completed.
func (b *Batcher) Append(ctx context.Context, facts [][]byte) error

// Close flushes and stops the batcher.
func (b *Batcher) Close() error

var (
    ErrQueueFull = errors.New("ingestion: queue full")
    ErrClosed    = errors.New("ingestion: batcher closed")
)
```

`MaxBatchDelay` of 2ms bounds the added tail latency. It is the whole trade: a caller waits up to
2ms for peers to join its fsync, and in exchange the fsync rate falls by up to `MaxBatchSize`.

### 5.2 Backpressure, not loss

When the queue is full, `Append` returns `ErrQueueFull` and the handler responds
**`503 Service Unavailable`** with `Retry-After: 1` and body
`{"error":"overloaded","retry_after_seconds":1}`. It must never return `200` for a fact it did not
persist. A dropped fact is a correctness incident, not a capacity event.

### 5.3 Durability invariant, tested by kill

`TestDurabilityUnderKill` must: start the service, send N facts, `SIGKILL` the process the moment the
last acknowledgement is received, restart, and assert every acknowledged fact is present on disk.
This is the only test that actually proves §6; a unit test asserting `fsync` was called does not.

## 6. Behaviour

1. Measure current throughput and p99 with `bench/`; record the baseline and the hot path in the report.
2. Implement `Batcher` per §5.1.
3. Route both HTTP and OTLP writes through it.
4. Implement §5.2 backpressure.
5. Write `TestDurabilityUnderKill`.
6. Re-measure. If below 20,000/core, profile and report where the time goes, per component.
7. Verify `go test ./schemas/... -cover` is still 100%.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Queue full | 503 + `Retry-After: 1` | `{"error":"overloaded","retry_after_seconds":1}` |
| fsync fails | 500, do not acknowledge | `{"error":"durability_failure"}` |
| Batcher closed | 503 | `{"error":"shutting_down"}` |
| Context cancelled mid-append | return `ctx.Err()`, do not acknowledge | — |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Sustained ≥20,000 events/sec/core on the reference machine | `TestIngestThroughputTarget` |
| AC-2 | Every acknowledged fact survives `SIGKILL` | `TestDurabilityUnderKill` |
| AC-3 | `Append` returns only after the covering fsync | `TestAppendBlocksUntilDurable` |
| AC-4 | A full queue returns 503, never 200 | `TestBackpressureNeverAcknowledges` |
| AC-5 | Zero facts lost under sustained overload | `TestNoFactLossUnderOverload` |
| AC-6 | Added p99 latency ≤ `MaxBatchDelay` + prior p99 | `TestTailLatencyBounded` |
| AC-7 | HTTP and OTLP share one batcher | `TestBothPathsShareBatcher` |
| AC-8 | Schema validation is unchanged and still 100% covered | `TestValidationUnchanged` |
| AC-9 | No plan-gated fast path exists | `TestNoPlanGatedIngestPath` |

## 8. Verification

```bash
# 1. Throughput
./bench/run.sh --scale standard && jq '.ingest_events_per_sec_per_core' bench/results/*.json | tail -1
# expect: >= 20000

# 2. Durability — the invariant that outranks throughput
go test ./services/ingestion/... -run TestDurabilityUnderKill -v
# expect: PASS

# 3. Backpressure never lies
go test ./services/ingestion/... -run 'TestBackpressureNeverAcknowledges|TestNoFactLossUnderOverload' -v
# expect: PASS

# 4. Validation untouched
go test ./schemas/... -cover
# expect: 100.0% coverage, PASS

# 5. Tail latency
go test ./services/ingestion/... -run TestTailLatencyBounded -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Baseline and achieved throughput both recorded
- [ ] `TestDurabilityUnderKill` passes on a real process kill, not a simulated one
- [ ] `schemas/` still at 100% coverage
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| 20,000/core unreachable without weakening durability | Report the achievable number with durability intact. Return `SPEC DEFECT: §5`. §6 outranks the target; the target is renegotiable, the invariant is not. |
| A path that acknowledges before fsync | STOP. `CORRECTNESS DEFECT: <path>:<line>` — this is data loss waiting for a power cut. |
| Throughput gained by relaxing validation | Revert it. Faster acceptance of malformed facts is not throughput. |
