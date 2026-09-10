# SPEC GRVX-905: Path-template auto-learning with a hard cardinality budget

| Field | Value |
|---|---|
| **Spec ID** | GRVX-905 |
| **Phase** | 9 |
| **Goal** | G3.6 (path templates auto-learned; cardinality budget never exceeded — 100%) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 "Schemas" row — "cardinality budget enforcement" is named explicitly as core, free forever. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704, GRVX-902 |
| **Blocks** | GRVX-909 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today, a `RequestFact` whose `path_template` contains a raw UUID or a ≥4-digit numeric segment is
**rejected outright** (`schemas/request_fact.go:80-87`) and routed to the DLQ — the client is
entirely responsible for templating its own paths before sending. A naive framework integration
(for example, an Express route registered as `/users/:id` but instrumented with the literal
request path `/users/42`) silently loses all its data into the DLQ with nothing on the dashboard.
After this spec, ingestion normalizes obviously-dynamic segments (UUIDs, long numeric runs) into
`{id}` automatically before validation, and separately learns and bounds the cardinality of
opaque, non-numeric dynamic segments (slugs, tokens) with a hard per-service template budget so
that no amount of naive instrumentation can create unbounded dimension cardinality.

## 2. Context the implementer needs

- `schemas/request_fact.go:13-17` — pre-compiled `uuidRegex` and `rawIDRegex` used by
  `containsUUID`/`containsRawID` (`schemas/request_fact.go:102-110`), both unexported.
- `schemas/request_fact.go:22-35` — `ParseRequestFact(data []byte) (*RequestFact, error)` calls
  `protojson.Unmarshal` then `ValidateRequestFact` — bundled, no way to normalize between the two
  steps today.
- `schemas/request_fact.go:38-100` — `ValidateRequestFact(f *RequestFact) error`, in particular
  lines 70-87 (the `path_template` checks this spec must run *after*, not instead of).
- `docs/04-non-goals.md` §5 — "All dimension columns must have bounded cardinality (e.g., < 1000
  unique values per day)."
- GRVX-902 (`pkg/discovery/discovery.go`) — the `discovered_services` SQLite registry opened by
  `discovery.Open`/`discovery.OpenWithFlushInterval`, and the `Registry` type this spec extends
  with a second table.
- `services/ingestion/main.go:912-976` (`handleFacts`) and `:978-1093` (`handleBatchFacts`) — both
  currently call `schemas.ParseRequestFact(body)` (or per-line, `schemas.ParseRequestFact(line)`)
  and, on error, write a `DLQEntry` via `writeDLQEntries` (`services/ingestion/main.go:49-74`) and
  respond `400`.
- `services/ingestion/main.go:1-35` already imports `google.golang.org/protobuf/encoding/protojson`.

## 3. Non-goals for this spec

- Do NOT modify `schemas/request_fact.go`'s `ValidateRequestFact` logic. All of its existing checks
  (event_id, event_time, service, method, status_code, latency, and the `?`/UUID/raw-ID rejections)
  continue to run, unchanged, on the **already-normalized** fact. This spec adds a new, separate,
  purely-additive function to `schemas/request_fact.go`; it does not alter any existing function's
  behaviour or signature.
