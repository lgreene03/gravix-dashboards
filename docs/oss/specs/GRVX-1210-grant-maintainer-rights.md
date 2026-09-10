# SPEC GRVX-1210: Grant merge rights to at least three non-founder maintainers

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1210 | **Phase** | 12 | **Goal** | G6.7, G9.1, G9.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Project governance is not a product tier. |
| **Implementer role** | `oss-steward`, with `cpo` and `security-engineer` |
| **Depends on** | GRVX-1203, GRVX-1205, GRVX-706 |
| **Blocks** | GRVX-1502, GRVX-1507 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Move the project's bus factor from one to at least three by granting real merge and release rights
to non-founder maintainers — with the access controls, key custody and revocation procedure that
makes doing so safe rather than reckless.

## 2. Context the implementer needs

- `MAINTAINERS.md` (GRVX-706) lists one maintainer and states bus factor 1 as a known risk tracked as G9.3.
- `docs/oss/contribution-ladder.md` (GRVX-1203) §5.2 defines Reviewer→Maintainer criteria, including criterion 4: has correctly applied the Crippleware Test or declined a request citing a non-goal, in public, at least once.
- Release signing is keyless cosign with GitHub OIDC (GRVX-709), so there is no long-lived signing key to hand over — the identity is the workflow, not a secret.
- `GOVERNANCE.md` (GRVX-706) states maintainers can merge, release, and grant Contributor→Reviewer, but cannot amend the charter or overrule a veto inside a sprint.
- Three agent roles hold vetoes (`docs/oss/10-agent-roster.md` §4); those are roles, not human ranks, and maintainer status does not confer them.

## 3. Non-goals for this spec

- Do NOT grant maintainer status to meet a number. A maintainer who has not met the §5.2 criteria of GRVX-1203 is a governance liability, and the count is a symptom of health rather than the health itself.
- Do NOT grant organisation-owner or billing access with merge rights. Those are separate, and GRVX-1503 handles their custody.
- Do NOT create a maintainer tier that cannot cut a release. A maintainer who cannot release does not improve the bus factor, which is the entire point.
- Do NOT hand over any long-lived secret. Keyless signing exists precisely so there is nothing to hand over.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/maintainer-onboarding.md` | The checklist for granting and accepting rights |
| `docs/oss/maintainer-offboarding.md` | The checklist for revoking them |
| `.github/CODEOWNERS` | Subsystem ownership |
| `scripts/audit_access.sh` | Reports who holds what, and flags anything unexpected |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `MAINTAINERS.md` | Add new maintainers with their subsystems, level and start date; update the bus-factor statement to the real current number |
| `GOVERNANCE.md` | Point at the onboarding and offboarding documents |
| `.github/workflows/ci.yml` | Run `audit_access.sh` weekly in a scheduled job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | Maintainer status confers no amendment power |
| `.github/workflows/release.yml` | Keyless signing needs no per-maintainer change |
| `docs/oss/contribution-ladder.md` | Criteria are settled by GRVX-1203 |

## 5. Interface contract

### 5.1 What a maintainer receives

| Access | Scope | Why |
|---|---|---|
| Merge on the default branch | All paths | Cannot improve bus factor without it |
| Tag and release | All releases | A maintainer who cannot release is decorative |
| Grant Contributor→Reviewer | Per the ladder | Distributes the promotion path |
| Triage: labels, milestones, close | All issues | Daily maintenance |
| Security advisory drafting | Private advisories | Disclosure must not queue behind one person |

**Not granted:** organisation ownership, billing, domain or DNS control, package-registry publishing
credentials, or `ee/` licence-signing key material. Those are custody matters handled by GRVX-1503,
and separating them means a compromised maintainer account cannot take the project's identity.

### 5.2 Onboarding checklist

Each item has an owner and must be recorded as done in the grant issue:

- [ ] Ladder criteria met and evidenced with links — *`oss-steward`*
- [ ] Nomination, unanimous maintainer agreement, 14-day public comment window closed — *`cpo`*
- [ ] Two-factor authentication verified on their account — *`security-engineer`*
- [ ] Signed commits or verified DCO history reviewed — *`security-engineer`*
- [ ] Charter read and acknowledged in writing, specifically §7.1, §7.3 Q4 and §7.4 — *`cpo`*
- [ ] `CODEOWNERS` entry added for their subsystems — *`oss-steward`*
- [ ] `MAINTAINERS.md` updated — *`oss-steward`*
- [ ] Access granted — *`cpo`*
- [ ] First release cut **with** them, not for them — *`sre-release-manager`*
- [ ] Offboarding procedure read by both parties — *`oss-steward`*

The last two matter most. Someone who has never cut a release does not improve the bus factor, and
agreeing the exit terms while everyone is happy is what makes an eventual exit uneventful.

### 5.3 Offboarding

Triggered by resignation, six months' inactivity (Emeritus, per GRVX-1203 §5.3), or removal for
cause. In every case, within **24 hours**:

1. Merge and release access revoked.
2. `MAINTAINERS.md` and `CODEOWNERS` updated.
3. `audit_access.sh` run to confirm revocation.
4. For removal for cause only: a written public reason, per `GOVERNANCE.md`.

Stated verbatim in `docs/oss/maintainer-offboarding.md`:

```
Removing access is not a judgement about a person.

