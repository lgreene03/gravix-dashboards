# SPEC GRVX-902: Service auto-discovery from arriving facts

| Field | Value |
|---|---|
| **Spec ID** | GRVX-902 |
| **Phase** | 9 |
| **Goal** | G3.3 (services auto-discovered from arriving facts — 100%) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 "Ingestion" row — facts ingestion and everything derived from it on the hot path is core, free forever. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704 |
| **Blocks** | GRVX-903, GRVX-904, GRVX-905, GRVX-907, GRVX-908 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today the only place a distinct list of services exists is a Cube.js query over rolled-up Parquet
data (`dashboards/app.js:883-928`, `fetchServices()`), which is empty until the first 5-minute
rollup completes and requires no registration step but also no *immediate* visibility. After this
spec, the ingestion service maintains a durable, queryable registry of every distinct `service`
value seen on a validated `RequestFact`, updated the moment the fact is accepted — before any
rollup runs — and exposes it over a new authenticated HTTP endpoint that the dashboard consumes.

## 2. Context the implementer needs

- `services/ingestion/main.go:912-976` — `handleFacts(sink *DurableSink, tdb tenantdb.DB)
  http.HandlerFunc` calls `schemas.ParseRequestFact(body)`, then `sink.Write(topic, cleanData)`,
  then `incrementEventCounter(tdb, tenantID, 1)` (fire-and-forget goroutine pattern to keep off the
  hot path), then responds `201`.
- `services/ingestion/main.go:978-1093` — `handleBatchFacts` does the same per-line in a loop,
  writing all valid records in one `sink.WriteBatch` call.
- `services/ingestion/main.go:107-119` — `requireScope(scope string, next http.HandlerFunc)
  http.HandlerFunc` middleware; existing scopes in use are `"ingest:write"`, `"traces:write"`, and
  the roster mentions `"admin:read"`/`"admin:write"` (`pkg/tenantdb/tenantdb.go:33-36` comment on
  `APIKey.Scopes`).
- `services/ingestion/main.go:787-792` — the `http.Handle` route table, all wrapped
  `authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope(...))))`.
- `services/ingestion/main.go:654-830` — `main()` opens storage, builds the `DurableSink`, then
  registers routes. `*baseDir` defaults to `"./data"` (flag `-base-dir`).
- `pkg/tenantdb/sqlite.go:16,26-38` — the existing pattern for opening a `modernc.org/sqlite`
  (CGO-free) database with `_journal_mode=WAL&_busy_timeout=5000` and a single-connection pool.
  `discovery` reuses this pattern in its own file, independent of `pkg/tenantdb`, because service
  discovery must work identically with or without `TENANT_DB_PATH` set (legacy single-key mode).
- `dashboards/app.js:1-13` — `GRAVIX_CONFIG` defaults (`cubeApiUrl`, `gatewayUrl`,
  `refreshIntervalMs`, `staleThresholdMs`), built via `Object.assign({defaults},
  window.GRAVIX_CONFIG || {})`. GRVX-901 (`cmd/bootstrap_seed`) now generates
  `dashboards/dashboard_config.js` populating `window.GRAVIX_CONFIG.ingestionApiUrl` and
  `window.GRAVIX_CONFIG.apiKey` for the bootstrap deployment.
- `storage/dashboard/nginx.conf:12` — the `Content-Security-Policy` header's `connect-src` directive
  currently allows only `'self' http://localhost:4000 http://localhost:8091`. It does **not** allow
  `http://localhost:8090` (ingestion), so any `fetch()` from the dashboard to ingestion is blocked
  by the browser today.

## 3. Non-goals for this spec

- Do NOT implement the SLO dashboard UI that consumes the service list — GRVX-903.
- Do NOT implement path-template learning — GRVX-905 extends this registry's schema for that.
- Do NOT persist anything about individual requests (no `request_id`, no per-request rows). The
  registry stores one row per distinct service name plus three counters. This spec does not cross
  non-goal §5 (No High-Cardinality Dimensions) because `service` is already a bounded, low-
  cardinality dimension accepted everywhere else in the system (`schemas/request_fact.go:61-63`
  requires it non-empty, and it is already a Parquet partition/dimension in
  `transforms/request_metrics_minute/main.go:76-88`).
- Do NOT add a new API key scope constant. Reuse the existing `"admin:read"` scope named in
  `pkg/tenantdb/tenantdb.go:33-36`.
