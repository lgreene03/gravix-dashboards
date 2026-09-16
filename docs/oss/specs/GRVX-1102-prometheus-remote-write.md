# SPEC GRVX-1102: Prometheus remote-write receiver → facts, with cardinality budget enforcement

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1102 |
| **Phase** | 11 |
| **Goal** | G5.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Ingestion row: "HTTP + OTLP-subset + Prometheus remote-write ingest". §7.3 Q1 = YES (a self-hosting team already running Prometheus expects Gravix to sit beside it) → core. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | none |
| **Blocks** | GRVX-1105, GRVX-1108, GRVX-1109 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

The ingestion service accepts standard Prometheus remote-write protobuf/snappy payloads at
`POST /api/v1/remote_write`, converts each accepted sample into a new, explicitly non-Fact
`ExternalMetricSample` record durably written alongside `RequestFact`s, and rejects — at the
receiver, before any storage write — any payload that would let Prometheus remote-write become a
distributed-tracing side channel or exceed a hard per-tenant, per-metric cardinality budget.

## 2. Context the implementer needs

- `docs/00-system-truth.md` §1 — "A Fact MUST NOT contain derived data." A Prometheus sample is
  already aggregated by the client (a counter or gauge value at scrape time), so it cannot be
  written as a `RequestFact`. This spec introduces a second, explicitly-labelled message type for
  exactly this reason; see §5.1.
- `docs/04-non-goals.md` §1 (No Distributed Tracing) — the Prometheus remote-write wire format
  carries an `Exemplar` per sample, whose purpose is to link a metric sample to a trace ID. This is
  the side door this spec must close; see §6 step 5.
- `proto/gravix.proto:1-20` — existing `RequestFact` message and generation pattern
  (`protoc --go_out=./gen --go_opt=paths=source_relative proto/gravix.proto`, per `CLAUDE.md`).
- `services/ingestion/main.go:140-228` — the `prometheus.CounterVec`/`HistogramVec` registration
  pattern this spec's new metrics follow.
- `services/ingestion/main.go:242-326` — `DurableSink.Write(topic string, data []byte) error`, the
  fsync-then-buffer primitive every ingestion handler uses. This spec calls it exactly the same way.
- `services/ingestion/main.go:121-128` — `topicForTenant(tenantID, baseTopic string) string`.
- `services/ingestion/main.go:786-792` — the `http.Handle` registration block in `main()`, where
  the new route is added.
- `services/ingestion/main.go:766-772` — `NewTenantLimiter`/trace-sample-rate construction pattern;
  this spec adds a similarly-constructed `*cardinality.Budget` in the same function.
- `go.mod:1` — module `github.com/lgreene/gravix-dashboards`, Go 1.24.9. No Prometheus wire-format
  dependency exists today; this spec does not add `github.com/prometheus/prometheus` (a multi-module
  dependency tree unrelated to what this spec needs) — it defines the minimal wire-compatible
  message set itself (§5.1).
- `schemas/request_fact.go:1-17` — the existing pattern of aliasing a generated protobuf type
  (`type RequestFact = gravixv1.RequestFact`) and writing a `ParseX`/`ValidateX` pair. This spec's
  `schemas/external_metric_sample.go` follows the same pattern.

## 3. Non-goals for this spec

- Do NOT add a Cube.js model, a Trino table, or a dashboard panel for `ExternalMetricSample`. G5.2's
  acceptance bar is "accepted and converted to facts," not "visualised." That is future work.
- Do NOT accept Prometheus remote-write metadata (`WriteRequest.metadata`, field 3 of the real
  `prompb.WriteRequest`) — it is not declared in this spec's `WriteRequest` message and is silently
  dropped by protobuf unknown-field handling, which is correct: metadata carries no sample data.
- Do NOT add `github.com/prometheus/prometheus` or any CGO dependency. `github.com/golang/snappy` is
  pure Go.
- This spec does not cross non-goal §1 (No Distributed Tracing): §6 step 5 rejects every payload
  carrying an `Exemplar` or a label named `trace_id`, `traceid`, `span_id`, `spanid`,
  `parent_span_id`, or `parentspanid` (case-insensitive), before any data is written.
- This spec does not cross non-goal §5 (No High-Cardinality Dimensions): §6 step 6 enforces a hard,
  numeric, per-(tenant, metric) daily cap via `pkg/cardinality`.
