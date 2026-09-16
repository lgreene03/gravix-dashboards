# SPEC GRVX-1105: OTLP metrics-subset hardening — reject traces and logs at the receiver, accept a cardinality-budgeted metrics subset

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1105 |
| **Phase** | 11 |
| **Goal** | G5.1 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Ingestion row: "HTTP + OTLP-subset + Prometheus remote-write ingest". §7.3 Q1 = YES (a team already running an OTel Collector expects Gravix to accept its metrics pipeline output without an SDK change) → core. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-1102 |
| **Blocks** | GRVX-1109 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

`POST /v1/metrics` accepts a simplified OTLP/HTTP JSON `ExportMetricsServiceRequest`, converts every
gauge and sum (counter) numeric data point into an `ExternalMetricSample`, and enforces the same
per-tenant, per-metric cardinality budget already enforced on Prometheus remote-write. `POST
/v1/traces` and the new `POST /v1/logs` reject every request outright, before parsing the body, with
a message citing the specific non-goal each protocol would otherwise violate. The existing
`handleOTLPTraces` span-to-fact conversion — which silently accepts OTLP trace export requests — is
deleted.

## 2. Context the implementer needs

- `services/ingestion/otlp.go:1-273` is the entire current file. It implements OTLP **trace**
  ingestion only:
  - `otlp.go:13-63` — `OTLPTraceRequest`, `ResourceSpan`, `ScopeSpan`, `OTLPSpan`, `OTLPStatus`:
    trace-specific types. **Delete.**
  - `otlp.go:24-27` — `OTLPResource` and `otlp.go:47-51` `OTLPAttribute` /
    `otlp.go:53-57` `OTLPAttrValue`: generic OTLP types, not trace-specific. **Keep, reused by this
    spec's metrics types.**
  - `otlp.go:65` — `const otlpMaxBodyBytes = 10 << 20`. **Keep, reused unchanged.**
  - `otlp.go:69-141` — `handleOTLPTraces`, registered at `/v1/traces`. Parses OTLP JSON, walks
    every span, and for every span with an `http.method`/`http.request.method` attribute, converts
    it into a `RequestFact`-shaped map and writes it to the durable sink. **Delete the function
    body**; §5 replaces it with a rejecting handler of the same route.
  - `otlp.go:145-202` — `spanToFact`. **Delete** (only caller is the deleted `handleOTLPTraces`).
  - `otlp.go:204-212` — `extractResourceAttr(res OTLPResource, key string) string`. **Keep**, reused
    to read `service.name` from a metrics request's resource.
  - `otlp.go:215-222`, `224-241`, `244-248` — `getSpanAttr`, `getSpanAttrInt`, `parseNano`.
    **Delete** (trace-span-specific, no other caller — verified: `grep -rn "getSpanAttr\|parseNano"
    --include=*.go .` outside `otlp.go` returns nothing).
  - `otlp.go:251-273` — `simplifyUserAgent`. **Delete** (only caller was `spanToFact`; verified no
    other caller in the repository).
- `sdk/go/otel/exporter.go:1-96` is the **sanctioned** path for OpenTelemetry users: it implements
  `sdktrace.SpanExporter`, converts HTTP spans to `gravix.RequestFact` **client-side**, and sends
  them through the normal Gravix SDK to `/api/v1/facts`. No trace ID, span ID, or parent span ID
  ever leaves the client process. This is why the server-side OTLP trace receiver is deleted rather
  than hardened: the legitimate use case it served is already served, correctly, elsewhere.
- `services/ingestion/main.go:83-88` — `getTenantID(r *http.Request) string`.
- `services/ingestion/main.go:131-138` — `writeErrorJSON(w http.ResponseWriter, code int, errMsg
  string)`. Writes `{"error": "<errMsg>", "code": <code>}` with `Content-Type: application/json`.
  Every rejection in this spec uses this function; that JSON shape is therefore this spec's exact
  response body for every failure mode.
