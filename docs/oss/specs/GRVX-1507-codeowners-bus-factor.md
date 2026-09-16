# SPEC GRVX-1507: `CODEOWNERS` with a bus factor of at least two on every critical subsystem

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1507 | **Phase** | 15 | **Goal** | G9.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Whether the project can survive losing a person is not a product tier. |
| **Implementer role** | `oss-steward`, with `sre-release-manager` |
| **Depends on** | GRVX-1210, GRVX-1502 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Enumerate Gravix's critical subsystems, assign at least two owners to each, and make the gap between
the target and reality **visible and audited** rather than assumed.

## 2. Context the implementer needs

- `GRVX-1210` creates an initial `.github/CODEOWNERS` and grants merge rights to ≥3 non-founder maintainers, and deliberately leaves `/ee/` at a single owner while the paid tier is small, recording that as a known gap.
- `MAINTAINERS.md` (GRVX-706, extended by GRVX-1210) states the real per-subsystem bus factor.
- `scripts/audit_access.sh` (GRVX-1210) already reports discrepancies between access, `MAINTAINERS.md` and `CODEOWNERS`.
- G9.3 targets bus factor ≥2 on every critical subsystem.
- Repository layout: `schemas/`, `services/ingestion/`, `services/gateway/`, `transforms/`, `pkg/recompute/`, `pkg/sketch/`, `pkg/manifest/`, `pkg/storage/`, `cube/`, `dashboards/`, `deploy/`, `cmd/cli/`, `sdk/`, `ee/`, `docs/oss/`.

## 3. Non-goals for this spec

- Do NOT list an owner who cannot actually review that subsystem. A name that produces a rubber-stamp approval makes the bus factor look like two while it is one, which is worse than an honest one.
- Do NOT require two approvals on every PR. `CODEOWNERS` assigns reviewers; the approval threshold is `GOVERNANCE.md`'s, and doubling it would stall a small project.
- Do NOT hide a subsystem still at bus factor one. It goes in `MAINTAINERS.md` as a known risk.
- Do NOT define "critical" loosely. §5.1 gives the test.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/subsystems.md` | The subsystem register with criticality and owners |
| `scripts/bus_factor.sh` | Audits bus factor per subsystem |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/CODEOWNERS` | Complete coverage per §5.2, with ≥2 owners where headcount permits |
| `MAINTAINERS.md` | Per-subsystem bus factor table, including every remaining gap |
| `.github/workflows/ci.yml` | Run `bus_factor.sh` monthly in the existing scheduled job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `GOVERNANCE.md` approval thresholds | `CODEOWNERS` assigns reviewers; it does not change thresholds |
| `scripts/audit_access.sh` | GRVX-1210 owns it; this spec complements it |

## 5. Interface contract

### 5.1 The criticality test

A subsystem is **critical** when a defect in it, shipped unnoticed, would cause any of:

1. **Data loss** — a fact accepted and then lost.
2. **A wrong number** — a metric that misreports and is believed.
3. **A security breach** — unauthorised access to data or credentials.
4. **An unrecoverable upgrade** — a user unable to roll back.

Anything else is important but not critical. The distinction matters because declaring everything
critical means nothing is prioritised.

Applying the test:

| Subsystem | Critical | Which failure |
|---|---|---|
| `schemas/` | ✅ | wrong number — invalid facts accepted |
| `services/ingestion/` | ✅ | data loss |
| `transforms/`, `pkg/recompute/` | ✅ | wrong number |
| `pkg/sketch/`, `pkg/manifest/` | ✅ | wrong number |
| `pkg/storage/` | ✅ | data loss |
| `services/gateway/` (auth paths) | ✅ | security breach |
| `deploy/`, migrations | ✅ | unrecoverable upgrade |
| `cube/` | ✅ | wrong number |
| `dashboards/` | — | renders what it is given |
| `cmd/cli/`, `sdk/` | — | a defect is visible immediately |
| `docs/` | — | important, not critical |
| `ee/` | ✅ | security breach (multi-tenant isolation) |

### 5.2 `CODEOWNERS` requirements

- **Complete coverage**: every path matches at least one rule. An unowned path is a path nobody is accountable for.
- Critical subsystems: ≥2 owners where headcount permits; the shortfall recorded when it does not.
- Non-critical: ≥1 owner.
- `/ee/` follows the same rule as any critical subsystem, and its current single-owner state is a recorded gap, not an exemption.
- Owners are individuals, never a team alias that resolves to one person.

### 5.3 `scripts/bus_factor.sh`

```bash
#!/usr/bin/env bash
# Audits bus factor per subsystem against docs/oss/subsystems.md.
# Usage: ./scripts/bus_factor.sh [--json]
set -euo pipefail
```

