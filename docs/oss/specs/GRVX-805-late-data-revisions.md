# SPEC GRVX-805: Handle late-arriving facts with event-time bucketing and revision counters

| Field | Value |
|---|---|
| **Spec ID** | GRVX-805 |
| **Phase** | 8 | **Goal** | G2.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. A bucket that silently changes value is a correctness defect. |
| **Implementer role** | `senior-engineer`, semantics owned by `semantic-modeler` |
| **Depends on** | GRVX-802, GRVX-804 |
| **Blocks** | GRVX-807, GRVX-810, GRVX-811 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

A fact whose `event_time` falls in an already-rolled-up bucket must land in **that** bucket, not the
bucket it arrived in. When that happens the partition's revision increments, the prior value stays
reproducible, and a consumer can tell that a number they read yesterday has since been revised.

`docs/00-system-truth.md` §5 already states "Events can arrive late, but `event_time` is the only
source of truth for ordering." This spec makes that observable.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go:347` — `processDay` processes one day at a time, keyed by `event_day`.
- `transforms/request_metrics_minute/main.go:~410` — `AggregationKey{BucketStart, Service, Method, PathTemplate}` where `BucketStart` derives from the fact's `event_time`. Bucketing is already event-time based; what is missing is detection that a rebuilt bucket differs from its predecessor.
- `pkg/manifest` (GRVX-802) has `Revision int` and `ContentDigest string`. `Revision` is written as `0` today and never incremented.
- `pkg/recompute.Run` (GRVX-801) already compares the newly computed bytes against the existing object and reports `Unchanged` when identical.
- `schemas/request_fact.go` validates `event_time`. There is no maximum-lateness rule today: a fact
  with an `event_time` from a year ago is accepted.
- `docs/00-system-truth.md` §2 — facts are immutable. Late data is handled by rebuilding derivatives, never by editing facts.
- Raw and warehouse data older than 30 days is purged (`CLAUDE.md`, `cmd/purge/`). A fact arriving for
  a purged day cannot be rolled up, and that case must be explicit rather than silent.

## 3. Non-goals for this spec

- Do NOT edit or delete any fact. Facts are immutable.
- Do NOT reject late facts by default. Ingesting them is correct; the question is only how derivatives react.
- Do NOT implement streaming or watermark-based windowing. Non-goal §4 forbids streaming; this is batch reconciliation.
- Do NOT notify anyone. GRVX-811 owns alerting; this spec records the revision.
- Do NOT change the purge window.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/lateness/lateness.go` | Lateness classification and the revision decision |
| `pkg/lateness/lateness_test.go` | Tests |
| `pkg/recompute/revision.go` | Revision bump logic on rebuild |
| `pkg/recompute/revision_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/recompute/recompute.go` | On a content change, increment `Revision` and preserve the prior manifest |
| `pkg/manifest/manifest.go` | Add `PreviousDigest string` and `RevisedAt string` fields; bump `SchemaVersion` to 2 |
| `services/ingestion/main.go` | Emit a metric counting accepted facts by lateness class. Change no acceptance decision. |
| `transforms/request_metrics_minute/main.go` | Detect facts whose `event_day` differs from the partition being processed and route them to their own day's partition |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/request_fact.go` | Validation rules unchanged; late is not invalid |
| `cmd/purge/**` | Retention policy unchanged |
| `cube/model/**` | GRVX-808 |

## 5. Interface contract

### 5.1 `pkg/lateness/lateness.go`