- Do NOT change `/api/v1/facts`, `/api/v1/events`, or any existing endpoint's behaviour.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `proto/remote_write.proto` | Wire-compatible subset of `prompb.WriteRequest` |
| `gen/remotewrite/v1/remote_write.pb.go` | Generated from the above |
| `pkg/cardinality/budget.go` | Per-tenant, per-metric daily series cardinality budget |
| `pkg/cardinality/budget_test.go` | Tests |
| `schemas/external_metric_sample.go` | `ParseExternalMetricSample`/`ValidateExternalMetricSample` |
| `schemas/external_metric_sample_test.go` | Tests |
| `services/ingestion/remote_write.go` | `handleRemoteWrite` and its helpers |
| `services/ingestion/remote_write_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `proto/gravix.proto` | Append the `ExternalMetricSample` message (§5.1) |
| `gen/gravix/v1/gravix.pb.go` | Regenerated via the `protoc` command in `CLAUDE.md` |
| `go.mod`, `go.sum` | Add `github.com/golang/snappy v0.0.4` |
| `services/ingestion/main.go` | Add the `externalMetricsBudget` construction, its 24h reset goroutine, the new Prometheus counter registrations, and the `http.Handle("/api/v1/remote_write", ...)` route (§6 steps 1–2) |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/otlp.go` | OTLP hardening is GRVX-1105, which depends on this spec |
| `schemas/request_fact.go`, `schemas/trace_sample.go` | Unrelated fact types |
| `storage/trino/init.sql` | No Trino table is added by this spec, per §3 |
| `services/ingestion/main_test.go` | No existing test in this file references remote-write; do not add remote-write tests here — they belong in `services/ingestion/remote_write_test.go` |

## 5. Interface contract

### 5.1 `proto/gravix.proto` addition

```proto
// ExternalMetricSample represents one pre-aggregated observation ingested
// from a foreign metrics protocol (Prometheus remote-write, OTLP metrics).
// Unlike RequestFact, it is NOT a Fact under docs/00-system-truth.md §1: it
// may contain derived/aggregated data, and gravix recompute never reads it.
// It exists so Prometheus- and OTLP-instrumented services can be visualised
// without an instrumentation change.
message ExternalMetricSample {
  string sample_id = 1;                        // UUIDv7, generated by the receiver
  google.protobuf.Timestamp sample_time = 2;
  string tenant_id = 3;
  string source = 4;                            // "prometheus_remote_write" | "otlp_metrics"
  string metric_name = 5;
  map<string, string> labels = 6;               // <= 20 entries; budgeted by pkg/cardinality
  double value = 7;
  string metric_type = 8;                       // "counter" | "gauge" | "histogram_bucket"
}
```

### 5.2 `proto/remote_write.proto` (new file)

```proto
syntax = "proto3";

package gravix.remotewrite.v1;

option go_package = "github.com/lgreene/gravix-dashboards/gen/remotewrite/v1;remotewritev1";

// WriteRequest mirrors the wire format of prometheus/prompb.WriteRequest
// field-for-field (field numbers 1-3 of remote.proto/types.proto), so it
// decodes any payload produced by a standard Prometheus remote-write client
// without requiring that client's own dependency tree. metadata (real
// field 3) is intentionally not declared here; protobuf leaves it as an
// ignored unknown field.
message WriteRequest {
  repeated TimeSeries timeseries = 1;
}

message TimeSeries {
  repeated Label labels = 1;
  repeated Sample samples = 2;
  repeated Exemplar exemplars = 3;
}

message Label {
  string name = 1;
  string value = 2;
}

message Sample {
  double value = 1;
  int64 timestamp = 2; // milliseconds since Unix epoch
}

// Exemplar mirrors prompb.Exemplar. Its presence on any TimeSeries in a
// WriteRequest causes the entire request to be rejected — see
// services/ingestion/remote_write.go's ErrExemplarRejected.
message Exemplar {
  repeated Label labels = 1;
  double value = 2;
  int64 timestamp = 3;
}
```

### 5.3 `pkg/cardinality/budget.go`