- `services/ingestion/main.go:766-791` — the handler-registration block in `main()`:
  ```go
  trl := ratelimit.NewTenantLimiter(100, 200)
  defer trl.Close()
  traceSampleRate := getTraceSampleRate()
  slog.Info("trace sampling configured", "rate", traceSampleRate)
  bufferCheck := func(next http.HandlerFunc) http.HandlerFunc { ... }
  http.Handle("/api/v1/facts", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleFacts(sink, tdb))))))
  ...
  http.Handle("/api/v1/traces", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("traces:write", handleTraces(sink, tdb, traceSampleRate))))))
  http.Handle("/v1/traces", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("traces:write", handleOTLPTraces(sink))))))
  http.Handle("/api/v1/deploy", authMW(tenantRateLimitMiddleware(trl, bufferCheck(handleDeployWebhook(sink, tdb)))))
  ```
  `/api/v1/traces` (`handleTraces`, `main.go:599`) is a **separate, pre-existing, Gravix-native**
  low-volume trace-sampling feature (`schemas.ParseTraceSample`, topic `trace_samples`, default 1%
  sample rate). It is unrelated to OTLP and out of scope for this spec — see §4.3.
- GRVX-1102 (dependency, already specced) adds, in `services/ingestion/remote_write.go` (package
  `main`, the same package as `otlp.go`):
  - `bannedCorrelationLabels map[string]bool` — `trace_id`, `traceid`, `span_id`, `spanid`,
    `parent_span_id`, `parentspanid` (case-insensitive keys already lower-cased).
  - `var ErrExemplarRejected = errors.New("exemplars are rejected: gravix does not ingest distributed tracing signals (see docs/04-non-goals.md §1)")`
  - `var ErrCorrelationLabel = errors.New("label carries tracing correlation data, which gravix does not ingest (see docs/04-non-goals.md §1)")`
  - These are package-level identifiers in `package main`; this spec's file, also `package main`,
    references them directly. **Do not duplicate them.**
  - `pkg/cardinality/budget.go` — `type Budget struct{...}`, `func NewBudget(max int) *Budget`,
    `func (b *Budget) Admit(tenantID, metricName string, labels map[string]string) (admitted bool, count int)`,
    `func (b *Budget) Max() int`.
  - `main.go` (after GRVX-1102 merges) constructs, immediately after the `traceSampleRate` block
    shown above:
    ```go
    externalMetricsBudget := cardinality.NewBudget(cardinality.DefaultMaxSeriesPerMetricPerDay)
    go func() { ... 24h Reset ticker ... }()
    ```
    and registers `POST /api/v1/remote_write` using this same `externalMetricsBudget` instance. This
    spec's `/v1/metrics` handler is constructed with, and shares, that **same instance** — one
    per-(tenant, metric) cardinality ledger for both protocols, so switching wire formats cannot be
    used to double a tenant's effective budget.
  - `proto/gravix.proto`'s `ExternalMetricSample` message (`schemas/external_metric_sample.go`'s
    `ExternalMetricSample` alias) already documents `source` as
    `"prometheus_remote_write" | "otlp_metrics"` and `ValidateExternalMetricSample` already accepts
    `"otlp_metrics"` — this spec is the first caller to use that value.
  - `schemas.ValidateExternalMetricSample(m *ExternalMetricSample) error` rejects more than 20
    labels (`ErrExternalMetricTooManyLabels`).
- `docs/04-non-goals.md` §1 (No Distributed Tracing) and §2 (No Log Aggregation) — the exact rules
  this spec enforces at the receiver.
- `go.mod:1` — module `github.com/lgreene/gravix-dashboards`, Go 1.24.9. This spec adds no new
  dependency.

## 3. Non-goals for this spec

- Do NOT touch `/api/v1/traces` (`handleTraces`, `main.go:599`) or `schemas/trace_sample.go`. That
  is a distinct, pre-existing, Gravix-native feature, not an OTLP endpoint.
- Do NOT add OTLP protobuf/gRPC support. This subset is OTLP/HTTP JSON only, matching the existing
  hand-rolled JSON approach already used for traces (now deleted) — no
  `go.opentelemetry.io/proto/otlp` dependency is added.
- Do NOT convert OTLP `Histogram`, `ExponentialHistogram`, or `Summary` metric points. This subset
  covers `Gauge` and `Sum` only, matching the value types Prometheus remote-write (GRVX-1102) already
  covers. A histogram/summary point is rejected with a clear message (§6.1), not silently dropped.
- Do NOT merge arbitrary OTLP resource attributes into `ExternalMetricSample.Labels`. Only
  `service.name`, under the fixed key `service_name`, is carried over. Correlating external metrics
  to richer resource metadata is future work, not part of G5.1's acceptance bar (G5.1 target: "OTLP
  metrics-subset ingestion accepted without an SDK change").
- Do NOT add a Cube.js model, a Trino table, or a dashboard panel for OTLP-sourced samples. Same
  scope boundary GRVX-1102 already drew for remote-write.
