# SPEC GRVX-1503: Succession plan for the trademark, signing identity, domains and registries

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1503 | **Phase** | 15 | **Goal** | G9.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. If the project's identity cannot survive its founder, every adopter carries a risk they were never told about. |
| **Implementer role** | `security-engineer`, with `cpo` and `oss-steward` |
| **Depends on** | GRVX-707, GRVX-709, GRVX-1210, GRVX-1502 |
| **Blocks** | none |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Document where every piece of the project's identity lives, who can recover it, and how — so that if
the founder disappears, the next release still ships from the same trusted sources.

**No secret or key material is written into any file this spec produces.** The plan records
locations, custodians and procedures, never values.

## 2. Context the implementer needs

- `GRVX-709` uses **keyless** cosign with GitHub OIDC. There is deliberately no long-lived signing key to inherit; the trusted identity is the repository's release workflow.
- `TRADEMARK.md` (GRVX-707) reserves the Gravix name and logo. Trademark ownership is a legal artefact, not a technical one.
- `GRVX-1210` separates key custody from merge rights: a maintainer holds merge and release access but not organisation ownership, billing, domains, or registry credentials.
- `GRVX-1502` establishes the council, which is where custodianship transfers to.
- Assets in scope: the GitHub organisation, the `gravix.io` domain and DNS, package-registry accounts (Homebrew tap, npm, PyPI, Go module proxy, container registry), the docs-site hosting, the `security@`, `conduct@` and `trademark@` addresses, and any `ee/` licence-signing key.
- `docs/disaster-recovery.md` (9008 bytes) covers data, not identity. Read it and reference rather than duplicate.

## 3. Non-goals for this spec

- **Do NOT write any secret, key, token, password, recovery code, or seed phrase into any file.** Not encrypted, not base64-encoded, not partially redacted. The plan names where a thing lives and who can reach it.
- Do NOT create a single custodian for everything. That reproduces the problem with extra steps.
- Do NOT rely on any custodian being reachable. Every asset needs a recovery path that works if one person is unavailable indefinitely.
- Do NOT make succession dependent on a company continuing to exist.
- Do NOT publish custodian home addresses, phone numbers, or personal details.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/succession.md` | The public plan: assets, custodians by role, recovery procedures |
| `docs/oss/succession-drill.md` | The annual drill procedure and its record |
| `scripts/verify_custody.sh` | Verifies each asset has ≥2 reachable custodians |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `GOVERNANCE.md` | Add a `## Succession` section linking the plan |
| `docs/disaster-recovery.md` | Add a cross-reference; this spec covers identity, that one covers data |
| `.github/workflows/ci.yml` | Run `verify_custody.sh` monthly in a scheduled job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `.github/workflows/release.yml` | Keyless signing needs no key handover |
| Any secret store or credential file | This spec documents locations, never values |

## 5. Interface contract

### 5.1 The asset register

`docs/oss/succession.md` carries one row per asset. **Every column is a location or a role; none is
a value.**

| Column | Contains | Never contains |
|---|---|---|
| Asset | What it is | — |
| Where it lives | The provider and account **name** | credentials |
| Primary custodian | A **role** (e.g. "council chair"), plus the current holder's handle | contact details |
| Secondary custodian | A second role and handle | contact details |
| Recovery path | The documented procedure to regain access | recovery codes |
| Verified | The date custody was last confirmed | — |
| Blast radius | What breaks if this is lost | — |

Assets: GitHub organisation; `gravix.io` domain and DNS; Homebrew tap; npm scope; PyPI project;
container registry; Go module path; docs-site hosting; `security@`, `conduct@`, `trademark@`
addresses; trademark registration if one exists; `ee/` licence-signing key.

### 5.2 The two-custodian rule

Every asset has **at least two** custodians who can independently recover it, from different
organisations where the council's composition permits. `verify_custody.sh` fails if any asset has
fewer than two, and the failure names the asset and its blast radius.

Stated verbatim in `docs/oss/succession.md`:

```
Every asset here has at least two people who can recover it.

Not two people who know about it — two people who have independently
demonstrated, in the last twelve months, that they can actually get in. We test
this annually, because a recovery procedure nobody has executed is a hypothesis.
```

### 5.3 Why keyless signing matters here

`GRVX-709` chose keyless cosign with GitHub OIDC over a long-lived signing key. That decision pays
off in this spec: there is no private key to escrow, split, or hand over. The trusted identity is
the repository's release workflow, and whoever controls the repository controls releases.

This concentrates risk on the **GitHub organisation**, which is therefore the highest-blast-radius
asset in the register and the one whose custody is verified most carefully. The plan says so
explicitly rather than leaving the concentration implicit.

