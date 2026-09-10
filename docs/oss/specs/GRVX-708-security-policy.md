# SPEC GRVX-708: Add `SECURITY.md` with a disclosure SLA and advisory process

| Field | Value |
|---|---|
| **Spec ID** | GRVX-708 |
| **Phase** | 7 |
| **Goal** | G1.4, G1.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q3 — security controls are permanently core. A free user's safety is not a paid feature, and neither is a disclosure channel. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701 |
| **Blocks** | GRVX-709 |
| **Effort** | 1 person-day |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give a security researcher a clear, private way to report a vulnerability, a stated response
timeline, and a published record of what has been fixed. After this spec, `SECURITY.md` exists, a
private reporting channel is enabled, and the supported-versions policy is unambiguous.

## 2. Context the implementer needs

- No `SECURITY.md` exists. `docs/responsible-disclosure.md` (1692 bytes) exists and must be
  reconciled, not duplicated.
- `docs/security.md` (3861 bytes) and `docs/security-checklist.md` (2349 bytes) exist and cover
  operator-facing hardening, not disclosure.
- `.github/ISSUE_TEMPLATE/config.yml` (GRVX-705) already links to `SECURITY.md`; that link 404s today.
- The disclosure SLA in `docs/oss/11-agent-loops.md` §L7-sec is: acknowledge <24h, triage <72h,
  fix or mitigation for critical <7 days, coordinated disclosure with credit.
- `.github/workflows/release.yml` exists; release signing and SBOM are GRVX-709, not this spec.
- The highest-risk surfaces are enumerated in `.claude/agents/security-engineer.md`: ingestion
  parsing, authentication and licence verification, SQL construction, multi-tenant isolation,
  secrets handling, supply chain.

## 3. Non-goals for this spec

- Do NOT fix any vulnerability. This spec establishes process only.
- Do NOT add release signing or SBOM generation. That is GRVX-709.
- Do NOT duplicate `docs/security.md` hardening guidance. Link it.
- Do NOT promise a bug bounty. State plainly that there is no monetary bounty; a false promise is
  worse than an honest absence.
- Do NOT publish any unfixed vulnerability, including in examples.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `SECURITY.md` | Disclosure policy, SLA, supported versions |
| `docs/security-advisories.md` | The published advisory index, initially empty |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/responsible-disclosure.md` | Replace the body with a pointer to `SECURITY.md`, preserving any operational detail `SECURITY.md` does not cover. Read it in full first. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/security.md`, `docs/security-checklist.md` | Operator hardening; a different audience |
| `.github/workflows/**` | Signing and SBOM are GRVX-709 |
| Any authentication or crypto code | Process spec only |

## 5. Interface contract

### 5.1 `SECURITY.md` — required sections, in order

1. `## Reporting a vulnerability` — the private channel, stated as: GitHub's private vulnerability
   reporting on this repository (preferred), or `security@gravix.io`. Followed verbatim by:
   `Do not open a public issue for a vulnerability. Do not post it in Discussions, a pull request, or a comment.`

2. `## What to include` — a required list: affected version; affected component; the concrete attack
   path (what an attacker does, step by step); observed impact; a reproduction if you have one.
   Include: `A report without a concrete attack path is difficult to triage. "This looks unsafe" is a code review comment, not a vulnerability report.`

3. `## Our commitments` — a table exactly matching the L7-sec SLA:

   | Stage | Commitment |
   |---|---|
   | Acknowledgement | within 24 hours |
   | Triage and severity assessment | within 72 hours |
   | Fix or documented mitigation, critical | within 7 days |
   | Fix or documented mitigation, high | within 30 days |
   | Coordinated public disclosure | with your credit, unless you ask otherwise |

   Followed by: `A security fix bypasses the normal biweekly release train and ships immediately.`

4. `## Severity` — the four levels with a one-line Gravix-specific definition each:
   - **Critical** — unauthenticated remote code execution, or one tenant reading another tenant's facts.
   - **High** — authentication bypass, privilege escalation, or credential disclosure.
   - **Medium** — denial of service against ingestion, or information disclosure limited to metadata.
   - **Low** — issues requiring an already-privileged position or an unrealistic precondition.

5. `## Supported versions` — a table of the currently supported minor versions and their status.
   Ships with the current version marked supported and a statement that the LTS policy arrives in
   Phase 15 (GRVX-1501). Do not claim an LTS window that does not exist yet.

6. `## No bounty` — verbatim:
   `Gravix does not operate a paid bug bounty. We credit every reporter who wants credit, and we will say so publicly. We would rather tell you that plainly than imply a reward that does not exist.`