- Do NOT persist learned segment counters or template counters across process restarts. State is
  in-memory for the life of the ingestion process. A restart resets learning; this is an accepted
  simplification, not a defect (Gravix's philosophy favours simplicity — see `AGENTS.md`).
- Do NOT retroactively re-template facts already written to Parquet. This spec affects only facts
  ingested after deployment.
- Do NOT change the total-cardinality budget referenced in `docs/04-non-goals.md` §5 (<1000/day);
  this spec's `DefaultTemplateBudgetPerService` of 200 sits comfortably under it with headroom for
  multiple services.
- This spec does not cross non-goal §5 (No High-Cardinality Dimensions) — it is the enforcement
  mechanism for that exact non-goal, not an exception to it.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/pathlearn/pathlearn.go` | Segment normalization and per-service template budget enforcement |
| `pkg/pathlearn/pathlearn_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `schemas/request_fact.go` | Add `UnmarshalRequestFactUnvalidated(data []byte) (*RequestFact, error)` — decode only, no validation |
| `schemas/request_fact_test.go` | Add `TestUnmarshalRequestFactUnvalidated` |
| `pkg/discovery/discovery.go` | Add `discovered_templates` table and `RecordTemplate`/`ListTemplates` methods |
| `pkg/discovery/discovery_test.go` | Add tests for the new table/methods |
| `services/ingestion/main.go` | Construct a `*pathlearn.Learner`; rewrite `handleFacts`/`handleBatchFacts` to normalize before validating |
| `services/ingestion/main_test.go` | Update call sites for the new `handleFacts`/`handleBatchFacts` parameter |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/request_fact.go`'s `ValidateRequestFact`, `containsUUID`, `containsRawID` | Unchanged business rules; this spec only adds a new function |
| `transforms/**` | Rollup jobs consume whatever `path_template` was already written; no change needed |
| `cmd/load_generator/**` | Its example paths (`cmd/load_generator/main.go:30`) already avoid raw IDs; not this spec's concern |

## 5. Interface contract

```go
// schemas/request_fact.go — new function, added below ParseRequestFact

// UnmarshalRequestFactUnvalidated decodes raw JSON into a RequestFact
// without running ValidateRequestFact, so a caller may rewrite fields (such
// as path_template) before validating. Returns the same wrapped error text
// as ParseRequestFact produces for a decode failure.
func UnmarshalRequestFactUnvalidated(data []byte) (*RequestFact, error) {
	var fact RequestFact
	if err := protojson.Unmarshal(data, &fact); err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
	}
	return &fact, nil
}
```

```go
// Package pathlearn normalizes and bounds the cardinality of path_template
// values on arriving RequestFacts before schemas.ValidateRequestFact runs.
package pathlearn

const (
	// DefaultSegmentDistinctBudget is the number of distinct literal values
	// allowed at one (service, method, position) before that position is
	// permanently collapsed to "{param}".
	DefaultSegmentDistinctBudget = 50

	// DefaultTemplateBudgetPerService is the number of distinct final
	// path_template values allowed per service, ever, for the life of the
	// process.
	DefaultTemplateBudgetPerService = 200
)

// Decision is the result of normalizing one path_template.
type Decision struct {
	Template string // the (possibly rewritten) path_template to use when Accepted
	Accepted bool   // false => reject the fact; do not ingest
	Reason   string // non-empty when Accepted is false, or when a segment was auto-collapsed
}

// Learner tracks per-(service,method,position) segment cardinality and
// per-service template cardinality. Safe for concurrent use.
type Learner struct {
	// unexported: mu sync.Mutex,
	//   segments map[segmentKey]map[string]struct{}  // distinct literal values seen
	//   collapsed map[segmentKey]bool                 // positions already forced to {param}
	//   templates map[string]map[string]struct{}      // service -> set of distinct templates
	//   segmentBudget, templateBudget int
}

// NewLearner constructs a Learner with the given budgets. Pass
// DefaultSegmentDistinctBudget and DefaultTemplateBudgetPerService for
// production use.
func NewLearner(segmentBudget, templateBudget int) *Learner

// Learn classifies and rewrites rawPath for the given service and method.
func (l *Learner) Learn(service, method, rawPath string) Decision
```

### 5.1 Exact `Reason` strings

| Condition | `Accepted` | `Reason` |
|---|---|---|
| No segment collapsed, template within budget | `true` | `""` |
| At least one opaque segment auto-collapsed to `{param}`, template within budget | `true` | `"path_template segment normalized to reduce cardinality"` |
| Template is new for this service and the service already has `DefaultTemplateBudgetPerService` distinct templates | `false` | `` `path_template budget exceeded for service "<service>" (budget=<n>); normalize this route in your instrumentation` `` |

## 6. Behaviour

1. `schemas/request_fact.go` gains `UnmarshalRequestFactUnvalidated` exactly as in §5. Add a
   doc-comment cross-reference from `ParseRequestFact`'s comment noting the new function exists for
   callers that need to mutate fields first.
2. `pkg/pathlearn/pathlearn.go`: `Learn(service, method, rawPath string) Decision`:
   a. If `rawPath` contains `?`, truncate at the first `?` (mirrors, and runs before,
      `schemas.ValidateRequestFact`'s existing query-parameter rejection — this is defense in
      depth, not a replacement for it).
   b. Split the truncated path on `/` into segments (preserving empty leading segment from a
      leading `/`).
   c. For each segment at position `i`:
      - If it matches the UUID pattern `[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`
        or the pattern `^[0-9]{4,}$` (both intentionally mirroring, and not imported from,
        `schemas/request_fact.go:15-16`, per §3), replace it with the literal `{id}`
        unconditionally — no budget check, this classification is always safe.
      - Otherwise, look up `key := segmentKey{service, method, i}`. If `l.collapsed[key]` is
        already `true`, replace the segment with `{param}`. Otherwise, add the literal segment
        value to `l.segments[key]`; if the resulting distinct count exceeds `segmentBudget`, set
        `l.collapsed[key] = true` and replace this segment with `{param}` (this segment's own
        occurrence is the one that trips the budget, and is itself collapsed).
   d. Reassemble `template := strings.Join(segments, "/")`.
   e. If at least one segment was replaced with `{param}` in step (c), remember
      `segmentCollapsed := true`.
   f. Look up `l.templates[service]`. If `template` is already present, return `Decision{Template:
      template, Accepted: true, Reason: <per segmentCollapsed>}` (a previously-accepted template is
      always re-accepted, regardless of current budget state — the budget only gates *new*
      templates).
   g. If `template` is new and `len(l.templates[service]) >= templateBudget`, return
      `Decision{Accepted: false, Reason: <the §5.1 budget-exceeded string>}` — do not add it to
      `l.templates[service]`.
   h. Otherwise add `template` to `l.templates[service]` and return `Decision{Template: template,
      Accepted: true, Reason: <per segmentCollapsed>}`.
3. `pkg/discovery/discovery.go` gains:
   ```go
   CREATE TABLE IF NOT EXISTS discovered_templates (
       service        TEXT NOT NULL,
       method         TEXT NOT NULL,
       path_template  TEXT NOT NULL,
       first_seen_at  TEXT NOT NULL,
       last_seen_at   TEXT NOT NULL,
       request_count  INTEGER NOT NULL DEFAULT 0,
       PRIMARY KEY (service, method, path_template)
   );

   type Template struct {
       Service      string    `json:"service"`
       Method       string    `json:"method"`
       PathTemplate string    `json:"path_template"`
       FirstSeenAt  time.Time `json:"first_seen_at"`
       LastSeenAt   time.Time `json:"last_seen_at"`
       RequestCount int64     `json:"request_count"`
   }

   // RecordTemplate records one accepted (service, method, path_template)
   // observation, batched exactly like RecordFact.
   func (r *Registry) RecordTemplate(service, method, template string, seenAt time.Time)

   // ListTemplates returns every known template for service, sorted by
   // path_template ascending.
   func (r *Registry) ListTemplates(ctx context.Context, service string) ([]Template, error)
   ```
   `Flush` persists both the service and template maps in the same transaction.
4. `services/ingestion/main.go`'s `main()` constructs `learner :=
   pathlearn.NewLearner(pathlearn.DefaultSegmentDistinctBudget,
   pathlearn.DefaultTemplateBudgetPerService)` and passes it as a fourth parameter to `handleFacts`
   and `handleBatchFacts`, alongside the `reg *discovery.Registry` parameter added by GRVX-902.
5. `handleFacts` is rewritten: replace the `fact, err := schemas.ParseRequestFact(body)` line with:
   ```go
   fact, err := schemas.UnmarshalRequestFactUnvalidated(body)
   if err != nil {
       // unchanged: same DLQ-write and 400 response as today, same message format
   }
   decision := learner.Learn(fact.Service, fact.Method, fact.PathTemplate)
   if !decision.Accepted {
       go writeDLQEntries(sink, tenantID, "request_fact", []DLQEntry{{
           Timestamp: time.Now().UTC(), TenantID: tenantID, RequestID: reqID,
           FactType: "request_fact", Error: decision.Reason, RawJSON: json.RawMessage(body),
       }})
       writeErrorJSON(w, http.StatusBadRequest, decision.Reason)
       ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "400", tenantID).Inc()
       return
   }
   fact.PathTemplate = decision.Template
   if err := schemas.ValidateRequestFact(fact); err != nil {
       // unchanged: same DLQ-write and 400 response as today
   }
   ```
   After a successful write, in addition to the existing `reg.RecordFact` call (GRVX-902), call
   `reg.RecordTemplate(fact.Service, fact.Method, fact.PathTemplate, fact.EventTime.AsTime())`.
6. `handleBatchFacts` is rewritten identically, per-line, inside its existing loop
   (`services/ingestion/main.go:1019-1055`): swap `schemas.ParseRequestFact(line)` for
   `schemas.UnmarshalRequestFactUnvalidated(line)` + `learner.Learn` + conditional
   `schemas.ValidateRequestFact`, preserving the existing per-line error-collection behaviour
   (`errors = append(errors, errMsg)` / `dlqEntries = append(...)`) for both the budget-rejection
   case and the post-normalization validation-failure case.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Segment budget exceeded at one position | segment silently collapsed to `{param}`, fact still accepted | (no error; `Decision.Reason = "path_template segment normalized to reduce cardinality"`) |
| Template budget exceeded for a service | fact rejected, DLQ entry written, `400` response | `` path_template budget exceeded for service "<service>" (budget=200); normalize this route in your instrumentation `` |
| `UnmarshalRequestFactUnvalidated` decode failure | unchanged from today | `invalid RequestFact: protojson unmarshal error: <detail>` |
| `ValidateRequestFact` fails for a non-path reason (e.g. bad `status_code`) after successful normalization | unchanged from today | `invalid RequestFact: validation error: <detail>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `Learn` on `/users/12345` returns `Template: "/users/{id}"`, `Accepted: true` | `TestLearnCollapsesNumericID` |
| AC-2 | `Learn` on a path containing a UUID segment returns that segment replaced with `{id}` | `TestLearnCollapsesUUID` |
| AC-3 | Calling `Learn` with 51 distinct literal values at the same `(service, method, position)` causes the 51st to be `{param}` | `TestLearnCollapsesAfterSegmentBudget` |
| AC-4 | Once a position is collapsed, a previously-seen literal value at that position is still rewritten to `{param}` (monotonic, no un-collapsing) | `TestLearnCollapseIsMonotonic` |
| AC-5 | The 201st distinct template for one service is rejected with `Accepted: false` | `TestLearnRejectsAfterTemplateBudget` |
| AC-6 | A template that was accepted before the budget was reached is still accepted on repeat occurrences after the budget is full | `TestLearnReacceptsKnownTemplateAfterBudgetFull` |
| AC-7 | `UnmarshalRequestFactUnvalidated` on a fact with `path_template: "/users/42"` succeeds (no validation run) | `TestUnmarshalRequestFactUnvalidated` |
| AC-8 | `POST /api/v1/facts` with `path_template: "/orders/987654"` is accepted (`201`), and the stored fact's `path_template` is `/orders/{id}` | `TestFactsEndpointAutoLearnsNumericID` |
| AC-9 | `POST /api/v1/facts` for the 201st distinct template on one service is rejected (`400`) with the exact §5.1 budget message | `TestFactsEndpointRejectsAfterTemplateBudget` |

## 8. Verification

```bash
# 1. pathlearn unit tests
go test ./pkg/pathlearn/... -v -cover

# 2. schemas addition, coverage still >= 90%
go test ./schemas/... -v -cover

# 3. discovery extension tests
go test ./pkg/discovery/... -v -run TestRecordTemplate

# 4. ingestion integration tests
go test ./services/ingestion/... -run TestFactsEndpoint -v

# 5. Nothing else broke
go build ./... && go test ./schemas/...

# 6. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `schemas/` coverage remains ≥90% (`scripts/golden_path_test.sh` step 5 threshold)
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (document the auto-learn behaviour and the two budgets in the
      ingestion API docs) or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| `schemas/` coverage tooling treats the new function's DLQ-adjacent branches as unreachable in a unit test | Return `SPEC DEFECT: §7 — AC-7 cannot reach 100% branch coverage without an integration test` |
| `handleBatchFacts`'s existing per-line error accumulation shape cannot represent a budget-rejection distinctly from a validation failure | Return `SPEC DEFECT: §6 step 6 — batch response schema needs a rejection-reason field` |
| Two ingestion processes (e.g. horizontally scaled) would each maintain independent, disagreeing in-memory budgets | Return `SPEC DEFECT: §3 — non-goal "in-memory only" breaks under multi-process ingestion` |