```go
// Package lateness classifies how late a fact arrived relative to its event_time
// and decides what that means for derived partitions.
package lateness

// Class is a lateness band. Bands exist so operators can reason about lateness
// without per-fact inspection, which non-goal §5 forbids.
type Class string

const (
    // ClassOnTime: arrived within the current rollup interval.
    ClassOnTime Class = "on_time"
    // ClassLate: arrived after its bucket was rolled up, within retention.
    ClassLate Class = "late"
    // ClassVeryLate: arrived more than 24h after its event_time, within retention.
    ClassVeryLate Class = "very_late"
    // ClassUnprocessable: event_time falls in a purged window. The fact is stored
    // but no derivative can be built for it.
    ClassUnprocessable Class = "unprocessable"
)

// Config bounds the classification.
type Config struct {
    RollupInterval time.Duration // default 5m
    VeryLateAfter  time.Duration // default 24h
    RetentionDays  int           // default 30
}

// DefaultConfig returns RollupInterval 5m, VeryLateAfter 24h, RetentionDays 30.
func DefaultConfig() Config

// Classify returns the class of a fact with the given event time, observed at now.
func Classify(cfg Config, eventTime, now time.Time) Class

// AffectedDay returns the UTC day whose partition a fact belongs to.
// It is derived only from eventTime, never from arrival time.
func AffectedDay(eventTime time.Time) time.Time
```

### 5.2 Manifest additions (`SchemaVersion` 1 → 2)

```go
// PreviousDigest is the ContentDigest this partition held before the most recent
// revision. Empty at Revision 0. It is what lets a consumer prove a number changed.
PreviousDigest string `json:"previous_digest"`

// RevisedAt is the RFC3339 UTC time of the most recent revision. Empty at Revision 0.
RevisedAt string `json:"revised_at"`
```

`RevisedAt` is the only wall-clock value in a manifest, and it is deliberately **excluded from
`ContentDigest`** (which covers row content only, GRVX-802 §5.2) so its presence cannot make output
non-reproducible.

### 5.3 Revision decision

On each rebuild of a partition:

| Existing manifest | New digest vs existing | Action |
|---|---|---|
| absent | — | write, `Revision = 0`, `PreviousDigest = ""`, `RevisedAt = ""` |
| present | identical | write nothing; report `Unchanged` |
| present | different | write, `Revision = old.Revision + 1`, `PreviousDigest = old.ContentDigest`, `RevisedAt = now().UTC().Format(time.RFC3339)` |

A `Revision` above 0 is a factual statement that the window's value changed after first publication.
It is not an error and must not be logged as one.

### 5.4 Ingestion metric

Add one Prometheus counter to `services/ingestion/main.go`:

```go
// gravix_facts_received_by_lateness_total counts accepted facts by lateness class.
// Labels: class (on_time|late|very_late|unprocessable), service.
```

`class` has four bounded values and `service` is already a bounded dimension, so this respects
non-goal §5. Do not add a `path_template` or any per-fact label.

### 5.5 Unprocessable handling

A fact classified `ClassUnprocessable` is **still accepted and stored** — facts are immutable and
storing them costs almost nothing. It is counted under the `unprocessable` class and written to the
existing DLQ prefix (`dlq/<tenant>/unprocessable/`) **in addition to** the raw path, so an operator
can find it. No derivative is built. This is stated in the ingestion response as a
`202 Accepted` with body `{"accepted":1,"note":"event_time precedes the retention window; stored but not aggregated"}`.

Silently accepting a fact that will never appear in any metric would be the worst option available.

## 6. Behaviour

1. Implement `pkg/lateness` per §5.1.
2. Add the two manifest fields, bump `SchemaVersion` to 2, and update the golden fixture.
3. Implement the §5.3 revision decision in `pkg/recompute/revision.go` and call it from `Run`.
4. In the rollup, when a fact's `AffectedDay` differs from the partition being processed, do not drop
   it and do not put it in the wrong bucket: collect it, and after the current partition completes,
   rebuild the affected day's partition too. Report every extra partition rebuilt.
5. Add the lateness counter to ingestion. Change no acceptance decision except adding the §5.5 note.
6. Implement §5.5 unprocessable handling.
7. Verify `RevisedAt` does not enter `ContentDigest`, so two runs with no data change produce
   identical digests and no revision bump.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Fact `event_time` before the retention window | accept, count `unprocessable`, DLQ copy, 202 with note | `event_time precedes the retention window; stored but not aggregated` |