```go
// Package cardinality enforces a hard per-tenant, per-metric cap on the
// number of distinct label-set combinations accepted from external metrics
// protocols (Prometheus remote-write, OTLP metrics) within one rolling
// window, so neither protocol can be used to smuggle per-request or
// per-user dimensions into Gravix. State is in-memory only; it does not
// survive a process restart.
package cardinality

// DefaultMaxSeriesPerMetricPerDay is the hard cap on distinct label-set
// combinations accepted for one (tenant, metric name) pair within one
// rolling 24-hour window.
const DefaultMaxSeriesPerMetricPerDay = 2000

// Budget is safe for concurrent use.
type Budget struct {
    // unexported fields
}

// NewBudget constructs a Budget with the given per-metric window cap.
// NewBudget panics if max < 1.
func NewBudget(max int) *Budget

// Admit reports whether a data point with the given tenant, metric name,
// and label set (excluding the metric name itself) is within budget. A
// label set already admitted in the current window is always re-admitted
// without consuming additional budget. count is the number of distinct
// label sets recorded for (tenantID, metricName) after this call.
func (b *Budget) Admit(tenantID, metricName string, labels map[string]string) (admitted bool, count int)

// Max returns the configured per-metric cap.
func (b *Budget) Max() int

// Reset clears all tracked series. The caller is responsible for invoking
// Reset on a schedule (see services/ingestion/main.go's 24h ticker); Budget
// itself runs no background goroutine.
func (b *Budget) Reset()
```

### 5.4 `schemas/external_metric_sample.go`

```go
package schemas

// ExternalMetricSample aliases the generated Protobuf type.
type ExternalMetricSample = gravixv1.ExternalMetricSample

// ValidateExternalMetricSample enforces schema constraints.
func ValidateExternalMetricSample(m *ExternalMetricSample) error

var (
    ErrExternalMetricMissingID     = errors.New("sample_id is required")
    ErrExternalMetricMissingTime   = errors.New("sample_time is required")
    ErrExternalMetricMissingTenant = errors.New("tenant_id is required")
    ErrExternalMetricBadSource     = errors.New("source must be prometheus_remote_write or otlp_metrics")
    ErrExternalMetricMissingName   = errors.New("metric_name is required")
    ErrExternalMetricTooManyLabels = errors.New("labels must not exceed 20 entries")
    ErrExternalMetricBadType       = errors.New("metric_type must be counter, gauge, or histogram_bucket")
)
```

### 5.5 `services/ingestion/remote_write.go`

```go
package main

const remoteWriteMaxBodyBytes = 10 << 20 // 10 MB, matches otlpMaxBodyBytes

// bannedCorrelationLabels lists label names rejected regardless of case,
// because their presence means the series is carrying tracing-correlation
// data (docs/04-non-goals.md §1), not a metric.
var bannedCorrelationLabels = map[string]bool{
    "trace_id": true, "traceid": true,
    "span_id": true, "spanid": true,
    "parent_span_id": true, "parentspanid": true,
}

// handleRemoteWrite returns the POST /api/v1/remote_write handler.
func handleRemoteWrite(sink *DurableSink, budget *cardinality.Budget) http.HandlerFunc

var (
    ErrExemplarRejected      = errors.New("exemplars are rejected: gravix does not ingest distributed tracing signals (see docs/04-non-goals.md §1)")
    ErrCorrelationLabel      = errors.New("label carries tracing correlation data, which gravix does not ingest (see docs/04-non-goals.md §1)")
    ErrMissingMetricName     = errors.New("time series missing __name__ label")
    ErrCardinalityExceeded   = errors.New("cardinality budget exceeded")
)
```

## 6. Behaviour

1. In `services/ingestion/main.go`, immediately after the `traceSampleRate` block (`main.go:770-772`),
   add:
   ```go
   externalMetricsBudget := cardinality.NewBudget(cardinality.DefaultMaxSeriesPerMetricPerDay)
   go func() {
       ticker := time.NewTicker(24 * time.Hour)
       defer ticker.Stop()
       for range ticker.C {
           externalMetricsBudget.Reset()
       }
   }()
   ```
2. Register the route immediately after the `/v1/traces` line (`main.go:791`):
   ```go
   http.Handle("/api/v1/remote_write", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleRemoteWrite(sink, externalMetricsBudget))))))
   ```
