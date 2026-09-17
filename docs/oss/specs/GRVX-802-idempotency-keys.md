# SPEC GRVX-802: Add idempotency keys and a content digest to rollup output

| Field | Value |
|---|---|
| **Spec ID** | GRVX-802 |
| **Phase** | 8 | **Goal** | G2.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. Without an idempotency key a user cannot tell whether two files describe the same window. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-801 |
| **Blocks** | GRVX-805, GRVX-807, GRVX-810 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give every rollup output a stable **idempotency key** identifying the window and inputs it was
derived from, and a **content digest** over its rows. Together they let any consumer answer two
questions without reading the data: *is this the same window as that one*, and *did the contents
change*. This is the mechanism GRVX-805 uses to detect a revision and GRVX-807 uses to prove lineage.

## 2. Context the implementer needs

- `pkg/recompute/` exists (GRVX-801) with `DeterministicKey` and byte-identical output.
- `transforms/request_metrics_minute/main.go:75-88` — `MetricRow`, 12 fields, written as Parquet with `parquet.NewGenericWriter[MetricRow]`.
- Warehouse layout is Hive-partitioned: `warehouse/request_metrics_minute/event_day=YYYY-MM-DD/`.
- `transforms/compaction/main.go` merges small Parquet files into ~128 MB targets. Compaction must
  preserve or recompute the manifest, or the manifest becomes wrong after a compaction run.
- `cube/model/schema/RequestMetricsMinute.js:4-8` reads with
  `read_parquet('/cube/data/warehouse/request_metrics_minute/**/*.parquet', union_by_name=true)`.
  A manifest file placed inside that glob would be read as data — so manifests must not match `*.parquet`.
- `docs/00-system-truth.md` §1 requires a Fact to carry a schema version. Derived rows have no
  version field today.

## 3. Non-goals for this spec

- Do NOT change how percentiles are computed. GRVX-804.
- Do NOT implement revision detection behaviour. This spec provides the key and digest; GRVX-805 acts on them.
- Do NOT change the Cube model. GRVX-808.
- Do NOT add lineage query output. GRVX-807.
- Do NOT place any file matching `*.parquet` in the warehouse that is not metric data.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/manifest/manifest.go` | Manifest type, idempotency key and digest computation |
| `pkg/manifest/manifest_test.go` | Tests |
| `pkg/manifest/testdata/golden_manifest.json` | Golden fixture pinning the format |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/recompute/recompute.go` | Write a manifest beside each Parquet output |
| `transforms/request_metrics_minute/main.go` | Write a manifest on the cron path too, using the same function |
| `transforms/compaction/main.go` | Recompute the manifest after merging; a merged file's manifest lists every source idempotency key |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cube/model/**` | GRVX-808 |
| `schemas/**` | Fact schema unchanged |
| `services/ingestion/**` | Manifests describe derivatives, not facts |

## 5. Interface contract

### 5.1 `pkg/manifest/manifest.go`

```go
// Package manifest describes a derived metric file: what window it covers, what
// inputs produced it, and a digest of its contents.
package manifest

// SchemaVersion is the manifest format version. Bump on any field change.
const SchemaVersion = 1

// Manifest sits beside a metric Parquet file and describes it.
type Manifest struct {
    SchemaVersion  int      `json:"schema_version"`
    Metric         string   `json:"metric"`
    MetricVersion  string   `json:"metric_version"`
    IdempotencyKey string   `json:"idempotency_key"`
    ContentDigest  string   `json:"content_digest"`
    TenantID       string   `json:"tenant_id"`
    EventDay       string   `json:"event_day"`
    WindowFrom     string   `json:"window_from"`
    WindowTo       string   `json:"window_to"`
    RowCount       int64    `json:"row_count"`
    FactCount      int64    `json:"fact_count"`
    SourceFactKeys []string `json:"source_fact_keys"`
    Revision       int      `json:"revision"`
    DataFile       string   `json:"data_file"`
}

// IdempotencyKey returns the stable identity of a derived partition. It depends
// only on what the partition IS, never on when or how it was computed.
func IdempotencyKey(metric, metricVersion, tenantID string, day time.Time) string

// ContentDigest returns a digest over row contents. Two partitions with the same
// rows in the same order have the same digest, on any machine.
func ContentDigest(rows any) (string, error)

// Path returns the manifest path for a data file path.
// It never returns a path ending in ".parquet".
func Path(dataFile string) string

// Write serialises m to store at Path(m.DataFile).
func Write(ctx context.Context, store storage.ObjectStore, m *Manifest) error

// Read loads the manifest for a data file. Returns ErrNoManifest when absent.
func Read(ctx context.Context, store storage.ObjectStore, dataFile string) (*Manifest, error)

var (
    ErrNoManifest      = errors.New("manifest: no manifest for data file")
    ErrSchemaTooNew    = errors.New("manifest: schema version is newer than this binary supports")
    ErrDigestMismatch  = errors.New("manifest: content digest does not match data file")
)
```

### 5.2 Exact key and digest construction

```
IdempotencyKey = "<metric>:<metricVersion>:<tenantID>:<YYYYMMDD>"
```
with `tenantID` rendered as the literal `_single` when empty, so the key never contains an empty
segment. Example: `request_metrics_minute:v1:_single:20260909`.

```
ContentDigest = "sha256:" + hex(sha256(canonical))
```
where `canonical` is produced by, in order:
1. Marshalling each row to JSON with **map keys sorted** and no whitespace.
2. Joining the row JSON documents with `\n`, in the file's existing row order.
3. Encoding as UTF-8.

The digest covers **row content only** — never the Parquet container, its compression, or its
metadata. This is deliberate: a Parquet library upgrade that changes byte layout must not look like
a data change. Byte-identity of the container is GRVX-801's concern; content identity is this spec's.

