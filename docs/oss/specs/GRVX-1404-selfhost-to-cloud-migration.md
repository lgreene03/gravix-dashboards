# SPEC GRVX-1404: One-command self-host → Cloud migration

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1404 |
| **Phase** | 14 |
| **Goal** | G8.4 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.2 borderline table — "Data export (Parquet/CSV/JSONL, manual + scheduled) — Core. Q5 = YES." The symmetric claim holds for the import direction: a self-hoster moving to Cloud is not made to pay to get their own history there, and the mechanism is the same public ingestion contract every SDK already uses. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | none |
| **Blocks** | none |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, a self-hoster runs exactly one command, `gravix migrate import-cloud`, and every
raw fact and service event already on their disk is replayed into a Gravix Cloud tenant through the
existing, unmodified public ingestion API — the same `POST /api/v1/facts/batch` and
`POST /api/v1/events` endpoints every SDK already calls. No new server-side code is required on
the Cloud side for this spec to work, because the ingestion contract already accepts historical
`event_time` values with no staleness check.

## 2. Context the implementer needs

- `services/ingestion/main.go:787-790` — the registered ingestion routes:
  `POST /api/v1/facts` (single fact), `POST /api/v1/facts/batch` (JSONL, multiple facts per
  request), `POST /api/v1/events` (single `ServiceEvent`). There is no `/api/v1/events/batch`.
- `services/ingestion/main.go:37` — `const maxBodyBytes = 1 << 20 // 1 MB max request body`.
- `services/ingestion/main.go:868-873` — `authMiddleware` requires header `X-API-Key`; a missing or
  invalid key returns `401` `"invalid or missing X-API-Key header"`.
- `services/ingestion/main.go:978-1090` — `handleBatchFacts`: request body is newline-delimited
  JSON (JSONL); each line is parsed with `schemas.ParseRequestFact`; response body on `200` is
  `{"accepted":<n>,"rejected":<n>,"errors":[...]}` (errors present only when `rejected > 0`).
  On `429` (quota exceeded), sets header `Retry-After: 30` (`services/ingestion/main.go:778`) and
  body `{"error":"monthly event quota exceeded — upgrade your plan"}`.
- `services/ingestion/main.go:1118-1148` — `handleEvents`: request body is exactly one JSON object,
  parsed with `schemas.ParseServiceEvent`; a parse failure returns `400`
  `"invalid ServiceEvent: <err>"`.
- `schemas/request_fact.go:51-57` — `ValidateRequestFact` requires `EventTime` to be present and
  parseable; there is no upper or lower bound check on how old `EventTime` may be. Historical
  replay is therefore accepted exactly as if the fact had just occurred.
