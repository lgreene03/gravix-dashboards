# SPEC GRVX-1107: Data export in the free tier — Parquet, CSV, JSONL, manual and scheduled

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1107 | **Phase** | 11 | **Goal** | G5.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.3 rules export core: "Export is the anti-lock-in guarantee; gating it is hostage-taking." §7.3 Q5 = YES. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-710, GRVX-1101 |
| **Blocks** | GRVX-1109, GRVX-1403 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make export a complete, free, first-class capability: any time range, any format the user's tools
read, on demand or on a schedule, with no plan gate and no volume cap. A user who can leave easily
is a user who can stay by choice.

## 2. Context the implementer needs

- `services/gateway/enterprise.go` implements `/api/gateway/exports/scheduled` (Horizon 1 Phase 6.7): full CRUD, admin-only create/update/delete, 5-field cron validation, `s3://` destination required, `lookback_days` 1–90, formats jsonl/csv/parquet. Read it in full.
- `GRVX-710` removes its plan gate. This spec assumes the gate is already gone and must not reintroduce one.
- Storage layout: `data/raw/` JSONL facts, `data/warehouse/` Parquet metrics, both partitioned by `event_day`.
- `pkg/storage/` provides `ObjectStore` with local and S3 backends.
- `GRVX-1101` documents reading `data/warehouse/` with DuckDB directly, with no Gravix process.
- `docs/04-non-goals.md` §5 forbids per-request querying. Exporting **raw facts** is a bulk file operation, not a query interface, and is permitted — the distinction is that it returns files, never answers to per-request questions.

## 3. Non-goals for this spec

- Do NOT add a plan gate, a volume cap, a row limit, or a rate limit that exists to make export inconvenient. Charter §7.4.
- Do NOT build a query interface. Export takes a time range and a dataset; it takes no filter expression, no predicate, and no `WHERE` clause.
- Do NOT require S3. Local filesystem destinations must work, because the bootstrap stack has no object store.
- Do NOT change the on-disk formats.
- Do NOT implement continuous warehouse sync. That is `ee/warehouse/` (GRVX-1311) and is a different thing: managed, incremental, schema-tracked.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/export/export.go` | The export engine |
| `pkg/export/export_test.go` | Tests |
| `pkg/export/format.go` | Parquet, CSV, JSONL writers |
| `pkg/export/format_test.go` | Tests |
| `cmd/cli/cmd_export.go` | `gravix export` |
| `cmd/cli/cmd_export_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/gateway/enterprise.go` | Route scheduled exports through `pkg/export`; allow `file://` destinations; remove the admin-only restriction on **read**, keeping it on create/update/delete |
| `services/gateway/main.go` | Register `POST /api/gateway/exports` for on-demand export. Change no existing route. |
| `docs/openapi.yaml` | Document the on-demand endpoint |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `transforms/**`, `services/ingestion/**` | Export reads; it never writes to the pipeline |
| `pkg/tenantdb/migrations/**` | `scheduled_exports` already exists |
| `cmd/purge/**` | Retention unchanged |

## 5. Interface contract

### 5.1 `pkg/export/export.go`

```go
// Package export writes Gravix data to open formats. It is a bulk file
// operation, not a query interface: it takes a dataset and a time range and
// nothing else, which is what keeps it clear of non-goal §5.
package export

// Dataset is what to export.
type Dataset string

const (
    // DatasetFacts exports raw RequestFacts — the recompute source of truth.
    DatasetFacts Dataset = "facts"
    // DatasetMetrics exports rolled-up minute metrics.
    DatasetMetrics Dataset = "metrics"
    // DatasetEvents exports ServiceEvents.
    DatasetEvents Dataset = "events"
)

// Format is the output encoding.
type Format string

const (
    FormatParquet Format = "parquet"
    FormatCSV     Format = "csv"
    FormatJSONL   Format = "jsonl"
)

// Request is one export.
type Request struct {
    Dataset     Dataset
    Format      Format
    From        time.Time // inclusive
    To          time.Time // exclusive
    TenantID    string    // empty for single-tenant
    Destination string    // "file:///path", "s3://bucket/prefix", or "-" for stdout
    Compress    bool      // gzip for csv and jsonl; parquet is already compressed
}

// Result reports what was written.
type Result struct {
    Files       []string  `json:"files"`
    Rows        int64     `json:"rows"`
    BytesWritten int64    `json:"bytes_written"`
    Manifest    string    `json:"manifest"`
    Duration    time.Duration `json:"duration"`
}

// Run executes an export. It streams: memory use is bounded by one partition,
// not by the size of the range, so exporting 90 days does not need 90 days of RAM.
func Run(ctx context.Context, store storage.ObjectStore, req Request) (*Result, error)

var (
    ErrUnknownDataset = errors.New("export: unknown dataset")
    ErrUnknownFormat  = errors.New("export: unknown format")
    ErrEmptyRange     = errors.New("export: To must be after From")
    ErrBadDestination = errors.New("export: destination must be file://, s3://, or -")
    ErrNoData         = errors.New("export: no data in range")
)
```

### 5.2 The export manifest

Every export writes a `manifest.json` beside its files:

```json
{
  "schema_version": 1,
  "exported_at": "2026-09-10T09:15:00Z",
  "gravix_version": "v1.4.2",
  "dataset": "facts",
  "format": "parquet",
  "from": "2026-08-11T00:00:00Z",
  "to": "2026-09-10T00:00:00Z",
  "files": ["facts_20260811.parquet"],
  "rows": 41203991,
  "schema": {"event_id": "string", "event_time": "timestamp", "…": "…"},
  "how_to_read": "duckdb -c \"SELECT * FROM read_parquet('facts_*.parquet') LIMIT 10;\""
}
```