Per subsystem it reports: declared owners; owners who have **actually reviewed a PR** in that
subsystem in the last 180 days; and the **effective** bus factor, which is the second number.

That distinction is the point. A declared owner who has not reviewed anything in six months is not a
bus factor of one more; they are a name in a file. Reporting effective rather than declared bus
factor is what makes this audit honest.

Output:

```
bus factor — <date>
  schemas/               declared 2  effective 2  ✅
  services/ingestion/    declared 2  effective 1  ⚠  @b has not reviewed here in 214 days
  ee/                    declared 1  effective 1  ⚠  known gap, tracked in MAINTAINERS.md
  coverage: 100% of paths owned
```

Exit codes: `0` every critical subsystem has effective bus factor ≥2, or its gap is recorded in
`MAINTAINERS.md`; `1` an unrecorded gap or an unowned path; `2` cannot read the repository.

An unrecorded gap fails; a recorded one passes. The audit's job is to prevent a gap being invisible,
not to prevent one existing.

## 6. Behaviour

1. Apply the §5.1 test to every subsystem; record the verdict and its failure mode in `docs/oss/subsystems.md`.
2. Write `CODEOWNERS` with complete coverage and ≥2 owners on critical subsystems where headcount permits.
3. Verify every listed owner can genuinely review that subsystem — confirm with each person and record the confirmation.
4. Implement `bus_factor.sh` reporting declared and effective separately.
5. Record every remaining gap in `MAINTAINERS.md`.
6. Add the monthly CI job.
7. Verify `CODEOWNERS` changes no approval threshold.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Path with no owner | exit 1 | `bus_factor: <path> has no owner in CODEOWNERS` |
| Critical subsystem below effective 2, unrecorded | exit 1 | `bus_factor: <subsystem> effective bus factor <n>; not recorded in MAINTAINERS.md` |
| Declared owner inactive >180 days | warn, count as ineffective | `bus_factor: @<owner> has not reviewed <subsystem> in <n> days` |
| Team alias resolving to one person | exit 1 | `bus_factor: <alias> resolves to a single person; list individuals` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every path in the repository matches a `CODEOWNERS` rule | `TestCompleteOwnershipCoverage` |
| AC-2 | Every critical subsystem is identified by the §5.1 test | `TestCriticalSubsystemsIdentified` |
| AC-3 | Each critical subsystem has ≥2 owners, or a recorded gap | `TestCriticalSubsystemsHaveTwoOwners` |
| AC-4 | The audit reports declared and effective separately | `TestDeclaredVersusEffective` |
| AC-5 | An inactive owner does not count toward effective bus factor | `TestInactiveOwnerNotCounted` |
| AC-6 | A team alias resolving to one person is refused | `TestAliasResolutionChecked` |
| AC-7 | An unrecorded gap fails the audit | `TestUnrecordedGapFails` |
| AC-8 | A recorded gap passes | `TestRecordedGapPasses` |
| AC-9 | `/ee/`'s gap is recorded, not exempted | `TestEEGapRecordedNotExempted` |
| AC-10 | No approval threshold changed | `TestApprovalThresholdsUnchanged` |
| AC-11 | Every listed owner confirmed they can review that subsystem | `TestOwnersConfirmed` |

## 8. Verification

```bash
# 1. The audit
./scripts/bus_factor.sh
# expect: coverage 100%, every critical subsystem >= 2 effective or recorded, exit 0

# 2. Honest counting
go test ./tests/... -run 'TestDeclaredVersusEffective|TestInactiveOwnerNotCounted|TestAliasResolutionChecked' -v
# expect: PASS

# 3. Gaps are visible, not hidden
go test ./tests/... -run 'TestUnrecordedGapFails|TestRecordedGapPasses|TestEEGapRecordedNotExempted' -v
grep -c "bus factor" MAINTAINERS.md
# expect: PASS; >= 1

# 4. Complete coverage
go test ./tests/... -run TestCompleteOwnershipCoverage -v
# expect: PASS

# 5. Thresholds untouched
git diff --stat GOVERNANCE.md | tail -1
go test ./tests/... -run TestApprovalThresholdsUnchanged -v
# expect: no change; PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The §5.1 test applied to every subsystem, with verdicts recorded
- [ ] Every owner's confirmation recorded
- [ ] Every remaining gap in `MAINTAINERS.md`, `/ee/` included
- [ ] No approval threshold changed
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A critical subsystem with only one possible owner | Record it in `MAINTAINERS.md` and escalate to `cpo` for G9.3. Never list a second name who cannot actually review it. |
| An owner who says they cannot review a subsystem | Remove them and record the gap. An honest one is more useful than a decorative two. |
| Pressure to require two approvals everywhere | Refuse, citing §3. `CODEOWNERS` assigns reviewers; `GOVERNANCE.md` sets thresholds, and doubling them would stall the project. |