- This spec does not cross non-goal §1 (No Distributed Tracing): `/v1/traces` rejects every request
  without inspecting the body (§6 step "trace handler"), and the metrics subset itself rejects any
  data point carrying an `exemplar` (OTLP's trace-correlation mechanism on a metric sample) or a
  `trace_id`/`span_id`/`parent_span_id` attribute, reusing `ErrExemplarRejected` and
  `ErrCorrelationLabel` from GRVX-1102 unmodified.
- This spec does not cross non-goal §2 (No Log Aggregation): `/v1/logs` rejects every request
  without inspecting the body. No log payload is ever parsed, stored, or forwarded.
- This spec does not cross non-goal §5 (No High-Cardinality Dimensions): every accepted metrics
  request is checked against `externalMetricsBudget`, the same hard numeric cap GRVX-1102 enforces.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `services/ingestion/otlp_test.go` | Tests for this spec (no test file exists for `otlp.go` today) |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/ingestion/otlp.go` | Delete trace-conversion code (§2); add OTLP metrics JSON types, `handleOTLPMetrics`, `handleOTLPTracesRejected`, `handleOTLPLogsRejected`, and their error vars (§5) |
| `services/ingestion/main.go` | Change the `/v1/traces` registration to use `handleOTLPTracesRejected`; add `/v1/metrics` and `/v1/logs` registrations (§6 step 1) |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/remote_write.go` | GRVX-1102's file. This spec reuses its exported-within-package identifiers unmodified; it does not edit them. |
| `pkg/cardinality/**` | Reused unmodified. |
| `services/ingestion/main.go:599-660` (`handleTraces`) and `schemas/trace_sample.go` | The unrelated native trace-sampling feature — see §3. |
| `sdk/go/otel/exporter.go` | The sanctioned client-side conversion path; unaffected by this spec. |
| `proto/gravix.proto`, `schemas/external_metric_sample.go` | `ExternalMetricSample` and its validation already support this spec's needs (GRVX-1102); no schema change required. |

## 5. Interface contract

### 5.1 `services/ingestion/otlp.go` — kept and new types

```go
package main

// otlpMaxBodyBytes is unchanged from the current file (10 << 20).

// OTLPResource, OTLPAttribute, OTLPAttrValue are unchanged from the current file.

// OTLPMetricsRequest is a simplified OTLP/HTTP JSON ExportMetricsServiceRequest,
// covering only the fields this subset reads.
type OTLPMetricsRequest struct {
	ResourceMetrics []OTLPResourceMetrics `json:"resourceMetrics"`
}

type OTLPResourceMetrics struct {
	Resource     OTLPResource       `json:"resource"`
	ScopeMetrics []OTLPScopeMetrics `json:"scopeMetrics"`
}

type OTLPScopeMetrics struct {
	Metrics []OTLPMetric `json:"metrics"`
}

// OTLPMetric holds one metric's data points. Per the OTLP spec, at most one
// of Gauge, Sum, Histogram, ExponentialHistogram, Summary is populated. This
// subset converts Gauge and Sum only; the other three are detected (as raw,
// unparsed JSON) only to be rejected with ErrOTLPMetricUnsupportedType.
type OTLPMetric struct {
	Name                 string          `json:"name"`
	Gauge                *OTLPGaugeOrSum `json:"gauge,omitempty"`
	Sum                  *OTLPGaugeOrSum `json:"sum,omitempty"`
	Histogram            json.RawMessage `json:"histogram,omitempty"`
	ExponentialHistogram json.RawMessage `json:"exponentialHistogram,omitempty"`
	Summary              json.RawMessage `json:"summary,omitempty"`
}

type OTLPGaugeOrSum struct {
	DataPoints []OTLPNumberDataPoint `json:"dataPoints"`
}

// OTLPNumberDataPoint mirrors OTLP's NumberDataPoint. TimeUnixNano and AsInt
// are strings because OTLP/JSON encodes 64-bit integers as decimal strings.
type OTLPNumberDataPoint struct {
	Attributes   []OTLPAttribute `json:"attributes"`
	TimeUnixNano string          `json:"timeUnixNano"`
	AsDouble     *float64        `json:"asDouble,omitempty"`
	AsInt        string          `json:"asInt,omitempty"`
	Exemplars    []OTLPExemplar  `json:"exemplars,omitempty"`
}

// OTLPExemplar mirrors OTLP's Exemplar. Its presence on any data point
// causes the entire request to be rejected with ErrExemplarRejected
// (services/ingestion/remote_write.go, GRVX-1102) — an exemplar links a
// metric sample to a trace/span, which is exactly the correlation this
// project does not ingest.
type OTLPExemplar struct {
	TraceID string `json:"traceId,omitempty"`
	SpanID  string `json:"spanId,omitempty"`
}

// handleOTLPMetrics returns the POST /v1/metrics handler. budget must be the
// same *cardinality.Budget instance constructed for
// POST /api/v1/remote_write (GRVX-1102), so Prometheus and OTLP metrics
// share one per-(tenant, metric) cardinality ledger.
func handleOTLPMetrics(sink *DurableSink, budget *cardinality.Budget) http.HandlerFunc

// handleOTLPTracesRejected is the POST /v1/traces handler. It rejects every
// request, of every HTTP method, without reading the request body: gravix
// does not ingest distributed tracing signals, in any wire format, at any
// endpoint.
func handleOTLPTracesRejected(w http.ResponseWriter, r *http.Request)

// handleOTLPLogsRejected is the POST /v1/logs handler. It rejects every
// request, of every HTTP method, without reading the request body: gravix
// does not ingest log aggregation signals, in any wire format, at any
// endpoint.
func handleOTLPLogsRejected(w http.ResponseWriter, r *http.Request)

var (
	ErrOTLPTracesRejected        = errors.New("gravix does not ingest OTLP trace signals: distributed tracing is not supported (see docs/04-non-goals.md §1)")
	ErrOTLPLogsRejected          = errors.New("gravix does not ingest OTLP log signals: log aggregation is not supported (see docs/04-non-goals.md §2)")
	ErrOTLPMetricMissingName     = errors.New("metric missing name")
	ErrOTLPMetricUnsupportedType = errors.New("histogram, exponential_histogram, and summary points are not supported in the OTLP metrics subset; only gauge and sum are accepted")
	ErrOTLPMetricMissingValue    = errors.New("data point missing asDouble/asInt, or has an invalid or missing timeUnixNano")

	ingestionOTLPMetricPointsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_otlp_metric_points_total",
			Help: "Total OTLP metrics data points processed, by outcome.",
		},
		[]string{"tenant", "result"}, // result: accepted | rejected_<reason>
	)
)

func init() {
	prometheus.MustRegister(ingestionOTLPMetricPointsTotal)
}
```

### 5.2 `services/ingestion/main.go` — registration block

```go
http.Handle("/api/v1/traces", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("traces:write", handleTraces(sink, tdb, traceSampleRate))))))
http.Handle("/v1/traces", authMW(tenantRateLimitMiddleware(trl, requireScope("traces:write", handleOTLPTracesRejected))))
http.Handle("/v1/logs", authMW(tenantRateLimitMiddleware(trl, requireScope("traces:write", handleOTLPLogsRejected))))
http.Handle("/v1/metrics", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleOTLPMetrics(sink, externalMetricsBudget))))))
http.Handle("/api/v1/deploy", authMW(tenantRateLimitMiddleware(trl, bufferCheck(handleDeployWebhook(sink, tdb)))))
```

`bufferCheck` is dropped from `/v1/traces` and `/v1/logs`: neither handler ever writes to `sink`, so
buffer fullness is irrelevant to them, and applying it would make an unrelated subsystem's state
(the durable sink's disk buffer) affect an endpoint that never touches it. `/v1/metrics` keeps
`bufferCheck` because `handleOTLPMetrics` does write to `sink`, identically to `/api/v1/remote_write`.

## 6. Behaviour

1. In `services/ingestion/otlp.go`, delete the code listed in §2 as "Delete", keeping the code listed
   as "Keep". Remove now-unused imports (at minimum `strings`, used only by the deleted
   `simplifyUserAgent` and `spanToFact`; verify with `go build` after the edit and remove any other
   import `go vet` flags as unused).
2. Add the types and error vars from §5.1.
3. `handleOTLPTracesRejected` and `handleOTLPLogsRejected` are each exactly:
   ```go
   func handleOTLPTracesRejected(w http.ResponseWriter, r *http.Request) {
       ingestionRequestsTotal.WithLabelValues("/v1/traces", "400", getTenantID(r)).Inc()
       writeErrorJSON(w, http.StatusBadRequest, ErrOTLPTracesRejected.Error())
   }
   ```
   (substituting `"/v1/logs"` and `ErrOTLPLogsRejected` for the logs variant). Neither reads
   `r.Body`. Both respond regardless of `r.Method`.
4. `handleOTLPMetrics`, on each request:
   a. If `r.Method != http.MethodPost`, respond 405 via `writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")`.
   b. Read the body via `http.MaxBytesReader(w, r.Body, otlpMaxBodyBytes)`; on a read error, respond
      413 with `"request body too large (max 10MB)"`.
   c. `json.Unmarshal` into `OTLPMetricsRequest`; on error, respond 400 with
      `"invalid JSON: " + err.Error()`.
   d. `tenantID := getTenantID(r)`.
5. Validate the whole request before writing anything (whole-request atomicity, matching GRVX-1102).
   For every `OTLPResourceMetrics` (`rm`) → every `OTLPScopeMetrics` → every `OTLPMetric` (`m`):
   a. If `m.Name == ""`, reject: respond 400 with `ErrOTLPMetricMissingName.Error()`.
   b. If `len(m.Histogram) > 0 || len(m.ExponentialHistogram) > 0 || len(m.Summary) > 0`, reject:
      respond 400 with `fmt.Sprintf("metric %q: %s", m.Name, ErrOTLPMetricUnsupportedType.Error())`.
   c. If `m.Gauge == nil && m.Sum == nil`, skip this metric (no data to convert; not an error) and
      continue to the next metric.
   d. Let `points = m.Gauge.DataPoints` and `metricType = "gauge"` if `m.Gauge != nil`, else
      `points = m.Sum.DataPoints` and `metricType = "counter"`.
   e. For every point `p` in `points`:
      - If `len(p.Exemplars) > 0`, reject: respond 400 with `ErrExemplarRejected.Error()`.
      - Build `labels := map[string]string{}`. For every `a := range p.Attributes`, if `a.Key ==
        ""`, skip; else set `labels[a.Key] = a.Value.StringValue` if non-empty, else
        `labels[a.Key] = a.Value.IntValue`.
      - If `extractResourceAttr(rm.Resource, "service.name")` returns a non-empty string `sn`, set
        `labels["service_name"] = sn` (overwriting any data-point attribute literally named
        `service_name`).
      - For every key `k` in `labels`, if `bannedCorrelationLabels[strings.ToLower(k)]` is true,
        reject: respond 400 with `fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), k)`.
      - If `p.AsDouble == nil && p.AsInt == ""`, reject: respond 400 with
        `fmt.Sprintf("metric %q: %s", m.Name, ErrOTLPMetricMissingValue.Error())`.
      - Parse `nanos, err := strconv.ParseUint(p.TimeUnixNano, 10, 64)`; if `err != nil`, reject:
        respond 400 with the same `ErrOTLPMetricMissingValue` message as above.
      - Call `admitted, _ := budget.Admit(tenantID, m.Name, labels)`. If `!admitted`, reject: respond
        400 with
        `fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day", tenantID, m.Name, budget.Max())`.
6. If every metric and every data point passes step 5 with no rejection, for every surviving data
   point construct one `ExternalMetricSample`:
   - `SampleId`: `uuid.NewV7().String()`.
   - `SampleTime`: `time.Unix(0, int64(nanos)).UTC()`.
   - `TenantId`: `tenantID`.
   - `Source`: `"otlp_metrics"`.
   - `MetricName`: `m.Name`.
   - `Labels`: the `labels` map built in step 5e.
   - `Value`: `*p.AsDouble` if `p.AsDouble != nil`, else `float64(v)` where
     `v, _ := strconv.ParseInt(p.AsInt, 10, 64)`.
   - `MetricType`: `metricType` from step 5d.
   Validate with `schemas.ValidateExternalMetricSample`; marshal with
   `protojson.MarshalOptions{UseProtoNames: true}.Marshal`; write via
   `sink.Write(topicForTenant(tenantID, "external_metrics"), data)` — the same topic
   `/api/v1/remote_write` writes to.
7. On success, respond `204 No Content` with an empty body, and increment
   `ingestionOTLPMetricPointsTotal.WithLabelValues(tenantID, "accepted")` once per data point
   written. On any `sink.Write` error, respond 500 with `"failed to persist external metric sample"`
   and stop processing further points.
8. On any rejection in step 5, before writing the error response, increment
   `ingestionOTLPMetricPointsTotal.WithLabelValues(tenantID, result)` exactly once, where `result` is
   one of `"rejected_missing_name"`, `"rejected_unsupported_type"`, `"rejected_exemplar"`,
   `"rejected_label"`, `"rejected_missing_value"`, `"rejected_cardinality"` matching the specific
   condition that triggered the rejection.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Any request to `/v1/traces`, any method | 400 | `gravix does not ingest OTLP trace signals: distributed tracing is not supported (see docs/04-non-goals.md §1)` |
| Any request to `/v1/logs`, any method | 400 | `gravix does not ingest OTLP log signals: log aggregation is not supported (see docs/04-non-goals.md §2)` |
| `/v1/metrics` method not POST | 405 | `only POST is accepted` |
| `/v1/metrics` body exceeds 10MB | 413 | `request body too large (max 10MB)` |
| `/v1/metrics` malformed JSON | 400 | `invalid JSON: <err>` |
| A metric has an empty `name` | 400 | `metric missing name` |
| A metric carries `histogram`/`exponentialHistogram`/`summary` | 400 | `metric "<name>": histogram, exponential_histogram, and summary points are not supported in the OTLP metrics subset; only gauge and sum are accepted` |
| A data point carries one or more exemplars | 400 | `exemplars are rejected: gravix does not ingest distributed tracing signals (see docs/04-non-goals.md §1)` |
| A data point (or the merged `service_name`) has a `trace_id`/`span_id`/`parent_span_id` attribute (any case) | 400 | `label carries tracing correlation data, which gravix does not ingest (see docs/04-non-goals.md §1): <key>` |
| A data point has neither `asDouble` nor `asInt`, or an unparseable `timeUnixNano` | 400 | `metric "<name>": data point missing asDouble/asInt, or has an invalid or missing timeUnixNano` |
| Cardinality budget exceeded for any data point | 400 | `cardinality budget exceeded: tenant=<t> metric=<m> limit=2000 distinct label-sets per day` |
| Durable write fails | 500 | `failed to persist external metric sample` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Any request to `POST /v1/traces` is rejected with 400 and the exact trace-rejection message, regardless of a well-formed legacy OTLP span body in the request | `TestHandleOTLPTracesRejectsAllRequests` |
| AC-2 | `POST /v1/traces` never calls `sink.Write` — a spy `DurableSink` records zero writes after the request | `TestHandleOTLPTracesNeverWritesToSink` |
| AC-3 | `GET /v1/traces` also returns 400 with the identical rejection message, proving rejection is unconditional on method | `TestHandleOTLPTracesRejectsRegardlessOfMethod` |
| AC-4 | Any request to `POST /v1/logs` is rejected with 400 and the exact log-rejection message | `TestHandleOTLPLogsRejectsAllRequests` |
| AC-5 | A well-formed single-gauge-datapoint OTLP metrics JSON payload is accepted and persisted as one `ExternalMetricSample` with `Source: "otlp_metrics"`, `MetricType: "gauge"`, responding 204 | `TestHandleOTLPMetricsAcceptsValidGauge` |
| AC-6 | A `sum` metric's data point is persisted with `MetricType: "counter"` | `TestHandleOTLPMetricsSumBecomesCounter` |
| AC-7 | A data point carrying an exemplar is rejected with 400 and the exact exemplar-rejection message reused from GRVX-1102 | `TestHandleOTLPMetricsRejectsExemplar` |
| AC-8 | A data point with an attribute named `trace_id` is rejected with 400 and the exact correlation-label message | `TestHandleOTLPMetricsRejectsTraceIDAttribute` |
| AC-9 | The (n+1)-th distinct label set for one (tenant, metric) pair, where n = the configured max, is rejected when the SAME `*cardinality.Budget` instance was already at capacity from a prior `POST /api/v1/remote_write` call, proving the two protocols share one ledger | `TestHandleOTLPMetricsSharesCardinalityBudgetWithRemoteWrite` |
| AC-10 | A `histogram` metric is rejected with 400 and the exact unsupported-type message | `TestHandleOTLPMetricsRejectsHistogram` |
| AC-11 | A resource `service.name` attribute is merged into the persisted sample's labels under the key `service_name` | `TestHandleOTLPMetricsMergesServiceNameLabel` |
| AC-12 | `GET /v1/metrics` returns 405 | `TestHandleOTLPMetricsMethodNotAllowed` |

## 8. Verification

```bash
# 1. Package tests
go test ./services/ingestion/... -run TestHandleOTLP -v
# expect: PASS for all twelve tests above

# 2. No dangling references to deleted trace-conversion symbols
grep -rn "spanToFact\|getSpanAttr\|getSpanAttrInt\|parseNano\|simplifyUserAgent\|OTLPTraceRequest\|ResourceSpan\|OTLPSpan\b" services/ingestion/*.go
# expect: no output

# 3. Full build and package tests
go build ./... && go test ./services/ingestion/... ./schemas/... ./pkg/cardinality/...
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
- [ ] The report lists every symbol deleted from `otlp.go` and confirms (via the §8 step 2 grep) none is referenced elsewhere

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| `bannedCorrelationLabels`, `ErrExemplarRejected`, or `ErrCorrelationLabel` do not exist in `services/ingestion/remote_write.go` at implementation time | Return `SPEC DEFECT: §2 — GRVX-1102 not yet merged or its identifiers differ`. Do not duplicate them speculatively. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |

---

## 11. Implementation report

All twelve acceptance criteria pass, plus nine additional tests — 21 in
`services/ingestion/otlp_test.go`, which did not exist before.

```
--- PASS: TestHandleOTLPTracesRejectsAllRequests
--- PASS: TestHandleOTLPTracesNeverWritesToSink
--- PASS: TestHandleOTLPTracesRejectsRegardlessOfMethod
--- PASS: TestHandleOTLPLogsRejectsAllRequests
--- PASS: TestHandleOTLPMetricsAcceptsValidGauge
--- PASS: TestHandleOTLPMetricsSumBecomesCounter
--- PASS: TestHandleOTLPMetricsRejectsExemplar
--- PASS: TestHandleOTLPMetricsRejectsTraceIDAttribute
--- PASS: TestHandleOTLPMetricsRejectsEveryCorrelationAttributeSpelling
--- PASS: TestHandleOTLPMetricsSharesCardinalityBudgetWithRemoteWrite
--- PASS: TestHandleOTLPMetricsRejectsHistogram
--- PASS: TestHandleOTLPMetricsMergesServiceNameLabel
--- PASS: TestHandleOTLPMetricsResourceServiceNameWinsOverAttribute
--- PASS: TestHandleOTLPMetricsMethodNotAllowed
--- PASS: TestHandleOTLPMetricsRejectsMalformedJSON
--- PASS: TestHandleOTLPMetricsRejectsMissingName
--- PASS: TestHandleOTLPMetricsRejectsMissingValueOrTimestamp
--- PASS: TestHandleOTLPMetricsAcceptsAsIntValue
--- PASS: TestHandleOTLPMetricsSkipsMetricWithNoDataPoints
--- PASS: TestHandleOTLPMetricsRejectionIsAtomic
--- PASS: TestHandleOTLPMetricsWritesToTheSharedExternalMetricsTopic
ok  github.com/lgreene/gravix-dashboards/services/ingestion  0.044s
boundary: 0 violations
build-oss exit=0
test-oss exit=0
```

### 11.1 §9 — symbols deleted from `otlp.go`, and the §8 step 2 grep

Deleted: `OTLPTraceRequest`, `ResourceSpan`, `ScopeSpan`, `OTLPSpan`, `OTLPStatus`,
`handleOTLPTraces`, `spanToFact`, `getSpanAttr`, `getSpanAttrInt`, `parseNano`, `simplifyUserAgent`.

Kept and reused: `OTLPResource`, `OTLPAttribute`, `OTLPAttrValue`, `otlpMaxBodyBytes`,
`extractResourceAttr`.

```
$ grep -rn "spanToFact\|getSpanAttr\|getSpanAttrInt\|parseNano\|simplifyUserAgent\|OTLPTraceRequest\|ResourceSpan\|OTLPSpan\b" services/ingestion/*.go
$
```

No output: nothing in the package references a deleted symbol. One clarification for anyone running
the grep repository-wide rather than scoped to `services/ingestion/*.go` as §8 step 2 specifies:
`sdk/go/otel/exporter.go` has its own `spanToFact`, a different function in a different package with
a different signature. It is the sanctioned client-side conversion path §2 describes, it is
untouched, and it is not a dangling reference.

### 11.2 §6 step 1's prediction about unused imports did not hold

§6 step 1 says to remove now-unused imports, "at minimum `strings`, used only by the deleted
`simplifyUserAgent` and `spanToFact`". `strings` is still used — by
`strings.ToLower(k)` in the correlation-label check this spec adds. No import became unused; the
final set is the original seven plus `errors`, `strconv`, `uuid`, `pkg/cardinality`, `schemas`,
`prometheus`, `protojson` and `timestamppb`. `go build` and `go vet` are clean.

### 11.3 The shared ledger is the part worth testing, and it is tested end to end

§5.1 says `budget` "must be the same `*cardinality.Budget` instance" as
`/api/v1/remote_write`, and §6 gives no way to verify that from inside either handler — a second
instance would work perfectly and silently double every tenant's effective budget.
`TestHandleOTLPMetricsSharesCardinalityBudgetWithRemoteWrite` therefore exhausts the budget for
`(acme, queue_depth)` entirely through `handleRemoteWrite`, then sends a brand-new label set for the
same pair through `handleOTLPMetrics` and requires the 400. It fails if the two handlers are ever
given separate instances, which is the only failure mode that matters here.

### 11.4 Where the spec's wording needed a decision, and what was chosen

**The resource wins.** §6 step 5e says to set `labels["service_name"]` from the resource
"overwriting any data-point attribute literally named `service_name`". Implemented as written, and
`TestHandleOTLPMetricsResourceServiceNameWinsOverAttribute` pins the reason: the resource is the
authoritative statement of who produced the metric, so a data point must not be able to claim a
different origin by declaring its own. Without the test the overwrite reads like an accident.

**`asInt` that will not parse.** §6 step 6 gives `v, _ := strconv.ParseInt(p.AsInt, 10, 64)` —
discarding the error, which would silently store `0` for a malformed integer. §6 step 5e validates
`p.AsDouble == nil && p.AsInt == ""` but never that `AsInt` is a number, so a value of `"twelve"`
would pass validation and be persisted as zero. The parse error is checked instead and rejected with
§6.1's existing `ErrOTLPMetricMissingValue` row, which already covers "missing asDouble/asInt, or an
invalid or missing timeUnixNano" — no new message invented. Covered by
`TestHandleOTLPMetricsRejectsMissingValueOrTimestamp/unparseable_asInt`. Storing a wrong number is
worse than refusing one, and this project's argument is that it knows the difference.

### 11.5 Deleting the receiver rather than hardening it

`/v1/traces` previously parsed OTLP JSON and converted any span with an `http.method` attribute into
a `RequestFact`. `TestHandleOTLPTracesRejectsAllRequests` posts exactly such a well-formed legacy
span body and requires the 400: nothing about a payload's validity earns it a hearing now.
`TestHandleOTLPTracesNeverWritesToSink` and the `/v1/logs` half of
`TestHandleOTLPLogsRejectsAllRequests` (which posts a log record containing what looks like a secret)
check that the body is never read, parsed, or stored.

This is a **breaking change**: a deployment pointing an OTel Collector's trace pipeline at
`/v1/traces` will start receiving 400s. The migration is `sdk/go/otel/exporter.go`, which does the
same conversion client-side so no trace ID leaves the caller's process. Nothing in
`docs-site/docs/api-reference.md` ever documented the endpoint, so no public documentation is
falsified — **NO DOCS DELTA REQUIRED** for the docs site. Two historical planning documents
(`docs/engineering-plan-phases-24-32.md:496`, `docs/engineering-plan-phase-33-beta.md:21`) describe
the receiver as it was when built; they are dated records of past sprints, not current API docs, and
are outside §4.

What is *not* covered: `CHANGELOG.md` has an `## [Unreleased]` section and no spec's §4 includes it,
so a breaking change ships with no changelog entry. Registered as **F-041**.

### 11.6 §2's line numbers have drifted again

As in GRVX-1102: `getTenantID` is at `services/ingestion/main.go:91` not 83-88, `writeErrorJSON` at
221 not 131-138, and the registration block at 978-992 not 766-791. The `otlp.go` line references in
§2 were accurate. The named symbols are unambiguous, so nothing was blocked — but §2's line numbers
should not be trusted by the next implementer.

### 11.7 Scope

`services/ingestion/remote_write.go` and `pkg/cardinality/**` are reused unmodified — this spec
references `bannedCorrelationLabels`, `ErrExemplarRejected` and `ErrCorrelationLabel` directly rather
than duplicating them, so one refusal has one wording whichever protocol carried the payload.
`handleTraces` (`/api/v1/traces`, the native low-volume sampling feature), `schemas/trace_sample.go`,
`sdk/go/otel/exporter.go`, `proto/gravix.proto` and `schemas/external_metric_sample.go` are
untouched. No OTLP protobuf/gRPC support, no histogram/summary conversion, no new dependency, no
Cube model or Trino table.