- Do NOT change `schemas/request_fact.go` or `schemas/service_event.go`.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/discovery/discovery.go` | The service registry: SQLite-backed, in-memory-batched |
| `pkg/discovery/discovery_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/ingestion/main.go` | Add `-discovery-db` flag; open a `*discovery.Registry`; record every accepted fact's service; add `GET /api/v1/services`; add `reg.Close()` to shutdown |
| `services/ingestion/main_test.go` | Update every call to `handleFacts` and `handleBatchFacts` to pass the new `*discovery.Registry` parameter (use `discovery.OpenInMemory()` in tests that do not assert on discovery) |
| `storage/dashboard/nginx.conf` | Add `http://localhost:8090` to the `connect-src` directive of the `Content-Security-Policy` header |
| `dashboards/app.js` | Add `ingestionApiUrl: "http://localhost:8090"` and `apiKey: ""` to the `GRAVIX_CONFIG` defaults object (`app.js:3-8`); add `ingestionFetch(path, options)` helper |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/**` | No validation rule changes in this spec |
| `pkg/tenantdb/**` | Discovery is a separate, tenant-DB-independent store |
| `transforms/**` | Rollup jobs are unaffected; discovery is purely ingestion-side |
| `dashboards/index.html` | No new page/nav in this spec — see GRVX-903 |

## 5. Interface contract

```go
// Package discovery maintains a durable registry of services observed on
// arriving RequestFacts, so consumers can enumerate them without waiting for
// a rollup. It is independent of pkg/tenantdb and works identically whether
// or not TENANT_DB_PATH is set.
package discovery

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Service is one row of the registry.
type Service struct {
	Name         string    `json:"name"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	RequestCount int64     `json:"request_count"`
}

// Registry batches in-memory service observations and flushes them to a
// local SQLite database on a fixed interval. Safe for concurrent use.
type Registry struct {
	// unexported: db *sql.DB, mu sync.Mutex, pending map[string]*pendingCount,
	// flushInterval time.Duration, stopCh chan struct{}, doneCh chan struct{}
}

// Open opens (or creates) the registry database at path and starts the
// background flush loop with a 10-second interval.
func Open(path string) (*Registry, error)

// OpenWithFlushInterval is Open with an explicit flush interval, for tests.
func OpenWithFlushInterval(path string, flushInterval time.Duration) (*Registry, error)

// OpenInMemory opens a registry backed by an in-process SQLite database
// (":memory:") with a 10-millisecond flush interval, for use in tests that
// need a Registry but do not assert on discovery behaviour.
func OpenInMemory() (*Registry, error)

// RecordFact records that service was observed at seenAt. It updates an
// in-memory counter synchronously (no I/O) and returns immediately; the
// counter is persisted by the next background flush.
func (r *Registry) RecordFact(service string, seenAt time.Time)

// Flush writes all pending in-memory counters to the database immediately.
// Called by the background loop and by Close.
func (r *Registry) Flush(ctx context.Context) error

// ListServices returns every known service, sorted by Name ascending. It
// reads the database directly (already-flushed state) merged with any
// still-pending in-memory counts, so a call immediately after RecordFact
// reflects it without waiting for a flush.
func (r *Registry) ListServices(ctx context.Context) ([]Service, error)

// Close stops the background flush loop, flushes any pending counters, and
// closes the database.
func (r *Registry) Close() error

var ErrEmptyServiceName = errors.New("discovery: service name must not be empty")
```

### 5.1 SQLite schema (created by `Open` if absent)

```sql
CREATE TABLE IF NOT EXISTS discovered_services (
	name           TEXT PRIMARY KEY,
	first_seen_at  TEXT NOT NULL,
	last_seen_at   TEXT NOT NULL,
	request_count  INTEGER NOT NULL DEFAULT 0
);
```

Timestamps are stored as RFC3339 UTC strings, matching the convention in
`pkg/tenantdb/sqlite.go`. Upserts use
`INSERT INTO discovered_services (name, first_seen_at, last_seen_at, request_count) VALUES (?,?,?,?)
ON CONFLICT(name) DO UPDATE SET last_seen_at = excluded.last_seen_at, request_count =
request_count + excluded.request_count`.

### 5.2 New ingestion endpoint

`GET /api/v1/services`

- Auth: existing `X-API-Key` middleware, scope `"admin:read"` via the existing `requireScope`
  helper.
- Request body: none.
- Response `200 OK`, `Content-Type: application/json`:

```json
{
  "services": [
    {"name": "auth-service", "first_seen_at": "2026-09-10T12:00:00Z", "last_seen_at": "2026-09-10T12:05:00Z", "request_count": 482}
  ]
}
```

- Services sorted by `name` ascending. Empty registry returns `{"services": []}`, HTTP `200` (no
  data yet is not an error — matches G3.7's empty-state philosophy).

### 5.3 `dashboards/app.js` addition

```js
// GRAVIX_CONFIG defaults gain two fields:
//   ingestionApiUrl: "http://localhost:8090"
//   apiKey: ""
//
// ingestionFetch(path, options) issues a request to
// `${GRAVIX_CONFIG.ingestionApiUrl}${path}`, setting the `X-API-Key` header
// from `GRAVIX_CONFIG.apiKey` when non-empty. It does not throw on a non-2xx
// response; callers inspect `response.ok`.
async function ingestionFetch(path, options = {}) { /* ... */ }
```

## 6. Behaviour

1. `main()` gains flag `-discovery-db string` default `""`. If empty, it is computed as
   `filepath.Join(*baseDir, "discovery.db")`.
2. `main()` calls `discovery.Open(discoveryDBPath)`. On error, log and `os.Exit(1)`, matching the
   existing pattern for `storage.NewLocalStore`/`NewS3Store` failures.
3. `handleFacts` and `handleBatchFacts` each gain a third parameter `reg *discovery.Registry`.
   Update the two call sites in `main()` (`services/ingestion/main.go:787-789`) to pass `reg`.
4. In `handleFacts`, immediately after the successful `sink.Write` call and before the response is
   written, call `reg.RecordFact(fact.Service, fact.EventTime.AsTime())`.
5. In `handleBatchFacts`, for every fact successfully parsed and included in `validRecords`, call
   `reg.RecordFact(fact.Service, fact.EventTime.AsTime())` in the same loop iteration that appends
   to `validRecords`.
6. Register `http.Handle("/api/v1/services", authMW(requireScope("admin:read",
   handleServices(reg))))` in `main()`, alongside the other route registrations.
7. `handleServices(reg *discovery.Registry) http.HandlerFunc` calls `reg.ListServices(r.Context())`
   and writes the JSON response per §5.2. On a `ListServices` error, respond `500` with
   `writeErrorJSON(w, http.StatusInternalServerError, "failed to list services")`.
8. Add `defer reg.Close()` in `main()` alongside the existing `defer sink.Close()`.
9. `dashboards/app.js`: add `ingestionApiUrl` and `apiKey` to the `GRAVIX_CONFIG` defaults object
   (`app.js:3-8`) and add the `ingestionFetch` helper per §5.3, placed immediately after the
   existing `cachedFetch` function (`app.js:19-42`).
10. `storage/dashboard/nginx.conf`: change the `Content-Security-Policy` header's `connect-src`
    value from `'self' http://localhost:4000 http://localhost:8091` to `'self' http://localhost:4000
    http://localhost:8090 http://localhost:8091`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `discovery.Open` fails at startup | `main()` logs and exits 1 | `failed to open discovery registry` (structured `slog.Error` with `"error"` field) |