### 5.4 Trigger conditions and the transfer procedure

| Trigger | Confirmation | Action |
|---|---|---|
| Planned handover | The departing custodian confirms | Transfer, verify, update the register |
| 90 days unreachable | Two council members attest, publicly | Secondary custodian assumes primary; register updated |
| Incapacity or death | Council decision, publicly minuted | Secondary assumes; a new secondary is appointed within 30 days |
| Compromise | `security-engineer` declares | Immediate rotation of everything the compromised custodian could reach |

The 90-day threshold is deliberately long and deliberately public. Declaring someone unreachable is
a serious act, and it should require patience and a written record.

### 5.5 The annual drill

Once a year, for each asset: the secondary custodian attempts recovery **without the primary's
help**, records whether it worked, and updates the `Verified` date. Failures are fixed before the
drill is considered complete.

`docs/oss/succession-drill.md` records each year's results publicly, including failures. A published
failed drill is more reassuring than an unpublished successful one, because it demonstrates the drill
is real.

## 6. Behaviour

1. Enumerate every asset in §5.1 and confirm its current custody.
2. Write the register with locations and roles only. **Verify no file contains a secret** before committing.
3. Implement `verify_custody.sh` checking the two-custodian rule and the twelve-month verification age.
4. Write the drill procedure and record the first drill's results, including any failure.
5. Add the trigger conditions and transfer procedure.
6. Add the `GOVERNANCE.md` section and the disaster-recovery cross-reference.
7. Add the monthly CI job.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Asset with fewer than two custodians | audit exits 1 | `custody: <asset> has <n> custodian(s); blast radius: <what breaks>` |
| Custody unverified for over 12 months | audit exits 1 | `custody: <asset> last verified <date>; run the annual drill` |
| A secret found in any succession file | CI fails | `custody: refusing to commit a secret in <file>` |
| Drill failure | recorded publicly, fixed before the drill completes | `drill: <asset> recovery failed: <what happened>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | No secret, key, token or recovery code appears in any file | `TestNoSecretsInSuccessionPlan` |
| AC-2 | Every asset has ≥2 custodians | `TestTwoCustodiansPerAsset` |
| AC-3 | The audit fails on a single-custodian asset | `TestSingleCustodianFails` |
| AC-4 | The audit fails on custody unverified for >12 months | `TestStaleCustodyFails` |
| AC-5 | Every asset records its blast radius | `TestBlastRadiusRecorded` |
| AC-6 | The GitHub organisation is identified as the highest-blast-radius asset | `TestOrgIdentifiedAsHighestRisk` |
| AC-7 | All four trigger conditions are defined with confirmation requirements | `TestTriggerConditionsDefined` |
| AC-8 | The two-custodian statement appears verbatim | `TestCustodyStatementVerbatim` |
| AC-9 | The first drill is recorded, including any failure | `TestFirstDrillRecorded` |
| AC-10 | No custodian personal contact detail is published | `TestNoPersonalDetailsPublished` |
| AC-11 | The plan works without any company continuing to exist | `TestNoCompanyDependency` |

## 8. Verification

```bash
# 1. The one thing that must never happen
go test ./tests/... -run TestNoSecretsInSuccessionPlan -v
grep -riE "BEGIN (RSA|OPENSSH|PGP|EC) PRIVATE KEY|ghp_|sk_live_|AKIA[0-9A-Z]{16}" docs/oss/succession*.md | grep -c . || true
# expect: PASS; 0

# 2. Custody
./scripts/verify_custody.sh
# expect: exit 0, every asset >= 2 custodians

# 3. The statement, verbatim
grep -c "a recovery procedure nobody has executed is a hypothesis" docs/oss/succession.md
# expect: 1

# 4. Blast radius and concentration
go test ./tests/... -run 'TestBlastRadiusRecorded|TestOrgIdentifiedAsHighestRisk' -v
# expect: PASS

# 5. No personal details
grep -riE "\+[0-9]{6,}|[0-9]+ [A-Z][a-z]+ (Street|Road|Avenue)" docs/oss/succession*.md | grep -c . || true
# expect: 0

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Zero secrets, keys, tokens or recovery codes in any produced file — verified by scan
- [ ] Every asset has ≥2 custodians who have demonstrated recovery
- [ ] The first drill recorded publicly, failures included
- [ ] No custodian personal contact detail published
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| An asset with only one possible custodian | Report it as an open risk in `MAINTAINERS.md` and escalate to `cpo`. Do not pretend it has two. |
| Pressure to store a recovery code "somewhere safe in the repo" | Refuse absolutely. A repository is not a vault, and this one is public. |
| A drill that fails | Publish the failure and fix it. A published failed drill proves the drill is real; a quietly repeated one proves nothing. |