Inactive access is a security liability, not a courtesy. We remove it quickly,
we say so plainly, and we restore it on request with no re-qualification if you
come back. Nobody has to explain why they stopped.
```

### 5.4 `scripts/audit_access.sh`

Reports, weekly: who holds merge rights, who holds release rights, who is in `MAINTAINERS.md`, and
who is in `CODEOWNERS`. It **flags any discrepancy between them** — access held by someone not
listed, or listed without access — and any account without two-factor authentication.

Exit codes: `0` consistent; `1` a discrepancy; `2` cannot query the platform.

A discrepancy is the interesting case: it usually means someone was added or removed without the
checklist, which is exactly the failure this spec exists to prevent.

### 5.5 `CODEOWNERS` subsystems

Derived from the real repository layout, with **≥2 owners each** once maintainer count permits:

```
/schemas/                @owner @maintainer-a
/services/ingestion/     @owner @maintainer-a
/transforms/             @owner @maintainer-b
/pkg/recompute/          @owner @maintainer-b
/pkg/sketch/             @owner @maintainer-b
/cube/                   @owner @maintainer-c
/dashboards/             @owner @maintainer-c
/deploy/                 @owner @maintainer-a
/ee/                     @owner
/docs/oss/               @owner @maintainer-c
```

`/ee/` deliberately keeps a single owner while the paid tier is small; that is recorded as a known
bus-factor gap in `MAINTAINERS.md` rather than papered over.

## 6. Behaviour

1. Identify candidates meeting GRVX-1203 §5.2, with linked evidence per criterion. If none qualify, that is the honest finding — report it and do not proceed.
2. Run the nomination and comment window.
3. Complete every onboarding checklist item, recording each in the grant issue.
4. Write `CODEOWNERS` with ≥2 owners per subsystem where headcount permits.
5. Update `MAINTAINERS.md` with the real bus factor, per subsystem.
6. Implement `audit_access.sh` and the weekly job.
7. Write the offboarding document with the §5.3 statement verbatim.
8. Cut one release **with** each new maintainer driving it.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Candidate has not met a ladder criterion | do not grant | `candidate has not met criterion <n>; the count is not the goal` |
| Two-factor not enabled | do not grant | `two-factor authentication is required before merge access` |
| Access held by someone not in `MAINTAINERS.md` | audit exits 1 | `access discrepancy: <handle> holds merge rights but is not listed` |
| Listed without access | audit exits 1 | `access discrepancy: <handle> is listed but holds no rights` |
| Subsystem with one owner after grants | report | `bus factor 1 remains on <path>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every new maintainer met every ladder criterion, with evidence | `TestLadderCriteriaEvidenced` |
| AC-2 | Every onboarding checklist item is recorded done | `TestOnboardingChecklistComplete` |
| AC-3 | Two-factor verified before any grant | `TestTwoFactorRequiredBeforeGrant` |
| AC-4 | Each new maintainer has cut a release | `TestEachMaintainerHasReleased` |
| AC-5 | No maintainer holds org-owner, billing, or key custody | `TestCustodySeparatedFromMerge` |
| AC-6 | `CODEOWNERS` gives ≥2 owners per subsystem where headcount permits | `TestCodeownersBusFactor` |
| AC-7 | `MAINTAINERS.md` states the real bus factor per subsystem | `TestMaintainersStatesRealBusFactor` |
| AC-8 | The audit flags access held by an unlisted account | `TestAuditFlagsUnlistedAccess` |
| AC-9 | The audit flags listing without access | `TestAuditFlagsListedWithoutAccess` |
| AC-10 | The offboarding statement appears verbatim | `TestOffboardingStatementVerbatim` |
| AC-11 | Revocation completes within 24 hours and is verified | `TestRevocationVerified` |
| AC-12 | Maintainer status confers no charter-amendment power | `TestNoCharterPowerFromMaintainership` |

## 8. Verification

```bash
# 1. Access is consistent with the record
./scripts/audit_access.sh
# expect: exit 0, no discrepancies

# 2. Bus factor
go test ./tests/... -run 'TestCodeownersBusFactor|TestMaintainersStatesRealBusFactor' -v
grep -c "@" .github/CODEOWNERS
# expect: PASS; >= 2 owners on each non-ee path

# 3. Custody is separate from merge
go test ./tests/... -run TestCustodySeparatedFromMerge -v
# expect: PASS

# 4. The audit catches drift
go test ./tests/... -run 'TestAuditFlagsUnlistedAccess|TestAuditFlagsListedWithoutAccess' -v
# expect: PASS

# 5. The offboarding statement
grep -c "Inactive access is a security liability, not a courtesy." docs/oss/maintainer-offboarding.md
# expect: 1

# 6. No governance overreach
go test ./tests/... -run TestNoCharterPowerFromMaintainership -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Evidence linked for every ladder criterion, per candidate
- [ ] Each new maintainer has driven a real release
- [ ] `audit_access.sh` reports zero discrepancies
- [ ] `MAINTAINERS.md` states the real per-subsystem bus factor, including the `/ee/` gap
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| No candidate meets the criteria | Report it honestly. G6.7 is a symptom of community health; granting rights to hit a number creates a governance problem instead of solving one. |
| Pressure to bundle key custody with merge rights | Refuse, citing §5.1. Route to `security-engineer`. Separation is what limits the blast radius of a compromised account. |
| A subsystem still at bus factor 1 after grants | Record it in `MAINTAINERS.md` and report to `cpo` for G9.3. Do not hide a known risk. |