| `RecordFact` called with `service == ""` | no-op, does not panic, does not increment any counter | (no message — silent no-op; `ValidateRequestFact` already rejects empty `service` before this is reached, so this path exists only as a defensive guard) |
| `reg.ListServices` fails (e.g. disk I/O error) | `handleServices` responds `500` | `failed to list services` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `RecordFact` followed by `ListServices` returns exactly one service with `RequestCount == 1` | `TestRecordFactThenList` |
| AC-2 | Calling `RecordFact` three times for the same service accumulates `RequestCount == 3` | `TestRecordFactAccumulatesCount` |
| AC-3 | `ListServices` returns services sorted by name ascending | `TestListServicesSortedByName` |
| AC-4 | `RecordFact("", time.Now())` does not create a row | `TestRecordFactRejectsEmptyName` |
| AC-5 | A fresh `Registry` with no `RecordFact` calls returns an empty, non-nil slice from `ListServices` | `TestListServicesEmptyRegistry` |
| AC-6 | `Close` flushes pending counters so a subsequent `Open` on the same path sees them | `TestCloseFlushesPending` |
| AC-7 | `POST /api/v1/facts` with a valid fact, followed by `GET /api/v1/services`, returns that fact's service | `TestServicesEndpointReflectsIngestedFact` |
| AC-8 | `GET /api/v1/services` without a valid `X-API-Key` returns `401` | `TestServicesEndpointRequiresAuth` |