### 5.3 Manifest path

```go
func Path(dataFile string) string {
    return strings.TrimSuffix(dataFile, ".parquet") + ".manifest.json"
}
```

For `.../request_metrics_minute_20260909.parquet` the manifest is
`.../request_metrics_minute_20260909.manifest.json`. It does not match `*.parquet`, so Cube's
`read_parquet` glob will not ingest it.

### 5.4 Compaction behaviour

When compaction merges files `A`, `B`, `C` into `M`:
- `M`'s `IdempotencyKey` is computed from `M`'s own metric, version, tenant and day.
- `M`'s `SourceFactKeys` is the sorted union of `A`, `B` and `C`'s `SourceFactKeys`.
- `M`'s `FactCount` is the sum; `RowCount` is `M`'s actual row count.
- `M`'s `Revision` is the **maximum** of the sources' revisions.
- `M`'s `ContentDigest` is computed over `M`'s rows.
- The source manifests are deleted with their data files, in that order: data file first, then
  manifest. A manifest without its data file is a detectable inconsistency; a data file without a
  manifest is not.

## 6. Behaviour

1. Implement `pkg/manifest` per §5.
2. Write `testdata/golden_manifest.json` and assert the serialised form matches it byte-for-byte, so
   an accidental field rename or reordering fails a test rather than silently changing the format.
3. In `pkg/recompute.Run`, after computing rows and before writing, build the manifest: set
   `MetricVersion` to `"v1"`, `Revision` to `0` for a first write or the existing manifest's
   `Revision` when the digest is unchanged, `SourceFactKeys` to the sorted list of raw fact object
   keys read for the partition, and `FactCount` to the number of facts read.
4. Write the data file, then the manifest. Never the reverse.
5. Do the same on the cron path in `transforms/request_metrics_minute/main.go`, calling the same
   function so the two paths cannot drift.
6. Update `transforms/compaction/main.go` per §5.4.
7. Verify Cube still reads the warehouse: the manifest must not appear in `read_parquet` results.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Manifest absent for a data file | `Read` returns `ErrNoManifest` | `manifest: no manifest for data file <path>` |
| Manifest `schema_version` > `SchemaVersion` | `Read` returns `ErrSchemaTooNew` | `manifest: schema version <n> is newer than this binary supports (<m>)` |
| Digest does not match the data file's rows | `ErrDigestMismatch` | `manifest: content digest does not match data file <path>` |
| Manifest write fails after the data write | leave the data file, return the error | `manifest: write <path>: <err>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The same partition always yields the same idempotency key | `TestIdempotencyKeyIsStable` |
| AC-2 | An empty tenant renders as `_single`, never an empty segment | `TestIdempotencyKeyHandlesSingleTenant` |
| AC-3 | Identical rows yield identical digests across processes | `TestContentDigestIsStable` |
| AC-4 | One changed value changes the digest | `TestContentDigestDetectsChange` |
| AC-5 | Re-writing with a different Parquet compression level leaves the digest unchanged | `TestDigestIgnoresContainerFormat` |
| AC-6 | `Path` never returns a `.parquet` path | `TestManifestPathNotParquet` |
| AC-7 | The manifest is not matched by Cube's `**/*.parquet` glob | `TestManifestExcludedFromParquetGlob` |
| AC-8 | Serialised form matches the golden fixture byte-for-byte | `TestManifestGoldenFormat` |
| AC-9 | A newer schema version is refused, not silently misread | `TestManifestRejectsNewerSchema` |
| AC-10 | Recompute writes a manifest beside every data file | `TestRecomputeWritesManifest` |
| AC-11 | The cron rollup writes an identical manifest for the same inputs | `TestCronAndRecomputeManifestsMatch` |
| AC-12 | Compaction unions source fact keys and takes the max revision | `TestCompactionMergesManifests` |
| AC-13 | Compaction deletes the data file before its manifest | `TestCompactionDeleteOrder` |
| AC-14 | The data file is written before the manifest | `TestWriteOrderDataThenManifest` |

## 8. Verification

```bash
# 1. Key and digest stability
go test ./pkg/manifest/... -run 'TestIdempotencyKey|TestContentDigest|TestDigestIgnores' -v
# expect: PASS

# 2. Format is pinned
go test ./pkg/manifest/... -run TestManifestGoldenFormat -v
# expect: PASS

# 3. Manifests are invisible to Cube
go test ./pkg/manifest/... -run TestManifestExcludedFromParquetGlob -v
ls data/warehouse/request_metrics_minute/*/*.parquet 2>/dev/null | grep -c manifest || true
# expect: PASS, and 0

# 4. Both write paths agree
go test ./pkg/recompute/... ./transforms/request_metrics_minute/... -run 'Manifest' -v
# expect: PASS

# 5. Compaction
go test ./transforms/compaction/... -v
# expect: PASS

# 6. Coverage and full suite
go test ./pkg/manifest/... -cover
go test ./... 2>&1 | tail -20
# expect: coverage >= 95%; no failures

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] No `.parquet`-matching non-data file exists in the warehouse
- [ ] `pkg/manifest` coverage ≥95%
- [ ] The golden fixture committed
- [ ] `docs-engineer` delta merged; `docs/03-storage-layout.md` documents the manifest
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Cube reading the manifest as data | Return `SPEC DEFECT: §5.3 — glob collision` |
| Compaction unable to preserve source fact keys | Return `SPEC DEFECT: §5.4 — <cause>`. Losing lineage on compaction would break GRVX-807. |
| An existing warehouse file with no manifest | Report `finding: <n> pre-existing files lack manifests` for GRVX-810. Do not backfill silently. |
