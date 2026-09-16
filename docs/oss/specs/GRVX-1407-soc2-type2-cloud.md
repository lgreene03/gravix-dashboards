# SPEC GRVX-1407: SOC 2 Type II engineering deliverables for Gravix Cloud

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1407 | **Phase** | 14 | **Goal** | G8.5 |
| **Placement** | `ee/` (BUSL-1.1) — the evidence pipeline; the controls it evidences are core |
| **Charter basis** | §7.3 Q3 = NO for the *audit programme* (a self-hoster's security posture is unaffected by whether we hold a SOC 2 report), Q1..Q2, Q4..Q5 = NO. → ee. The **security controls themselves** stay core: TLS, RBAC, audit logging, 2FA. |
| **Implementer role** | `pro-engineer`, with `security-engineer` |
| **Depends on** | GRVX-1308, GRVX-1401 |
| **Blocks** | none |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Build the **engineering** deliverables a SOC 2 Type II audit of Gravix Cloud requires: continuous
evidence collection, access reviews, change-management records and control monitoring — without any
of it becoming a reason to gate a security control that self-hosters need.

## 2. Context the implementer needs

- `GRVX-1308` provides `ee/compliance/` with evidence collection, SIEM streaming and retention holds, plus the verbatim statement that evidence collection is not attestation.
- `GRVX-1401` requires Gravix Cloud to run the same OSS core with build provenance proving it.
- `GRVX-709` provides signed releases, SBOM and reproducible builds — three of the change-management controls an auditor asks about.
- `GRVX-1210` provides `audit_access.sh`, which already reports who holds what.
- Horizon 1 Phase 4.4 scoped SOC 2 as "documentation and process only — no code deliverables". This spec is the code half, for the cloud offering only.
- Charter §7.3 Q3 keeps every security **control** in the core. This spec builds the evidence pipeline, never the controls.

## 3. Non-goals for this spec

- Do NOT move any security control into `ee/`. TLS, authentication, RBAC, audit logging, 2FA and rate limiting are core and stay core. An audit programme is not a control.
- Do NOT claim compliance. This produces evidence; an auditor forms the opinion.
- Do NOT collect evidence from self-hosted installations. This is scoped to Gravix Cloud, which we operate.
- Do NOT let evidence collection affect any customer-facing path.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/soc2/controls.go` | Control catalogue and monitoring |
| `ee/soc2/controls_test.go` | Tests |
| `ee/soc2/evidence.go` | Continuous evidence collection |
| `ee/soc2/evidence_test.go` | Tests |
| `ee/soc2/access_review.go` | Quarterly access review generation |
| `ee/soc2/access_review_test.go` | Tests |
| `ee/soc2/README.md` | Scope, and what this does not claim |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/auth/**`, `pkg/sso/**`, `pkg/totp/**`, `pkg/ratelimit/**` | Security controls; core and free |
| The audit-log handler | Core and free |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 Scope statement, verbatim in the README

```
This package is about Gravix Cloud, not about Gravix.

SOC 2 is an audit of an organisation's operation of a service. It says something
about how we run Gravix Cloud. It says nothing about the software, and it is not
a security feature you are missing if you self-host.

Every control an auditor examines here — TLS, authentication, RBAC, two-factor,
audit logging, rate limiting — is in the Apache-2.0 core and free to everyone.
What is in this package is the paperwork: collecting evidence that we operated
those controls, continuously, over a period. Charter §7.3 Q3 keeps the controls
free; only the evidence pipeline is commercial.
```

### 5.2 Control catalogue

```go
// Package soc2 collects evidence that Gravix Cloud operated its controls over
// an audit period. It implements no control itself: every control it evidences
// lives in the Apache-2.0 core.
package soc2

// Control is one auditable control.
type Control struct {
    ID           string   `json:"id"`            // e.g. "CC6.1"
    Description  string   `json:"description"`
    Implementation string `json:"implementation"` // the CORE package that implements it
    EvidenceKind EvidenceKind `json:"evidence_kind"`
    Frequency    string   `json:"frequency"`     // "continuous"|"daily"|"quarterly"
}

// EvidenceKind is how a control is evidenced.
type EvidenceKind string

const (
    EvidenceAuditLog      EvidenceKind = "audit_log"
    EvidenceAccessReview  EvidenceKind = "access_review"
    EvidenceChangeRecord  EvidenceKind = "change_record"
    EvidenceBackupTest    EvidenceKind = "backup_verification"
    EvidenceMonitoring    EvidenceKind = "monitoring_alert"
    EvidenceBuildProvenance EvidenceKind = "build_provenance"
)

// Catalogue returns every control. Each entry's Implementation names a core
// package, which is the point: validation fails if it names an ee/ path.
func Catalogue() []Control

// ErrControlInEE is returned when a control claims an ee/ implementation.
var ErrControlInEE = errors.New("soc2: a control cannot be implemented in ee/; controls are core")
```

`ErrControlInEE` is the charter enforced in code. If a control's implementation ever points inside
`ee/`, a security control has been moved out of the free tier and the catalogue refuses to load.

### 5.3 Evidence is immutable and traceable

Every evidence record carries: the control id, the period, the collection timestamp, the source
(audit log entry ids, release tags, backup run ids), and a content digest. Evidence is written
append-only to the same durable store as audit events, and a collection gap is recorded explicitly
as a gap rather than omitted.

An auditor asking "how do you know this was true in March" must get an answer that does not depend
on trusting a summary generated in September.

### 5.4 Access reviews

Generated quarterly from `audit_access.sh` (GRVX-1210) plus tenancy records: who held what access,
when it was granted, by whom, and whether it was still required. Each review requires a named
reviewer's sign-off recorded as an audit event.

A review that nobody signs is not a review; the generator produces the artefact, and the sign-off is
an explicit human act with its own audit trail.

### 5.5 Isolation

Evidence collection is a background `ee/` job reading existing records. It must never: block a
customer request, write to a customer data path, or affect the cloud's availability. A collection
failure raises an internal alert and is recorded as a gap; it does not surface to customers.

## 6. Behaviour

1. Write the control catalogue, with every `Implementation` naming a **core** package.
2. Implement the §5.2 validation refusing any `ee/` implementation.
3. Implement continuous evidence collection with explicit gap recording.
4. Implement quarterly access reviews with mandatory named sign-off.
5. Verify no security control is moved, wrapped, or gated by this spec.
6. Verify evidence collection cannot affect any customer-facing path.
7. Write the README with the §5.1 statement verbatim.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A control names an `ee/` implementation | catalogue fails to load | `soc2: control <id> claims implementation <path> inside ee/; controls are core` |
| Evidence collection fails | record a gap, alert internally | `soc2: evidence gap for control <id>, period <p>: <err>` |
| Access review unsigned at period end | report as outstanding | `soc2: access review for <quarter> has no recorded sign-off` |
| Collection would block a request | refuse the design | `soc2: evidence collection must not be in a request path` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every control's implementation is in a core package | `TestAllControlsImplementedInCore` |
| AC-2 | A control naming an `ee/` path fails to load | `TestControlInEERefused` |
| AC-3 | No security control file is modified | `TestSecurityControlsUnmodified` |
| AC-4 | Evidence is append-only and digest-verified | `TestEvidenceImmutable` |
| AC-5 | A collection gap is recorded explicitly, never omitted | `TestGapsRecordedNotHidden` |
| AC-6 | Access reviews require a named sign-off | `TestAccessReviewRequiresSignoff` |
| AC-7 | Evidence collection never touches a customer request path | `TestCollectionOutOfRequestPath` |
| AC-8 | A collection failure does not surface to customers | `TestCollectionFailureInvisible` |
| AC-9 | The scope statement appears verbatim | `TestScopeStatementVerbatim` |
| AC-10 | No compliance claim is asserted anywhere | `TestNoComplianceClaim` |
| AC-11 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-12 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Controls are core, and stay core
go test ./ee/soc2/... -run 'TestAllControlsImplementedInCore|TestControlInEERefused|TestSecurityControlsUnmodified' -v
git diff --name-only | grep -E '^pkg/(auth|sso|totp|ratelimit)/' | grep -c . || true
# expect: PASS; 0

# 2. Evidence is defensible
go test ./ee/soc2/... -run 'TestEvidenceImmutable|TestGapsRecordedNotHidden|TestAccessReviewRequiresSignoff' -v
# expect: PASS

# 3. Compliance work cannot hurt customers
go test ./ee/soc2/... -run 'TestCollectionOutOfRequestPath|TestCollectionFailureInvisible' -v
# expect: PASS

# 4. Honest scope
grep -c "It says nothing about the software" ee/soc2/README.md
go test ./ee/soc2/... -run TestNoComplianceClaim -v
# expect: 1; PASS

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every control mapped to a core implementation, listed in the report
- [ ] No security-control file modified
- [ ] The scope statement present verbatim
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A control that would need an `ee/` implementation | STOP. `CHARTER VIOLATION: §7.3 Q3`. The control belongs in core; only its evidence is commercial. |
| Pressure to market SOC 2 as a product feature | Refuse, citing §5.1. It is a statement about our operations, not about the software. |
| Evidence collection in a request path | Return `SPEC DEFECT: §5.5`. Compliance tooling that causes latency will be disabled by whoever is on call. |