## 8. Verification

```bash
# 1. Registry unit tests
go test ./pkg/discovery/... -v -cover
# expect: PASS, coverage printed

# 2. Ingestion integration test
go test ./services/ingestion/... -run TestServicesEndpoint -v
# expect: PASS

# 3. Nothing else broke
go build ./... && go test ./schemas/... && go test ./services/ingestion/...
# expect: build ok, PASS

# 4. CSP change is well-formed
grep -o "connect-src[^;]*" storage/dashboard/nginx.conf
# expect: connect-src 'self' http://localhost:4000 http://localhost:8090 http://localhost:8091

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

### 8.1 Verification record — 2026-09-11

**Result: implemented and verified.** All eight acceptance criteria pass with their named tests, plus
seven more covering paths the criteria do not reach.

**1. Registry unit tests**

```
$ go test ./pkg/discovery/... -v -cover
--- PASS: TestRecordFactThenList              (AC-1)
--- PASS: TestRecordFactAccumulatesCount      (AC-2)
--- PASS: TestRecordFactAccumulatesAcrossAFlush
--- PASS: TestListServicesSortedByName        (AC-3)
--- PASS: TestRecordFactRejectsEmptyName      (AC-4)
--- PASS: TestListServicesEmptyRegistry       (AC-5)
--- PASS: TestCloseFlushesPending             (AC-6)
--- PASS: TestCloseIsIdempotent
--- PASS: TestConcurrentRecordFact
--- PASS: TestFlushRequeuesOnFailure
ok  	github.com/lgreene/gravix-dashboards/pkg/discovery	coverage: 80.6% of statements
```

**2. Ingestion integration test**

```
$ go test ./services/ingestion/... -run TestServicesEndpoint -v
--- PASS: TestServicesEndpointReflectsIngestedFact   (AC-7)
--- PASS: TestServicesEndpointReflectsBatchIngest
--- PASS: TestServicesEndpointEmptyRegistry
--- PASS: TestServicesEndpointRejectsNonGET
--- PASS: TestServicesEndpointRequiresAuth           (AC-8)
    --- PASS: /no_key_at_all
    --- PASS: /a_key_that_was_never_issued
    --- PASS: /the_issued_key
