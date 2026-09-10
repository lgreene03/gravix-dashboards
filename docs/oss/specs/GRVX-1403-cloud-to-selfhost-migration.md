# SPEC GRVX-1403: One-command Cloud → self-host migration

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1403 |
| **Phase** | 14 |
| **Goal** | G8.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.2 borderline table — "Data export (Parquet/CSV/JSONL, manual + scheduled) — Core. Q5 = YES. Export is the anti-lock-in guarantee; gating it is hostage-taking." The exit path out of Cloud is the sharpest instance of that rule and must never require a paid tier. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-1401, GRVX-1101 |
| **Blocks** | none |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, a Gravix Cloud customer runs exactly one command,
`gravix migrate export-cloud`, and receives a local directory of their raw facts and service
events in the exact object-store key layout the OSS self-host stack already reads, produced
using only the existing, unmodified Cloud gateway export endpoint and the existing, unmodified
ingestion key convention. Running the existing, unmodified `rollup` binary against that directory
reproduces the warehouse Parquet, and the existing, unmodified `duckdb` CLI reads it — proving in
CI that the exit path is real, not documentation.

## 2. Context the implementer needs

- `services/gateway/main.go:1251` — `handleExport(w http.ResponseWriter, r *http.Request)`,
  registered at `services/gateway/main.go:425` as
  `POST /api/gateway/export`. Request body `{"start_date":"YYYY-MM-DD","end_date":"YYYY-MM-DD","data_type":"request_facts"|"service_events"}`
  (`services/gateway/main.go:1259-1263`). Requires JWT bearer auth via `gw.requireAuth` (the
  handler is wrapped by it at registration). Caps the range at 30 days
  (`services/gateway/main.go:1290-1293`: `"export range cannot exceed 30 days"`). Returns `404`
  `"no data found for the specified date range"` when no keys match
  (`services/gateway/main.go:1333`). Streams a `tar.gz` whose tar entry `Name` is set to the exact
  object-store key (`services/gateway/main.go:1373`: `hdr.Name = key`) — the key is never
  transformed before being written into the archive.
- `services/ingestion/main.go:123-128` — `topicForTenant(tenantID, baseTopic string) string`
  returns `tenantID + "/" + baseTopic`, e.g. `"ten_abc123/request_facts"`.
- `services/ingestion/main.go:484-487` — object keys are written as
  `fmt.Sprintf("raw/%s/%s/%s/%s", topic, dayStr, hourStr, filepath.Base(sourcePath))`, i.e.
  `raw/<tenant>/request_facts/YYYY-MM-DD/HH/<uuid>.jsonl`. Every exported tar entry name is
  therefore already `raw/<tenant>/<data_type>/YYYY-MM-DD/HH/<uuid>.jsonl`.
- `pkg/storage/local.go:17-26` — `NewLocalStore(baseDir string) (*LocalStore, error)` reads and
  writes keys as `filepath.Join(baseDir, key)`. Extracting a tar entry named
  `raw/<tenant>/request_facts/...` directly under a chosen `baseDir` makes it immediately visible
  to a `LocalStore` rooted at that `baseDir` — no transformation of the path is required.
- `transforms/request_metrics_minute/main.go:173` —
  `flag.StringVar(&inputDir, "input-dir", "./data/raw/request_facts", ...)`, and
  `transforms/request_metrics_minute/main.go:300` —
  `inputDir: fmt.Sprintf("./data/raw/%s/request_facts", t.ID)` for the per-tenant path. Pointing
  `-input-dir` at `<out-dir>/raw/<tenant>/request_facts` reproduces this exactly.
- `cmd/cli/main.go:1-69` — the `gravix` CLI's command dispatch. `send`, `status`, `tail`, `replay`
  are registered in a `switch os.Args[1]` in `main()`; each subcommand is implemented in its own
  `cmd_<name>.go` file (e.g. `cmd/cli/cmd_status.go`) with a `run<Name>(args []string)` function
  taking the remaining `os.Args` slice.
- `pkg/auth/jwt.go:35-41` — `Claims{TenantID, UserID, Email, Role}`; gateway sessions are JWT
  bearer tokens obtained via `POST /api/gateway/login` (not implemented by this spec — the
  operator supplies an already-obtained token).
- `docs/oss/specs/GRVX-1101-bare-parquet-access.md` §4.2 adds a DuckDB-CLI-install step to the
  `e2e` job in `.github/workflows/ci.yml` before "Run end-to-end tests", and that job runs
  `go test ./tests/e2e/... -v -count=1 -timeout 300s`. This spec's end-to-end test lives in that
  same package and is picked up by that same command with no further CI change.
- `go.mod:3` — `go 1.24.9`. `go.mod:1` — module `github.com/lgreene/gravix-dashboards`.

## 3. Non-goals for this spec

- Do NOT implement `GRVX-1404` (the reverse, self-host → Cloud direction).
- Do NOT modify `services/gateway/main.go`'s `handleExport`, its 30-day cap, or its auth model.
  This spec's CLI works around the 30-day cap by issuing multiple requests, not by changing the
  server.