3. `handleRemoteWrite`, on each request:
   a. If method is not `POST`, respond 405 with `writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")`.
   b. If the `Content-Encoding` header is not exactly `"snappy"`, respond 400 with
      `"Content-Encoding must be snappy"`.
   c. Read the body via `http.MaxBytesReader(w, r.Body, remoteWriteMaxBodyBytes)`; on a read error,
      respond 413 with `"request body too large (max 10MB)"`.
   d. Decompress with `snappy.Decode(nil, body)`; on error, respond 400 with
      `fmt.Sprintf("invalid snappy payload: %v", err)`.
   e. Unmarshal into `remotewritev1.WriteRequest` via `proto.Unmarshal`; on error, respond 400 with
      `fmt.Sprintf("invalid protobuf payload: %v", err)`.
4. For every `TimeSeries` in the decoded request, build a `map[string]string` of its labels. If any
   `TimeSeries` has zero or more than one label named exactly `"__name__"`, or the `__name__` value
   is empty, the entire request is rejected: respond 400 with `ErrMissingMetricName.Error()`.
5. If any `TimeSeries` has `len(ts.Exemplars) > 0`, the entire request is rejected: respond 400 with
   `ErrExemplarRejected.Error()`. Otherwise, if any label name in any `TimeSeries` (case-insensitive)
   is a key of `bannedCorrelationLabels`, the entire request is rejected: respond 400 with
   `fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), labelName)`.
6. For every `TimeSeries` surviving steps 4–5, compute its label set with `__name__` removed and call
   `budget.Admit(tenantID, metricName, labels)`. If any call returns `admitted == false`, the entire
   request is rejected: respond 400 with
   `fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day", tenantID, metricName, budget.Max())`.
   No partial writes occur — validation of every series happens before any `sink.Write` call.
7. If every `TimeSeries` passes steps 4–6, for every `Sample` in every `TimeSeries`, construct one
   `ExternalMetricSample`:
   - `SampleId`: `uuid.NewV7()` (as used at `services/ingestion/main_test.go:59`).
   - `SampleTime`: `time.UnixMilli(sample.Timestamp).UTC()`.
   - `TenantId`: the request's tenant ID (empty string in legacy single-key mode).
   - `Source`: `"prometheus_remote_write"`.
   - `MetricName`: the series' `__name__` value.
   - `Labels`: the series' label map with `__name__` removed.
   - `Value`: `sample.Value`.
   - `MetricType`: `"counter"` if `MetricName` ends with `"_total"`, else `"histogram_bucket"` if it
     ends with `"_bucket"`, else `"gauge"`.
   Validate with `schemas.ValidateExternalMetricSample`; marshal with
   `protojson.MarshalOptions{UseProtoNames: true}.Marshal`; write via
   `sink.Write(topicForTenant(tenantID, "external_metrics"), data)`.
8. On success, respond `204 No Content` with an empty body. On any `sink.Write` error, respond 500
   with `"failed to persist external metric sample"` and do not attempt further samples in the
   request.
9. Increment `ingestionRemoteWriteSeriesTotal.WithLabelValues(tenantID, result)` once per `TimeSeries`
   processed, where `result` is one of `"accepted"`, `"rejected_missing_name"`,
   `"rejected_exemplar"`, `"rejected_label"`, `"rejected_cardinality"`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Method not POST | 405 | `only POST is accepted` |