| Fact `event_time` in the future beyond one rollup interval | accept, count `on_time`, log once per minute | `fact event_time is <d> in the future; check the sender's clock` |
| Manifest `schema_version` 1 read by a version-2 binary | read, treat missing fields as empty | none; forward compatibility is required |
| Manifest `schema_version` 2 read by a version-1 binary | refuse | `manifest: schema version 2 is newer than this binary supports (1)` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A late fact lands in its `event_time` bucket, not its arrival bucket | `TestLateArrivalUsesEventTime` |
| AC-2 | Rebuilding with new data increments `Revision` by exactly 1 | `TestLateArrivalRevision` |
| AC-3 | `PreviousDigest` holds the superseded digest | `TestRevisionRecordsPreviousDigest` |
| AC-4 | Rebuilding with no data change leaves `Revision` unchanged | `TestNoChangeNoRevisionBump` |
| AC-5 | The prior value is reproducible from facts as they were | `TestPriorRevisionReproducible` |
| AC-6 | `RevisedAt` is excluded from `ContentDigest` | `TestRevisedAtNotInDigest` |
| AC-7 | Each lateness class is classified at its boundary | `TestClassifyBoundaries` |
| AC-8 | `AffectedDay` depends only on `event_time` | `TestAffectedDayIgnoresArrival` |
| AC-9 | A cross-day late fact triggers a rebuild of the affected day | `TestCrossDayLateFactRebuildsAffectedDay` |
| AC-10 | An unprocessable fact is stored, DLQ'd, counted, and returns the 202 note | `TestUnprocessableFactStoredNotSilent` |
| AC-11 | The lateness counter has only bounded labels | `TestLatenessMetricCardinalityBounded` |
| AC-12 | A version-1 manifest is readable by a version-2 binary | `TestManifestForwardCompatible` |
| AC-13 | A version-2 manifest is refused by a version-1 binary | `TestManifestRejectsNewerSchema` |
| AC-14 | Ingestion accepts every fact it accepted before this change | `TestNoAcceptanceRegression` |

## 8. Verification

```bash
# 1. Event-time bucketing and revisions
go test ./pkg/lateness/... ./pkg/recompute/... -run 'TestLateArrival|TestRevision|TestNoChangeNoRevision|TestPriorRevision' -v
# expect: PASS

# 2. RevisedAt must not break determinism
go test ./pkg/recompute/... -run 'TestRevisedAtNotInDigest|TestRecomputeDeterminism' -v
# expect: PASS

# 3. Cross-day handling
go test ./transforms/request_metrics_minute/... -run TestCrossDayLateFact -v
# expect: PASS

# 4. Unprocessable facts are visible, never silent
go test ./services/ingestion/... -run TestUnprocessableFactStoredNotSilent -v
# expect: PASS

# 5. Cardinality guard
go test ./services/ingestion/... -run TestLatenessMetricCardinalityBounded -v
# expect: PASS

# 6. No acceptance regression
go test ./services/ingestion/... -v
# expect: PASS

# 7. Coverage and full suite
go test ./pkg/lateness/... -cover
go test ./... 2>&1 | tail -20
# expect: coverage >= 95%; no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Extra partitions rebuilt by cross-day facts reported per run
- [ ] `RevisedAt` proven absent from `ContentDigest`
- [ ] Golden manifest fixture updated for `SchemaVersion` 2
- [ ] `docs-engineer` delta merged; `docs/01-facts-and-events.md` documents lateness classes
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A late fact that cannot be routed to its day without editing an existing fact | Return `SPEC DEFECT: §6 step 4`. Facts are immutable; a design needing mutation is wrong. |
| The lateness counter would need an unbounded label | Return `SPEC DEFECT: §5.4 — <label> is unbounded`. Non-goal §5 is not negotiable for our own telemetry either. |
| Existing partitions with no manifest, so no revision baseline | Report `finding: <n> partitions lack a revision baseline` for GRVX-810. Treat them as `Revision 0` on first rebuild and say so. |