- Do NOT transfer or reconstruct `data/warehouse/` Parquet directly from Cloud. Per
  `docs/00-system-truth.md` §4 (Recomputability), the warehouse is disposable and derived; this
  spec exports the raw facts, and the existing, unmodified rollup binary regenerates the warehouse
  on the self-host side. Building a second Parquet-transfer path would duplicate a capability the
  product's own philosophy says should not need to exist.
- Do NOT build a dashboard button for this. CLI only.
- Do NOT implement `docker compose up` auto-discovery of an already-populated `data/raw/`
  directory; `GRVX-901` (Phase 9, zero-config boot) already covers that, and this spec's output
  directory is deliberately laid out to satisfy it without further code.
- This spec does not cross non-goal §5 (`docs/04-non-goals.md`, No High-Cardinality Dimensions) —
  it moves already-validated facts unchanged; it introduces no new dimension.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/cli/cmd_migrate_export.go` | `runMigrateExportCloud`, `dateWindows`, `fetchExportWindow`, manifest writer |
| `cmd/cli/cmd_migrate_export_test.go` | Unit tests against a fake gateway (`httptest.Server`) |
| `tests/e2e/cloud_to_selfhost_migration_test.go` | `TestMigrationOutputReadableByDuckDB` — real export, real rollup binary, real `duckdb` CLI |
| `docs-site/docs/cloud-to-selfhost-migration.md` | The published, single-command migration guide |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/main.go` | Add a `"migrate"` case to the `switch os.Args[1]` in `main()`, dispatching `os.Args[2]` `"export-cloud"` to `runMigrateExportCloud(os.Args[3:])`; update `printUsage()` to list `gravix migrate export-cloud` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/main.go` | `handleExport` is reused exactly as it exists; this spec is a client of it |
| `transforms/request_metrics_minute/main.go` | Reused unmodified as the recompute step this spec's E2E test invokes |
| `pkg/storage/**` | `LocalStore`'s key layout is read-only context; not modified |
| `pkg/auth/**` | Token issuance and validation are unchanged; this spec only carries a caller-supplied token |

## 5. Interface contract

### 5.1 `cmd/cli/cmd_migrate_export.go`

```go
package main

// runMigrateExportCloud implements `gravix migrate export-cloud`.
func runMigrateExportCloud(args []string)

// dateWindow is one inclusive [Start, End] date range of at most maxDays days.
type dateWindow struct {
	Start time.Time
	End   time.Time
}

// dateWindows splits the inclusive range [since, until] into consecutive
// dateWindows of at most maxDays days each, in chronological order. The
// final window may be shorter than maxDays. Panics are not used; since must
// not be after until (the caller validates this before calling).
func dateWindows(since, until time.Time, maxDays int) []dateWindow

// fetchExportWindow calls POST <gatewayEndpoint>/api/gateway/export with
// body {"start_date","end_date","data_type"} set from w and dataType, header
// "Authorization: Bearer "+token, decodes the gzip+tar response body, and
// writes every tar entry to filepath.Join(outDir, hdr.Name), creating parent
// any missing parent directories. A 404 response is not an error: it returns (0, nil).
// A 429 response is retried exactly once after sleeping 5 seconds; a second
// 429 returns (0, ErrExportFailed). Any other non-200 status returns
// (0, ErrExportFailed) wrapped with the response body's "error" field.
func fetchExportWindow(ctx context.Context, client *http.Client, gatewayEndpoint, token, dataType string, w dateWindow, outDir string) (filesWritten int, err error)

// migrationManifest is written to <out-dir>/MIGRATION_MANIFEST.json after a
// successful export.
type migrationManifest struct {
	TenantID        string    `json:"tenant_id"`
	GatewayEndpoint string    `json:"gateway_endpoint"`
	Since           string    `json:"since"`
	Until           string    `json:"until"`
	DataTypes       []string  `json:"data_types"`
	FilesExported   int       `json:"files_exported"`
	ExportedAt      time.Time `json:"exported_at"`
}

var (
	ErrMissingTenantID  = errors.New("migrate export-cloud: --tenant-id is required")
	ErrMissingSince     = errors.New("migrate export-cloud: --since is required")
	ErrMissingToken     = errors.New("migrate export-cloud: --gateway-token is required (or set GRAVIX_GATEWAY_TOKEN)")
	ErrInvalidDateRange = errors.New("migrate export-cloud: --until must not be before --since")
	ErrExportFailed     = errors.New("migrate export-cloud: gateway export request failed")
)
```

### 5.2 CLI flags — `gravix migrate export-cloud`

`flag.NewFlagSet("migrate export-cloud", flag.ExitOnError)`:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--gateway-endpoint` | string | `http://localhost:8091` | Base URL of the Gravix Cloud gateway API |
| `--tenant-id` | string | `""` | Tenant ID to export (required) |
| `--gateway-token` | string | `""` | JWT bearer token for gateway auth; falls back to GRAVIX_GATEWAY_TOKEN env var if empty |
| `--since` | string | `""` | Earliest date to export, YYYY-MM-DD (required) |
| `--until` | string | today, UTC, YYYY-MM-DD | Latest date to export, YYYY-MM-DD, inclusive |
| `--out-dir` | string | `./gravix-export` | Directory to extract exported raw facts and service events into |
| `--data-types` | string | `request_facts,service_events` | Comma-separated data types to export |

## 6. Behaviour

1. Parse flags. If `--tenant-id` is empty, print `usage: gravix migrate export-cloud --tenant-id <id> --since <YYYY-MM-DD> [flags]` to stderr and exit `2`.
2. If `--since` is empty, print the same usage line and exit `2`.
3. Resolve the token: `--gateway-token`, else `os.Getenv("GRAVIX_GATEWAY_TOKEN")`. If both are empty, print `migrate export-cloud: --gateway-token is required (or set GRAVIX_GATEWAY_TOKEN)` to stderr and exit `2`.
4. Parse `--since` and `--until` as `"2006-01-02"`. If `--until` is before `--since`, print `migrate export-cloud: --until must not be before --since` to stderr and exit `2`.
5. Compute `windows := dateWindows(since, until, 30)`.
6. For each data type in `--data-types` (split on `,`, trimmed): for each window, call `fetchExportWindow`. Accumulate `filesWritten`. If `fetchExportWindow` returns a non-nil error, print `migrate export-cloud: <error>` to stderr and exit `1`.
7. After all windows and data types complete, write `migrationManifest{TenantID: tenantID, GatewayEndpoint: gatewayEndpoint, Since: since string, Until: until string, DataTypes: dataTypes, FilesExported: total, ExportedAt: time.Now().UTC()}` as indented JSON to `<out-dir>/MIGRATION_MANIFEST.json`.
8. Print `exported <n> files across <m> requests into <out-dir>` to stdout and exit `0`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `--tenant-id` empty | exit 2 | `usage: gravix migrate export-cloud --tenant-id <id> --since <YYYY-MM-DD> [flags]` |
| `--since` empty | exit 2 | `usage: gravix migrate export-cloud --tenant-id <id> --since <YYYY-MM-DD> [flags]` |
| Token unresolved | exit 2 | `migrate export-cloud: --gateway-token is required (or set GRAVIX_GATEWAY_TOKEN)` |
| `--until` before `--since` | exit 2 | `migrate export-cloud: --until must not be before --since` |
| Gateway returns 429 twice for one window | exit 1 | `migrate export-cloud: gateway export request failed` |
| Gateway returns 404 for a window | not an error; window contributes 0 files, loop continues | — |
| Gateway returns any other non-2xx status | exit 1 | `migrate export-cloud: gateway export request failed` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `dateWindows` splits a 65-day range into exactly 3 windows, each ≤30 days, covering the full range with no gap or overlap | `TestDateWindowsSplitsLongRange` |
| AC-2 | `fetchExportWindow` extracts a fake tar.gz response's entries to `outDir` at the exact relative path given by the entry's `Name` | `TestFetchExportWindowPreservesRawKeyLayout` |
| AC-3 | `runMigrateExportCloud` exits 2 when `--tenant-id` is empty | `TestMigrateExportRequiresTenantID` |
| AC-4 | `fetchExportWindow` retries exactly once after a 429 and succeeds on the second attempt | `TestFetchExportWindowRetriesOnRateLimit` |
| AC-5 | `fetchExportWindow` returns `(0, nil)` for a 404 response, not an error | `TestFetchExportWindowSkipsEmptyWindow` |
| AC-6 | `runMigrateExportCloud` writes `MIGRATION_MANIFEST.json` with `tenant_id`, `since`, and `until` matching the invocation's flags | `TestMigrateExportWritesManifest` |
| AC-7 | Running `gravix migrate export-cloud` against a fixture gateway, then the unmodified `rollup` binary against the exported directory, then the `duckdb` CLI against the resulting Parquet, returns a row count greater than zero | `TestMigrationOutputReadableByDuckDB` |

## 8. Verification

```bash
# 1. Unit tests
go test ./cmd/cli/... -run 'TestDateWindows|TestFetchExportWindow|TestMigrateExport' -v
# expect: PASS

# 2. Real end-to-end: export, recompute, read with a foreign tool
go test ./tests/e2e/... -run TestMigrationOutputReadableByDuckDB -v -count=1 -timeout 300s
# expect: PASS, printed row count > 0

# 3. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All seven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report, including the DuckDB row count from AC-7
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The report states, verbatim, the single command a customer runs to leave Cloud

## 10. Escalation

| If you find… | Do this |
|---|---|
| `handleExport`'s response shape has changed since §2 was written | Return `SPEC DEFECT: §2 — services/gateway/main.go:1251 shape mismatch` |
| The `e2e` job's DuckDB CLI install step (`GRVX-1101`) is not present when this spec is dispatched | Return `SPEC DEFECT: §2 — GRVX-1101 not yet merged` |
| A criterion cannot be met without modifying a file outside §4.1/§4.2 | Return `SPEC DEFECT: §4 — needs <path>` |
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