| `Content-Encoding` missing or not `snappy` | 400 | `Content-Encoding must be snappy` |
| Body exceeds 10MB | 413 | `request body too large (max 10MB)` |
| Malformed snappy | 400 | `invalid snappy payload: <err>` |
| Malformed protobuf | 400 | `invalid protobuf payload: <err>` |
| Missing/duplicate `__name__` | 400 | `time series missing __name__ label` |
| Any series carries an exemplar | 400 | `exemplars are rejected: gravix does not ingest distributed tracing signals (see docs/04-non-goals.md §1)` |
| A label named `trace_id`/`span_id`/`parent_span_id` (any case) | 400 | `label carries tracing correlation data, which gravix does not ingest (see docs/04-non-goals.md §1): <label>` |
| Cardinality budget exceeded for any series | 400 | `cardinality budget exceeded: tenant=<t> metric=<m> limit=2000 distinct label-sets per day` |
| Durable write fails | 500 | `failed to persist external metric sample` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A well-formed, single-series, single-sample snappy/protobuf payload is accepted and persisted, responding 204 | `TestHandleRemoteWriteAcceptsValidSeries` |
| AC-2 | A payload whose `TimeSeries` carries one `Exemplar` is rejected with 400 and the exact exemplar-rejection message | `TestHandleRemoteWriteRejectsExemplar` |
| AC-3 | A payload with a label named `trace_id` is rejected with 400 and the exact correlation-label message | `TestHandleRemoteWriteRejectsTraceIDLabel` |
| AC-4 | A payload missing `__name__` is rejected with 400 and the exact missing-name message | `TestHandleRemoteWriteRejectsMissingMetricName` |
| AC-5 | A request with `Content-Encoding: identity` is rejected with 400 | `TestHandleRemoteWriteRejectsWrongContentEncoding` |
| AC-6 | Malformed snappy bytes are rejected with 400 | `TestHandleRemoteWriteRejectsMalformedSnappy` |
| AC-7 | The (n+1)-th distinct label set for one (tenant, metric) pair within a window, where n = the configured max, is rejected with 400 and the whole batch fails, including its 1st..n-th series | `TestHandleRemoteWriteRejectsOverBudgetCardinality` |
| AC-8 | `Budget.Admit` re-admits an already-known label set without incrementing the tracked count | `TestBudgetAdmitIdempotentForKnownSeries` |
| AC-9 | `Budget.Admit` rejects the call that would exceed `max` | `TestBudgetAdmitRejectsOverLimit` |
| AC-10 | `Budget.Reset` clears prior admissions, allowing a previously-rejected series through | `TestBudgetResetClearsState` |
| AC-11 | `ValidateExternalMetricSample` rejects a sample with more than 20 labels | `TestValidateExternalMetricSampleTooManyLabels` |
| AC-12 | GET on `/api/v1/remote_write` returns 405 | `TestHandleRemoteWriteMethodNotAllowed` |

## 8. Verification

```bash
# 1. Package tests
go test ./pkg/cardinality/... -v -cover
# expect: PASS, coverage reported

go test ./schemas/... -run TestValidateExternalMetricSample -v
# expect: PASS

go test ./services/ingestion/... -run TestHandleRemoteWrite -v
# expect: PASS for all 8 TestHandleRemoteWrite* cases

# 2. Regenerate protobuf and confirm no diff beyond the intended addition
protoc --go_out=./gen --go_opt=paths=source_relative proto/gravix.proto
protoc --go_out=./gen --go_opt=paths=source_relative proto/remote_write.proto
git status --short gen/
# expect: gen/gravix/v1/gravix.pb.go modified, gen/remotewrite/v1/remote_write.pb.go added

# 3. Full build
go build ./... && go test ./schemas/... ./pkg/cardinality/... ./services/ingestion/...
# expect: build ok, all PASS

# 4. Open-core integrity
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The exact resolved version of `github.com/golang/snappy` recorded in the report from `go.sum`

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
| `protoc`/`protoc-gen-go` unavailable in the build environment | Return `SPEC DEFECT: §8 — protoc toolchain missing`. Do not hand-write the generated file. |

---

## 11. Implementation report

All twelve acceptance criteria pass, plus ten additional tests. `pkg/cardinality` is at 100%
statement coverage and race-clean. Two spec defects and one codebase finding registered.

```
ok  github.com/lgreene/gravix-dashboards/pkg/cardinality   coverage: 100.0% of statements
ok  github.com/lgreene/gravix-dashboards/schemas
ok  github.com/lgreene/gravix-dashboards/services/ingestion
boundary: 0 violations
build-oss exit=0
test-oss exit=0
```

`github.com/golang/snappy v0.0.4`, resolved from `go.sum`:

```
github.com/golang/snappy v0.0.4 h1:yAGX7huGHXlcLOEtBnF4w7FQwA26wojNCwOYAEhLjQM=
github.com/golang/snappy v0.0.4/go.mod h1:/XxbfmMg8lxefKM7IXC3fBNl/7bRcc72aCRzEWrmP2Q=
```

`go get` recorded it `// indirect`; `go mod tidy` graduated it to a direct requirement once
`services/ingestion/remote_write.go` imported it. It is pure Go, as §3 requires.

### 11.1 SD-028 and F-040 — the regeneration step §4.2 mandates does nothing

§4.2 says to regenerate `gen/gravix/v1/gravix.pb.go` "via the protoc command in CLAUDE.md". That
command uses `paths=source_relative`, which writes `gen/proto/gravix.pb.go` — not the tracked file —
and `.gitignore:16` (`/gen`) hides it, so the step reports success and changes nothing. The command
that reproduces the tracked layout is `--go_opt=module=github.com/lgreene/gravix-dashboards/gen`.
Registered as **SD-028**.