7. `## Scope` — in scope: this repository's code, the published container images, the Helm chart, the
   SDKs. Out of scope: findings that require an attacker to already have valid admin credentials;
   denial of service by sending more traffic than the documented capacity; vulnerabilities in
   third-party dependencies with no Gravix-specific exploit path (report those upstream, and tell us
   so we can bump the dependency).

8. `## Past advisories` — links `docs/security-advisories.md`.

### 5.2 `docs/security-advisories.md`

A `# Security advisories` heading; a statement that every fixed vulnerability is published here with
its CVE where one was assigned; a table with headers
`| ID | Date | Severity | Component | Fixed in | Reporter |`; and, while the table is empty, this
line: `No advisories have been published. This page exists so that when one is, there is an obvious place to look.`

### 5.3 `docs/responsible-disclosure.md` after modification

Retains its heading, adds a first line pointing to `SECURITY.md` as authoritative, and keeps only
content `SECURITY.md` does not cover. Every fact removed must be recorded in the implementation
report so nothing is silently lost.

## 6. Behaviour

1. Read `docs/responsible-disclosure.md` in full and list its facts in the report.
2. Write `SECURITY.md` with all eight §5.1 sections in order, including the three verbatim passages.
3. Write `docs/security-advisories.md` per §5.2.
4. Rewrite `docs/responsible-disclosure.md` per §5.3, recording every fact dropped or migrated.
5. Verify GitHub private vulnerability reporting is enabled on the repository. If it is not, note in
   `SECURITY.md` that email is the only channel until it is, and record the gap in the report.
6. Verify `security@gravix.io` is deliverable. If it is not, return a `SPEC DEFECT`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `security@gravix.io` unverified | stop | `SPEC DEFECT: §5.1 — security contact address unverified` |
| Private vulnerability reporting disabled | proceed, email-only, record it | `note: private vulnerability reporting is not enabled; email is the only channel` |
| A fact from `responsible-disclosure.md` has no home | stop | `SPEC DEFECT: §5.3 — orphaned fact: <quote>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `SECURITY.md` contains all eight §5.1 section headings | `TestSecurityPolicyHasRequiredSections` |
| AC-2 | The "do not open a public issue" sentence appears verbatim | `TestSecurityForbidsPublicDisclosure` |
| AC-3 | The commitments table contains all five SLA rows | `TestSecuritySLAComplete` |
| AC-4 | All four severity levels are defined | `TestSecuritySeverityLevelsDefined` |
| AC-5 | The no-bounty paragraph appears verbatim | `TestSecurityStatesNoBounty` |
| AC-6 | `SECURITY.md` contains a working contact channel | `TestSecurityHasContactChannel` |
| AC-7 | `docs/security-advisories.md` exists with the six-column table | `TestAdvisoryIndexExists` |
| AC-8 | `docs/responsible-disclosure.md` points to `SECURITY.md` | `TestResponsibleDisclosureRedirects` |
| AC-9 | No unfixed vulnerability is described anywhere in the new files | `TestNoUnfixedVulnDisclosed` |
| AC-10 | `.github/ISSUE_TEMPLATE/config.yml`'s `SECURITY.md` link resolves | `TestIssueTemplateSecurityLinkResolves` |

## 8. Verification

```bash
# 1. Sections present
for s in "Reporting a vulnerability" "What to include" "Our commitments" "Severity" \
         "Supported versions" "No bounty" "Scope" "Past advisories"; do
  grep -q "$s" SECURITY.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: eight "ok:" lines

# 2. Verbatim passages
grep -c "Do not open a public issue for a vulnerability" SECURITY.md
# expect: 1
grep -c "Gravix does not operate a paid bug bounty" SECURITY.md
# expect: 1

# 3. SLA rows
grep -c "within 24 hours" SECURITY.md && grep -c "within 72 hours" SECURITY.md && grep -c "within 7 days" SECURITY.md
# expect: 1, 1, 1

# 4. Advisory index and redirect
test -f docs/security-advisories.md && grep -c "Reporter" docs/security-advisories.md
grep -c "SECURITY.md" docs/responsible-disclosure.md
# expect: >= 1 for both

# 5. Tests
go test ./tests/... -run 'TestSecurity|TestAdvisory|TestResponsibleDisclosure|TestIssueTemplateSecurity|TestNoUnfixedVuln' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every fact from `docs/responsible-disclosure.md` accounted for in the report
- [ ] `security@gravix.io` confirmed deliverable, or a `SPEC DEFECT` returned
- [ ] Private vulnerability reporting status recorded in the report
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (README links `SECURITY.md`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The security contact is unverified | Return `SPEC DEFECT: §5.1` |
| An actual vulnerability while reading the code | Do NOT write it in any file. Report it privately through `SECURITY.md`'s own channel and tell the dispatcher a private report was filed. |
| A fact in `responsible-disclosure.md` with no home | Return `SPEC DEFECT: §5.3` |
