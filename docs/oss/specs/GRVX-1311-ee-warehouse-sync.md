# SPEC GRVX-1311: `ee/warehouse/` — continuous sync to Snowflake, BigQuery and Databricks

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1311 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §2.3 settles it: **export is core** because Q5 = YES — gating it is hostage-taking. Continuous warehouse sync with schema management is **ee** because Q1 = NO: it is operational convenience for enterprise data teams, and everything it does can be done manually with the free export. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-1107, GRVX-1201 |
| **Blocks** | none |
| **Effort** | 7 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Keep a customer's data warehouse continuously in step with Gravix — incrementally, with schema
evolution handled and late-arriving revisions reconciled — while the free manual export remains
capable of producing the same data, just with more effort.

## 2. Context the implementer needs

- `pkg/export` (GRVX-1107) is core and free: Parquet, CSV and JSONL, manual and scheduled, any range, no cap. Anything this package produces must be reachable through it too, given enough manual work.
- `pkg/manifest` (GRVX-802, extended by GRVX-805) carries `IdempotencyKey`, `ContentDigest`, `Revision` and `PreviousDigest`. Sync uses these to detect what changed rather than re-reading everything.
- **GRVX-805 is why this is hard.** A partition can be *revised* when late facts arrive. A sync that only appends would leave a warehouse permanently disagreeing with Gravix about a historical bucket.
- `GRVX-1201`'s exporter plugin interface exists; warehouse sync is a first-party consumer of it.
- Charter §7.4 forbids requiring a network call for core operation, which this respects because sync is entirely within `ee/`.

## 3. Non-goals for this spec

- Do NOT make the free export less capable to differentiate this. §7.3 Q4 and GRVX-1107 are settled.
- Do NOT let a warehouse outage affect ingestion, rollup, alerting, or dashboards.
- Do NOT write to a warehouse table Gravix does not own. Sync creates and manages its own tables and refuses to touch pre-existing ones it did not create.
- Do NOT append revised partitions without reconciling. A warehouse that silently disagrees with Gravix is worse than no sync.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/warehouse/sync.go` | Incremental sync engine |
| `ee/warehouse/sync_test.go` | Tests |
| `ee/warehouse/schema.go` | Schema creation and evolution |
| `ee/warehouse/schema_test.go` | Tests |
| `ee/warehouse/reconcile.go` | Revision reconciliation |
| `ee/warehouse/reconcile_test.go` | Tests |
| `ee/warehouse/targets/` | Snowflake, BigQuery, Databricks adapters |
| `ee/warehouse/register.go` | Extension-point registration |
| `ee/warehouse/README.md` | What this adds over the free export |

## 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/export/**` | Free export is settled by GRVX-1107 |
| `pkg/manifest/**` | Manifest semantics are settled by GRVX-802 and GRVX-805 |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 What this adds over free export

`ee/warehouse/README.md` opens verbatim:

```
You can already do this by hand.

gravix export writes Parquet, CSV or JSONL for any range, free, with no cap, and
your warehouse can load those files on a schedule you write yourself. That path
is not going away and is not being made worse.

What this package adds is the part that is tedious rather than the part that is
possible: tracking what has already been loaded, creating and evolving the target
schema, and — the genuinely hard part — reconciling partitions that Gravix
revised after a late fact arrived, so your warehouse never quietly disagrees
with your dashboard.
```

Naming the free alternative in the paid feature's own README is the charter working as intended.

### 5.2 Incremental sync keyed on the manifest

```go
// Package warehouse continuously synchronises Gravix data to an external data
// warehouse. It tracks state by partition idempotency key and content digest,
// so a sync run transfers only what changed.
package warehouse

// SyncState is what has been loaded, per partition.
type SyncState struct {
    IdempotencyKey string    `json:"idempotency_key"`
    ContentDigest  string    `json:"content_digest"`
    Revision       int       `json:"revision"`
    LoadedAt       time.Time `json:"loaded_at"`
    TargetTable    string    `json:"target_table"`
    RowCount       int64     `json:"row_count"`
}

// Action is what a sync run decided to do with a partition.
type Action string

const (
    ActionSkip      Action = "skip"       // digest unchanged
    ActionInsert    Action = "insert"     // new partition
    ActionReconcile Action = "reconcile"  // digest changed: revision handling
)

// Plan decides the action for every partition in range, writing nothing.
func Plan(ctx context.Context, target Target, from, to time.Time) ([]PartitionAction, error)

// Sync executes a plan.
func Sync(ctx context.Context, target Target, plan []PartitionAction) (*SyncReport, error)
```

### 5.3 Revision reconciliation — the hard part, specified exactly

When a partition's `ContentDigest` differs from the loaded `SyncState`, the partition was revised
(GRVX-805). The sync must make the warehouse agree with Gravix, atomically:

1. Load the new partition data into a staging table named `<table>_stg_<idempotency key hash>`.
2. In **one transaction**: delete existing rows for that partition's `event_day` and tenant, insert
   the staged rows, and update `SyncState`.