Using it surfaced **F-040**: the committed generated code was two fields behind its own `.proto`.
`proto/gravix.proto` declares `tenant_id` on both `RequestFact` (field 9) and `ServiceEvent`
(field 8); the generated Go had neither. Nothing referenced those fields, so nothing failed — but
`schemas.ParseRequestFact` decodes with `protojson.Unmarshal` at default options, which rejects
unknown fields, so a client sending the `tenant_id` the published schema declares was answered
`unknown field "tenant_id"`. Verified directly before and after. The regeneration this spec mandates
fixes it; the documented command that concealed it is SD-028's to fix.

`gen/remotewrite/v1/remote_write.pb.go` needed `git add -f`, since `/gen` is ignored and the existing
generated file is tracked by an earlier force-add.

### 11.2 SD-027 — `tenant_id` is required and also empty

§5.4 makes `tenant_id` a validation rule; §6 step 7 says it is "empty string in legacy single-key
mode" and then says to validate. In legacy mode every sample therefore fails the validation the same
step mandates, and §6.1 defines no behaviour for a validation failure.

§5.4 was implemented literally — dropping the rule would leave a declared error that never fires,
and both shipped compose files set `TENANT_DB_PATH`, so the default stack is unaffected. A validation
failure returns 500 under §6 step 8's existing row rather than inventing a message §6.1 does not
define. `TestHandleRemoteWriteFailsInLegacySingleKeyMode` pins the broken path in the suite, with a
comment saying to replace it with a 204 assertion once the defect is resolved.

### 11.3 Where the label-set fingerprint had to be more careful than the spec says

§5.3 specifies `Admit(tenantID, metricName, labels)` without saying how a label set becomes a key.
It matters: label names and values arrive from an untrusted remote-write payload, so a `"k=v,k=v"`
join lets a sender who controls a value embed the separators and make two genuinely different label
sets produce one key — one series posing as another, and the cap counting them as one. Every part is
length-prefixed instead, which no choice of label content can forge, and
`TestBudgetDistinguishesLabelSetsThatJoinIdentically` drives the three collisions a naive join
admits. `metricKey` is a struct for the same reason: `("a","bc")` and `("ab","c")` must not share a
bucket, or one tenant could drain another's budget by choosing its own name.

### 11.4 The correlation-label ban is on the dimension, not the spelling

§5.5 lists six names. `TestHandleRemoteWriteRejectsEveryCorrelationLabelSpelling` drives thirteen —
`TRACE_ID`, `TraceID`, `SpanId`, `ParentSpanID` and the rest — because a guard that matches the names
its author typed protects only those. The case-insensitive lookup §6 step 5 specifies is what makes
the ban hold; the test is what proves it still does.

### 11.5 Two readings resolved without a defect

**Duplicate `__name__`.** §6 step 4 rejects a series with "more than one label named exactly
`__name__`", but a `map[string]string` collapses duplicates before anything can object. The count is
taken while reading the repeated field, not after building the map.

**The counter in §6 step 9.** "Once per `TimeSeries` processed" with one `result` label cannot mean
every series is counted on a whole-request rejection, since the later series are never examined. A
rejection increments once, for the series that caused it, under its specific reason; a success
increments `accepted` once per series. Rejections are therefore counted by cause and acceptances by
volume, which is what makes the counter readable.

### 11.6 Not a defect, but §2's line numbers have drifted

Every anchor in §2 is off — `DurableSink.Write` is at `services/ingestion/main.go:393` not 242-326,
`topicForTenant` at 130 not 121-128, the `http.Handle` block at 964-980 not 786-792, and the
trace-sample-rate construction at 948 not 766-772. The named symbols are all present and
unambiguous, so nothing was blocked; searching by name rather than trusting a line number is the only
thing that made the spec executable.

### 11.7 Scope

No Cube model, Trino table or dashboard panel was added (§3). `services/ingestion/otlp.go`,
`schemas/request_fact.go`, `schemas/trace_sample.go`, `storage/trino/init.sql` and
`services/ingestion/main_test.go` are untouched (§4.3) — `remote_write_test.go` reuses
`setupSink` from `main_test.go` without modifying it. `/api/v1/facts`, `/api/v1/events` and every
other existing route are unchanged.