ok  	github.com/lgreene/gravix-dashboards/services/ingestion	0.054s
```

**3. Nothing else broke**

```
$ go build ./... && go test ./schemas/... ./services/ingestion/...
ok  	github.com/lgreene/gravix-dashboards/schemas	0.006s
ok  	github.com/lgreene/gravix-dashboards/services/ingestion	0.404s
```

**4. CSP change is well-formed**

```
$ grep -o "connect-src[^;]*" storage/dashboard/nginx.conf
connect-src 'self' http://localhost:4000 http://localhost:8090 http://localhost:8091
```

**5. Open-core integrity**

```
$ make check-boundary
boundary: 0 violations
$ make build-oss && make test-oss
(both succeed with ee/ absent)
```

**Beyond §8** — the gates that exist only in CI, plus the two suites this change could plausibly
disturb:

```
$ go test ./... -race -count=1     # no failures, no races
$ make lint                         # go vet + staticcheck, clean
$ make test-correctness             # runtime: 52s (budget 300s), all seven properties hold
$ make contracts-check              # no diff
$ git check-ignore -v data/discovery.db
.gitignore:51:data/	data/discovery.db
```

### 8.2 The concurrency contract, exercised rather than assumed

§10 makes an unresolved race here a spec defect against §5, so `TestConcurrentRecordFact` runs eight
writers, a flusher and a reader against one registry under `-race` and then checks the total. It
passes and the count is exact, so the escalation does not trigger.

The sharp edge is not `RecordFact` itself but `Flush`, which swaps the pending map out from under
concurrent readers and writers. An observation must be counted exactly once whichever side of that
swap it lands on, and two tests pin it: `TestRecordFactAccumulatesAcrossAFlush` (a flush between
observations does not lose or double them, and flushing twice does not re-apply a landed batch) and
`TestFlushRequeuesOnFailure`.

`TestFlushRequeuesOnFailure` covers the branch that decides whether a disk problem loses data
silently. A failed flush has already taken the pending map; unless it puts the batch back, those
observations are gone with no error reaching anyone, because `RecordFact` returns nothing and the
handler replied long ago. The test closes the database underneath the registry, requires the flush to
fail, and then requires the batch to be back in `pending` and to *merge* with — not be replaced by —
anything recorded since.

### 8.3 Guards proven by mutation

| Guard | Mutation | Reported |
|---|---|---|
| AC-3 `TestListServicesSortedByName` | removed the sort | yes — named the position and printed the real order |
| AC-5 `TestListServicesEmptyRegistry` | returned a nil slice instead of an empty one | yes — a nil slice serialises as `null`, and a dashboard reading `.length` throws |
| AC-7 `TestServicesEndpointReflectsIngestedFact` | removed `RecordFact` from `handleFacts` | yes — the endpoint returned `{"services":[]}` after a successful ingest |

### 8.4 Defects found

- **SD-014 (low, implemented as specified).** §6.4 records a single fact *after* a successful write;
  §6.5 records batch facts *inside the parse loop*, before `WriteBatch` runs. If the batch write
  fails the handler returns 500, the services are already counted, and a client retry counts them
  again. Followed the spec rather than quietly improving it, and said so in three places: a comment
  at the call site, the `request_count` description in `docs/openapi.yaml`, and the register.
- **SD-013 (filed against GRVX-901) is now partly closed.** This spec supplies the consumers that
  were missing: `ingestionApiUrl` and `apiKey` join the `GRAVIX_CONFIG` defaults, and
  `ingestionFetch` sends the key. The defect was in reading two specs one at a time — which is the
  working rule — not in either spec alone. What remains open is the security half: the generated
  config is served from the nginx web root, so `GET /dashboard_config.js` hands a live *write*-capable
  ingestion key to anyone who can load the dashboard.

### 8.5 Deviations from the spec

| Deviation | Why |
|---|---|
| `services/ingestion/lateness_test.go` modified, though §4.2 names only `main_test.go` | It holds five more `handleFacts`/`handleBatchFacts` call sites. Without updating them the package does not compile, so the spec's file list is simply incomplete. |
| `services/ingestion/services_endpoint_test.go` created, though §4.1 names only the two `pkg/discovery` files | AC-7 and AC-8 are ingestion-side and need somewhere to live; putting them in the 900-line `main_test.go` would bury them. |
| `docs/openapi.yaml` modified | The §9 docs delta. §10 asks first whether it is generated — it is not, and there is now only one such document, so a hand edit is the documented route. |
| `testRegistry` takes `testing.TB`, not `*testing.T` | `BenchmarkHandleFacts` is one of the call sites and has a `*testing.B`. |

## 9. Definition of done

- [x] All eight acceptance criteria pass with their named tests — §8.1, plus seven more
- [x] Every Verification command run, real output pasted into the report — §8.1
- [x] `make check-boundary` clean
- [x] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] **No** — four files outside §4.1/§4.2, each listed with its reason in §8.5. One of them
      (`lateness_test.go`) is not optional: the package does not compile without it.
- [x] `docs-engineer` delta merged — `GET /api/v1/services` documented in `docs/openapi.yaml`,
      including the caveat that `request_count` is a discovery aid and not a billing figure
- [x] Zero new skipped or quarantined tests
- [x] `data/discovery.db` covered by the existing `data/` rule at `.gitignore:51` — verified with
      `git check-ignore -v`, not by reading the file

## 10. Escalation

| If you find… | Do this |
|---|---|
| `docs/openapi.yaml` has a generator that would be bypassed by a hand edit | Return `SPEC DEFECT: §9 — openapi.yaml is generated, needs a documented regeneration step` |
| `requireScope` scope string `"admin:read"` is not actually issued to any existing API key in practice (only `""`/unrestricted keys exist) | Proceed — an unrestricted key (`Scopes == ""`) already satisfies `HasScope("admin:read")` per `pkg/tenantdb/tenantdb.go:59-65`; this is not a defect |
| Concurrent `RecordFact` calls under `-race` reveal a data race not resolved by the documented `sync.Mutex` | Return `SPEC DEFECT: §5 — Registry concurrency contract insufficient` |
