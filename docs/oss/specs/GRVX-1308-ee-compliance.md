# SPEC GRVX-1308: `ee/compliance/` — SIEM streaming, retention holds, SOC 2 evidence

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1308 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §2.3 settles this: the **local, queryable audit log is core** because Q3 = YES (baseline security). Streaming it to a SIEM is **ee** because Q1 = NO — it only matters when a compliance team demands it. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-1306 |
| **Blocks** | GRVX-1407 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give a compliance function what it needs — audit events streamed to its SIEM, legal retention holds,
and machine-readable SOC 2 evidence — **without the free tier losing the audit log it already has**.

## 2. Context the implementer needs

- Horizon 1 Phase 4.3 shipped an immutable audit log at `/api/gateway/audit-log`, with all mutations writing via `AuditLog().Log()`. It was admin-only.
- `GRVX-710` removed its **plan** gate. It remains admin-only by role, which is correct. The local audit log is free, permanently, under §7.3 Q4 and §2.3.
- `docs/00-system-truth.md` §2 permits fact deletion only for legal compliance or retention policy; a retention hold is the mechanism that **prevents** the retention path from deleting.
- `cmd/purge/` implements 30-day purging.
- `GRVX-1306` provides identity for attributing audit events.
- `docs/04-non-goals.md` §2 forbids a log platform. Streaming Gravix's own audit events to someone else's SIEM is not building one.

## 3. Non-goals for this spec

- Do NOT gate, cripple, or degrade the local audit log. It is free and stays free.
- Do NOT build a log search platform. Non-goal §2. This forwards Gravix's own audit events to a customer's existing SIEM and does nothing else.
- Do NOT let a retention hold be bypassed by the purge job. A hold that purging can ignore is not a hold.
- Do NOT edit any core file.
- Do NOT auto-generate compliance claims. Evidence collection is not attestation.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/compliance/siem.go` | Audit-event streaming |
| `ee/compliance/siem_test.go` | Tests |
| `ee/compliance/hold.go` | Legal retention holds |
| `ee/compliance/hold_test.go` | Tests |
| `ee/compliance/evidence.go` | SOC 2 evidence collection |
| `ee/compliance/evidence_test.go` | Tests |
| `ee/compliance/register.go` | Extension-point registration |
| `ee/compliance/README.md` | What is here, and what stays free |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | The purge job must already expose a retention-hold extension point; if it does not, return `EXTENSION POINT REQUIRED` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/` audit-log handler | Free and staying free |
| `cmd/purge/**` | Holds register through an extension point, never by editing purge |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 What stays free, stated in the README verbatim

```
The audit log is free.

Every mutation Gravix performs is recorded in a local, immutable, queryable
audit log, in the Apache-2.0 core, with no licence required. It shipped that way
in Horizon 1 and charter §7.3 Q4 makes that permanent.

What is in this package is forwarding those events to a SIEM you already run,
placing legal holds on data, and collecting evidence in the shapes an auditor
asks for. If you do not have a compliance function demanding those things, you
are not missing anything.
```

### 5.2 SIEM streaming

```go
// Package compliance implements Gravix Enterprise compliance integrations.
// The local audit log itself is core and free; this package forwards it.
package compliance

// Sink is a SIEM destination.
type Sink string

const (
    SinkSplunkHEC   Sink = "splunk_hec"
    SinkSyslogRFC5424 Sink = "syslog_rfc5424"
    SinkWebhookJSON Sink = "webhook_json"
    SinkS3JSONL     Sink = "s3_jsonl"
)

// Stream forwards audit events to a sink. It is at-least-once: a duplicate
// audit event in a SIEM is harmless, a missing one is a finding.
func Stream(ctx context.Context, tenantID string, s Sink, cfg SinkConfig) error

// ErrSinkUnreachable is returned when a sink cannot be written to.
// It never propagates to the operation that generated the audit event.
var ErrSinkUnreachable = errors.New("compliance: sink unreachable")
```

**Streaming failure never blocks the audited operation.** If Splunk is down, the mutation still
happens, the local audit log still records it, and the forwarder queues and retries. An audit
forwarder that can block writes turns a compliance feature into an availability risk.

Events are buffered durably on disk so a restart during a SIEM outage does not lose them, with a
bounded queue that drops **oldest-first** and records the drop count as its own audit event —
because silently losing audit events is the one failure a compliance customer cannot accept
undisclosed.

### 5.3 Retention holds

```go
// Hold prevents deletion of data in a time range for a stated legal reason.
type Hold struct {
    ID        string    `json:"id"`
    TenantID  string    `json:"tenant_id"`
    From      time.Time `json:"from"`
    To        time.Time `json:"to"`
    Reason    string    `json:"reason"`     // required, free text, recorded in the audit log
    PlacedBy  string    `json:"placed_by"`
    PlacedAt  time.Time `json:"placed_at"`
    ReleasedAt *time.Time `json:"released_at"`
}

// Place creates a hold. Placing and releasing are both audited events.
func Place(ctx context.Context, h Hold) error

// Covers reports whether any active hold covers a partition. The purge job
// consults this through a core extension point before deleting anything.
func Covers(ctx context.Context, tenantID string, day time.Time) (bool, error)
```