3. Drop the staging table.
4. Record the reconciliation in the sync report with the old and new digests and the row delta.

If the target cannot do steps 1–3 transactionally, the adapter must say so at registration time and
the sync refuses to run against it, rather than performing a non-atomic delete-then-insert that
leaves the warehouse briefly missing a day of data.

**Never** `UPDATE` individual rows. Partition-level replace is the only correct operation, because
a revision can change row counts, not just values.

### 5.4 Schema evolution

| Change | Handling |
|---|---|
| New column added by Gravix | `ALTER TABLE ADD COLUMN`, nullable, backfilled null |
| Column type widened | `ALTER TABLE` where the target supports it; otherwise refuse and report |
| Column removed by Gravix | **Retained** in the warehouse, nullable. Never dropped: a customer may have built reports on it. |
| Metric version bump | New table `<table>_v<n>`; the old table is retained and no longer written |

Never dropping a column is a deliberate asymmetry. Gravix removing a column is a Gravix decision;
destroying a customer's downstream report is not ours to make.

### 5.5 Isolation from the data path

Sync runs as an `ee/` background job reading stored partitions. A warehouse outage, credential
failure, or schema conflict must produce **no** effect on ingestion, rollup, alerting, dashboards or
the free export. Failures are recorded in the sync report and raise an `ee/` alert.

## 6. Behaviour

1. Implement `Plan` and `Sync` keyed on `IdempotencyKey` and `ContentDigest`.
2. Implement reconciliation per §5.3, refusing non-transactional targets.
3. Implement schema evolution per §5.4, never dropping a column.
4. Implement the three target adapters, each declaring its transactional capability.
5. Refuse to write to any table the sync did not create — check for a Gravix-written table marker.
6. Apply `degrade.Guard` to sync configuration; already-scheduled syncs continue per GRVX-1303 §5.2.
7. Verify a warehouse outage has no effect on any core path.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Warehouse unreachable | retry with backoff; no core effect | `warehouse: <target> unreachable; <n> partitions pending` |
| Target lacks transactional replace | refuse to sync | `warehouse: <target> cannot replace a partition atomically; refusing to sync` |
| Pre-existing table not created by Gravix | refuse | `warehouse: table <name> was not created by Gravix; refusing to write to it` |
| Type widening unsupported | refuse that column, continue others, report | `warehouse: <target> cannot widen <column> from <a> to <b>` |
| Digest changed | reconcile per §5.3 | `warehouse: partition <key> revised (<old> -> <new>); reconciled <n> rows` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Unchanged partitions are skipped, not re-transferred | `TestUnchangedPartitionsSkipped` |
| AC-2 | A revised partition is reconciled so the warehouse matches Gravix exactly | `TestRevisedPartitionReconciled` |
| AC-3 | Reconciliation is atomic; the warehouse is never briefly missing a day | `TestReconciliationIsAtomic` |
| AC-4 | A non-transactional target is refused | `TestNonTransactionalTargetRefused` |
| AC-5 | No individual-row `UPDATE` is ever issued | `TestNoRowLevelUpdates` |
| AC-6 | A removed column is retained in the warehouse | `TestRemovedColumnRetained` |
| AC-7 | A metric version bump creates a new table and retains the old | `TestVersionBumpCreatesNewTable` |
| AC-8 | A pre-existing foreign table is never written to | `TestForeignTableRefused` |
| AC-9 | A warehouse outage has no effect on ingestion, rollup, alerting or dashboards | `TestWarehouseOutageDoesNotAffectCore` |
| AC-10 | The free export produces equivalent data | `TestFreeExportEquivalence` |
| AC-11 | The README's opening statement appears verbatim | `TestReadmeNamesFreeAlternative` |
| AC-12 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-13 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. The hard part
go test ./ee/warehouse/... -run 'TestRevisedPartitionReconciled|TestReconciliationIsAtomic|TestNoRowLevelUpdates' -v
# expect: PASS

# 2. Safety around customer data
go test ./ee/warehouse/... -run 'TestRemovedColumnRetained|TestForeignTableRefused|TestNonTransactionalTargetRefused' -v
# expect: PASS

# 3. Sync cannot hurt the core
go test ./ee/warehouse/... -run TestWarehouseOutageDoesNotAffectCore -v
# expect: PASS

# 4. The free path remains sufficient
go test ./ee/warehouse/... -run TestFreeExportEquivalence -v
grep -c "You can already do this by hand." ee/warehouse/README.md
# expect: PASS; 1

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Reconciliation demonstrated against a real revised partition
- [ ] Each target adapter's transactional capability recorded
- [ ] The README names the free alternative verbatim
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A target that cannot replace a partition atomically | Refuse it and report. Non-atomic replace means a customer's dashboard shows a missing day at some point, which is worse than no sync. |
| Pressure to weaken the free export | Refuse, citing §7.3 Q4 and GRVX-1107. Route to `license-boundary-auditor`. |
| A revision that cannot be reconciled | STOP. `CORRECTNESS DEFECT`. A warehouse that disagrees with Gravix undermines every Phase 8 guarantee downstream. |