The `how_to_read` field is the point. An export that arrives without instructions for opening it
outside Gravix is only technically an export.

### 5.3 CLI

`gravix export` with:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--dataset` | `string` | `metrics` | `facts, metrics, or events` |
| `--format` | `string` | `parquet` | `parquet, csv, or jsonl` |
| `--from` | `string` | *(required)* | `start of range, RFC3339 or YYYY-MM-DD` |
| `--to` | `string` | *(required)* | `end of range, exclusive` |
| `--out` | `string` | `-` | `destination: file:///path, s3://bucket/prefix, or - for stdout` |
| `--tenant` | `string` | `""` | `tenant id; empty for single-tenant` |
| `--compress` | `bool` | `false` | `gzip csv and jsonl output` |

### 5.4 `POST /api/gateway/exports`

Body is the `Request` fields. Roles: **any authenticated role may export**. Export is how a user
retrieves their own data; restricting it to admins would make a viewer unable to leave with the data
they can already see on screen.

Status codes: `202` accepted with a job id; `400` invalid field, naming it; `401`; `404` unknown
dataset; `422` no data in range; `429`. **No `403` for plan.** A `403` on this endpoint would be a
charter violation and `make check-boundary` will flag any `requirePlan` added here.

## 6. Behaviour

1. Read `services/gateway/enterprise.go` in full; record the scheduled-export implementation and every validation rule it enforces.
2. Implement `pkg/export` with streaming, bounded by one partition.
3. Implement all three format writers. CSV writes a header row; JSONL writes one object per line; Parquet reuses the existing schema.
4. Write the §5.2 manifest on every export, including `how_to_read`.
5. Add `file://` destination support so the bootstrap stack works.
6. Route scheduled exports through the same engine, so the two paths cannot diverge.
7. Keep create/update/delete admin-only; make **read** available to any role.
8. Add the on-demand endpoint with no plan gate.
9. Verify exported Parquet opens in DuckDB with no Gravix process running.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Unknown dataset | 400 | `invalid field "dataset": must be facts, metrics, or events` |
| Unknown format | 400 | `invalid field "format": must be parquet, csv, or jsonl` |
| `to` not after `from` | 400 | `invalid field "to": must be after from` |
| Bad destination scheme | 400 | `invalid field "destination": must be file://, s3://, or -` |
| No data in range | 422 | `no data in range <from> .. <to>` |
| Destination not writable | 500 | `cannot write to <destination>: <err>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | All three datasets export in all three formats | `TestExportAllDatasetsAllFormats` |
| AC-2 | Exported Parquet opens in DuckDB with no Gravix process | `TestExportedParquetReadableExternally` |
| AC-3 | Exported CSV opens in a stock CSV reader | `TestExportedCSVReadableExternally` |
| AC-4 | Every export writes a manifest containing `how_to_read` | `TestExportManifestIncludesHowToRead` |
| AC-5 | Memory use is bounded by one partition, not the range | `TestExportStreamsWithinMemoryBound` |
| AC-6 | `file://` destinations work without any object store | `TestExportToLocalFilesystem` |
| AC-7 | No plan gate on any export route | `TestExportHasNoPlanGate` |
| AC-8 | Any authenticated role may export | `TestAnyRoleCanExport` |
| AC-9 | Create/update/delete of a schedule stays admin-only | `TestScheduleMutationStaysAdminOnly` |
| AC-10 | Scheduled and on-demand exports produce identical output | `TestScheduledAndOnDemandAgree` |
| AC-11 | No filter, predicate, or query expression is accepted | `TestExportAcceptsNoQueryExpression` |
| AC-12 | Every pre-existing scheduled-export validation still holds | `TestExistingScheduleValidationIntact` |

## 8. Verification

```bash
# 1. Round trip through an external reader — the anti-lock-in proof
go test ./pkg/export/... -run 'TestExportedParquetReadableExternally|TestExportedCSVReadableExternally' -v
# expect: PASS

# 2. Charter: export is free
go test ./services/gateway/... -run 'TestExportHasNoPlanGate|TestAnyRoleCanExport' -v
grep -n "requirePlan" services/gateway/enterprise.go || echo "no plan gate on export"
# expect: PASS; no plan gate on export

# 3. Not a query interface
go test ./pkg/export/... -run TestExportAcceptsNoQueryExpression -v
# expect: PASS

# 4. Streaming
go test ./pkg/export/... -run TestExportStreamsWithinMemoryBound -v
# expect: PASS

# 5. Bootstrap stack works
go test ./pkg/export/... -run TestExportToLocalFilesystem -v
# expect: PASS

# 6. Nothing regressed
go test ./services/gateway/... -run TestExistingScheduleValidationIntact -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every pre-existing scheduled-export validation rule listed and verified intact
- [ ] No `requirePlan` on any export route
- [ ] Exported files verified readable by a tool that is not Gravix
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to cap export volume or gate it by plan | Refuse, citing charter §2.3. Route to `license-boundary-auditor`. |
| A request to add a filter expression | Refuse, citing non-goal §5 and §3. Export takes a range; querying is what SQL over the Parquet is for. |
| An export unreadable outside Gravix | STOP. `CORRECTNESS DEFECT` — this defeats the feature's entire purpose. |