Behaviour:
- The purge job calls `Covers` before every deletion. A held partition is skipped and the skip is logged.
- With `ee/` absent, the extension point has no registration and `Covers` is effectively `false` — purging behaves exactly as it does today.
- A hold **cannot be released by the person who placed it alone** if the tenant has more than one admin; it requires a second admin. Single-admin tenants get a warning recorded in the audit log instead of a block, because blocking would strand them.
- Holds survive licence expiry: in `StateReadOnly`, existing holds remain **in force** and cannot be released. Expiry must never become a way to delete held data.

### 5.4 SOC 2 evidence

Collects, per period: access-review exports (who had what role, when), change-management records
(merged PRs, releases, approvals), backup verification results, and incident records. Output is
machine-readable JSON plus a rendered summary.

`ee/compliance/README.md` states verbatim:

```
Evidence collection is not attestation.

This package gathers records an auditor will ask for. It does not assert that
Gravix is SOC 2 compliant, that your deployment is, or that the evidence is
sufficient. Those are your auditor's judgements, not this tool's output.
```

## 6. Behaviour

1. Confirm a retention-hold extension point exists in the purge path. If not, return `EXTENSION POINT REQUIRED`.
2. Implement streaming with durable buffering, at-least-once delivery, and non-blocking failure.
3. Implement oldest-first drop with an audited drop count.
4. Implement holds with two-admin release and expiry-survival.
5. Implement evidence collection with the non-attestation statement.
6. Apply `degrade.Guard` to compliance mutations, **excluding hold release**, which stays blocked in `StateReadOnly`.
7. Verify the local audit log works unchanged with `ee/` deleted.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Sink unreachable | queue, retry, never block the audited operation | `compliance: sink unreachable; events queued (<n>)` |
| Queue full | drop oldest, audit the drop | `compliance: audit forward queue full; dropped <n> oldest events` |
| Purge encounters a hold | skip, log | `compliance: partition <day> is under legal hold <id>; not purged` |
| Single admin releasing a hold | allow, warn, audit | `compliance: hold released by a single administrator; recorded` |
| Hold release in `StateReadOnly` | refuse | `compliance: licence expired; existing holds remain in force` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The local audit log works unchanged with `ee/` deleted | `TestLocalAuditLogFreeWithoutEE` |
| AC-2 | A SIEM outage never blocks an audited operation | `TestSinkFailureDoesNotBlockOperation` |
| AC-3 | Events survive a restart during a sink outage | `TestBufferDurableAcrossRestart` |
| AC-4 | Queue overflow drops oldest-first and audits the drop | `TestDropIsAudited` |
| AC-5 | The purge job skips a held partition | `TestHoldPreventsPurge` |
| AC-6 | With `ee/` absent, purging behaves exactly as before | `TestPurgeUnchangedWithoutEE` |
| AC-7 | Hold release requires a second admin when one exists | `TestHoldReleaseRequiresSecondAdmin` |
| AC-8 | Holds remain in force during `StateReadOnly` | `TestHoldsSurviveExpiry` |
| AC-9 | Placing and releasing a hold are audited | `TestHoldLifecycleAudited` |
| AC-10 | The non-attestation statement appears verbatim | `TestNoAttestationClaim` |
| AC-11 | The free-audit-log statement appears verbatim | `TestFreeAuditLogStated` |
| AC-12 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-13 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. The free tier keeps its audit log
go test ./ee/compliance/... -run 'TestLocalAuditLogFreeWithoutEE|TestPurgeUnchangedWithoutEE' -v
# expect: PASS

# 2. Compliance never becomes an availability risk
go test ./ee/compliance/... -run 'TestSinkFailureDoesNotBlockOperation|TestBufferDurableAcrossRestart|TestDropIsAudited' -v
# expect: PASS

# 3. A hold is really a hold
go test ./ee/compliance/... -run 'TestHoldPreventsPurge|TestHoldsSurviveExpiry|TestHoldReleaseRequiresSecondAdmin' -v
# expect: PASS

# 4. Honest about what evidence is
grep -c "Evidence collection is not attestation." ee/compliance/README.md
grep -c "The audit log is free." ee/compliance/README.md
# expect: 1 each

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The local audit log demonstrated working with `ee/` deleted
- [ ] A hold demonstrated blocking a real purge run
- [ ] Both verbatim statements present
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The purge path has no hold extension point | Return `EXTENSION POINT REQUIRED`. Do not edit `cmd/purge/`. |
| Streaming failure blocking an operation | STOP. `SPEC DEFECT: §5.2`. Compliance tooling that causes outages will be turned off. |
| Pressure to gate the local audit log | Refuse, citing §2.3 and §7.3 Q3. Route to `license-boundary-auditor`. |