- `pkg/storage/local.go`, `services/ingestion/main.go:484-487` — self-host raw facts already on
  disk live at `<baseDir>/raw/<tenant>/request_facts/YYYY-MM-DD/HH/*.jsonl` and
  `<baseDir>/raw/<tenant>/service_events/YYYY-MM-DD/HH/*.jsonl`, one JSON object per line, already
  in the exact wire format `handleBatchFacts`/`handleEvents` expect — because it is the same sink
  that wrote them in the first place (`services/ingestion/main.go`'s `DurableSink`).
- `cmd/cli/main.go:1-69` — CLI command dispatch pattern; see `GRVX-1403` §2 for the identical
  convention this spec's `migrate import-cloud` subcommand follows.
- `go.mod:3` — `go 1.24.9`.

## 3. Non-goals for this spec

- Do NOT implement `GRVX-1403` (the reverse, Cloud → self-host direction).
- Do NOT add a `/api/v1/events/batch` endpoint to `services/ingestion/main.go`. This spec works
  within the existing contract: facts are batched, events are sent one at a time, because that is
  what the server already accepts.
- Do NOT modify `services/ingestion/main.go`, `schemas/**`, or `pkg/storage/**`. The unmodified
  ingestion API and the unmodified on-disk JSONL format are used exactly as they exist today.
- Do NOT implement de-duplication against facts the destination tenant may already have (e.g. from
  a prior partial import). Re-running this command against an already-imported range produces
  duplicate facts in the destination. Idempotent replay is a documented limitation, stated in the
  guide this spec publishes, not solved by this spec.
- This spec does not cross non-goal §5 (`docs/04-non-goals.md`, No High-Cardinality Dimensions) —
  it moves already-validated facts unchanged; it introduces no new dimension.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/cli/cmd_migrate_import.go` | `runMigrateImportCloud`, `chunkJSONLLines`, `postFactsBatch`, `postEvent` |
| `cmd/cli/cmd_migrate_import_test.go` | Unit tests against a fake ingestion server (`httptest.Server`) |
| `tests/e2e/selfhost_to_cloud_migration_test.go` | `TestSelfHostToCloudImportPreservesFactCount` — real unmodified ingestion binary as source, real unmodified ingestion binary as destination |
| `docs-site/docs/selfhost-to-cloud-migration.md` | The published, single-command migration guide, including the no-deduplication limitation |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/main.go` | Add an `"import-cloud"` case under the existing `"migrate"` case in the `switch os.Args[2]` (introduced by `GRVX-1403`; if `GRVX-1403` has not yet added the `"migrate"` case, add it here instead), dispatching to `runMigrateImportCloud(os.Args[3:])`; update `printUsage()` to list `gravix migrate import-cloud` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go` | Reused unmodified; this spec is a client of `/api/v1/facts/batch` and `/api/v1/events` |
| `schemas/**` | Validation is unchanged; replayed facts either pass or fail exactly as they did on first ingestion |
| `pkg/storage/**` | The on-disk JSONL layout is read-only context |

## 5. Interface contract

### 5.1 `cmd/cli/cmd_migrate_import.go`

```go
package main

// runMigrateImportCloud implements `gravix migrate import-cloud`.
func runMigrateImportCloud(args []string)

// chunkJSONLLines groups lines into batches of at most maxLines lines and at
// most maxBytes total bytes (the sum of each line's length plus one newline
// byte per line), preserving input order. A single line whose own length
// exceeds maxBytes forms its own one-line batch.
func chunkJSONLLines(lines [][]byte, maxLines, maxBytes int) [][][]byte

// postFactsBatch sends lines newline-joined as the body of one
// POST <ingestionEndpoint>/api/v1/facts/batch, header "X-API-Key": apiKey,
// header "Content-Type": "application/json". On a 200 response it decodes
// {"accepted":int,"rejected":int} and returns those counts. On a 429
// response it sleeps for the duration in the response's "Retry-After"
// header (seconds) and retries exactly once; a second 429 returns
// (0, len(lines), nil) — every line in the batch is counted rejected, and
// the caller continues to the next batch rather than aborting the run. Any
// other non-2xx status returns (0, len(lines), ErrIngestionRejected).
func postFactsBatch(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, lines [][]byte) (accepted, rejected int, err error)

// postEvent sends one line as the body of one
// POST <ingestionEndpoint>/api/v1/events, header "X-API-Key": apiKey. It
// returns nil only on a 2xx response.
func postEvent(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, line []byte) error

// importSummary is written to stdout as JSON after the run completes.
type importSummary struct {
	FilesRead      int       `json:"files_read"`
	FactsAccepted  int       `json:"facts_accepted"`
	FactsRejected  int       `json:"facts_rejected"`
	EventsAccepted int       `json:"events_accepted"`
	EventsFailed   int       `json:"events_failed"`
	ImportedAt     time.Time `json:"imported_at"`
}

var (
	ErrMissingTenantDir  = errors.New("migrate import-cloud: --tenant-dir-name is required")
	ErrMissingAPIKey     = errors.New("migrate import-cloud: --api-key is required (or set GRAVIX_API_KEY)")
	ErrIngestionRejected = errors.New("migrate import-cloud: ingestion API returned a non-2xx status")
)
```

### 5.2 CLI flags — `gravix migrate import-cloud`

`flag.NewFlagSet("migrate import-cloud", flag.ExitOnError)`:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--data-dir` | string | `./data` | Local self-host data directory; facts are read from <data-dir>/raw/<tenant-dir-name>/request_facts and .../service_events |
| `--tenant-dir-name` | string | `""` | The <tenant> path segment under --data-dir/raw/ to read from (required) |
| `--ingestion-endpoint` | string | `http://localhost:8090` | Base URL of the target Cloud ingestion API |
| `--api-key` | string | `""` | Cloud tenant API key; falls back to GRAVIX_API_KEY env var if empty |
| `--batch-lines` | int | `500` | Number of JSONL lines grouped per /api/v1/facts/batch request |
| `--dry-run` | bool | `false` | Walk and count files/lines without sending any HTTP request |

## 6. Behaviour

1. Parse flags. If `--tenant-dir-name` is empty, print `usage: gravix migrate import-cloud --tenant-dir-name <name> [flags]` to stderr and exit `2`.
2. Resolve the API key: `--api-key`, else `os.Getenv("GRAVIX_API_KEY")`. If both are empty and `--dry-run` is false, print `migrate import-cloud: --api-key is required (or set GRAVIX_API_KEY)` to stderr and exit `2`.
3. For `request_facts`: recursively walk `<data-dir>/raw/<tenant-dir-name>/request_facts/`; if the directory does not exist, treat it as zero files (not an error). Collect every `*.jsonl` file path, sorted lexicographically (the embedded `YYYY-MM-DD/HH` path segments make lexicographic order chronological).
4. For each file: read all lines (skip empty lines). Call `chunkJSONLLines(lines, batchLines, 900000)`. For each chunk, if `--dry-run` is true, add `len(chunk)` to a running count and continue without calling `postFactsBatch`. Otherwise call `postFactsBatch` and add its `accepted`/`rejected` to the running totals; if it returns a non-nil `err`, print `migrate import-cloud: <error>` to stderr and exit `1`.
5. For `service_events`: same directory-walk and file-sort rule under `.../service_events/`. For each line in each file, if `--dry-run` is true, add 1 to a running count and continue. Otherwise call `postEvent`; on success increment `EventsAccepted`; on error increment `EventsFailed` and continue to the next line (a single bad event does not abort the run).
6. After all files are processed, print the JSON-encoded `importSummary` to stdout and exit `0`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `--tenant-dir-name` empty | exit 2 | `usage: gravix migrate import-cloud --tenant-dir-name <name> [flags]` |
| API key unresolved and not `--dry-run` | exit 2 | `migrate import-cloud: --api-key is required (or set GRAVIX_API_KEY)` |
| `postFactsBatch` returns a non-2xx, non-429 status | exit 1 | `migrate import-cloud: ingestion API returned a non-2xx status` |
| `postFactsBatch` receives a second consecutive 429 for one batch | batch's lines counted as rejected; run continues | — |
| `postEvent` fails for one line | event counted as failed; run continues to the next line | — |
| `request_facts` or `service_events` subdirectory absent | treated as zero files for that data type; run continues | — |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `chunkJSONLLines` never produces a chunk exceeding `maxLines` lines or `maxBytes` total bytes, except a single line longer than `maxBytes` alone in its own chunk | `TestChunkJSONLLinesRespectsBothLimits` |
| AC-2 | `postFactsBatch` sends the exact newline-joined `lines` as the request body and returns the `accepted`/`rejected` counts decoded from the response | `TestPostFactsBatchParsesResponse` |
| AC-3 | `runMigrateImportCloud` exits 2 when `--tenant-dir-name` is empty | `TestMigrateImportRequiresTenantDir` |
| AC-4 | `runMigrateImportCloud` with `--dry-run` sends zero HTTP requests to the fake ingestion server | `TestMigrateImportDryRunSendsNoRequests` |
| AC-5 | `postFactsBatch` retries exactly once after a 429, waiting for the duration in `Retry-After`, and counts the batch rejected on a second 429 | `TestPostFactsBatchRetriesOnceOn429` |
| AC-6 | `service_events` lines are POSTed individually to `/api/v1/events`, one HTTP request per line | `TestMigrateImportPostsEventsIndividually` |
| AC-7 | Facts written to a source self-host raw directory by the unmodified ingestion binary, then imported via `gravix migrate import-cloud` into a second unmodified ingestion binary instance, produce a destination `FactsAccepted` count equal to the source file's line count | `TestSelfHostToCloudImportPreservesFactCount` |

## 8. Verification

```bash
# 1. Unit tests
go test ./cmd/cli/... -run 'TestChunkJSONLLines|TestPostFactsBatch|TestMigrateImport' -v
# expect: PASS

# 2. Real end-to-end: two unmodified ingestion binaries, one importer between them
go test ./tests/e2e/... -run TestSelfHostToCloudImportPreservesFactCount -v -count=1 -timeout 300s
# expect: PASS

# 3. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All seven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The no-deduplication limitation is stated in the published guide, not only in this spec

## 10. Escalation

| If you find… | Do this |
|---|---|
| `handleBatchFacts` or `handleEvents`'s request/response shape has changed since §2 was written | Return `SPEC DEFECT: §2 — services/ingestion/main.go shape mismatch` |
| `GRVX-1403` has already added the `"migrate"` case in `cmd/cli/main.go` in an incompatible shape | Return `SPEC DEFECT: §4.2 — cmd/cli/main.go migrate dispatch conflict` |
| A criterion cannot be met without modifying a file outside §4.1/§4.2 | Return `SPEC DEFECT: §4 — needs <path>` |
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
