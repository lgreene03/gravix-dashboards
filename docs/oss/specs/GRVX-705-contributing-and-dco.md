# SPEC GRVX-705: Add `CONTRIBUTING.md`, DCO enforcement, and issue/PR templates

| Field | Value |
|---|---|
| **Spec ID** | GRVX-705 |
| **Phase** | 7 |
| **Goal** | G1.7, G6.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §3 — DCO sign-off, **never** a CLA. A CLA would let a future owner relicense contributed code; the DCO cannot. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-704 |
| **Blocks** | GRVX-706, GRVX-1205 |
| **Effort** | 1.5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give an outside contributor everything needed to land a first pull request: how to build, how to
test, what the review gates are, and how to sign off. Enforce DCO sign-off in CI. Provide issue and
PR templates that route work to the correct role from the roster.

## 2. Context the implementer needs

- No `CONTRIBUTING.md`, `.github/ISSUE_TEMPLATE/`, or `.github/PULL_REQUEST_TEMPLATE.md` exists. Verified by `ls`.
- `.github/workflows/` contains `ci.yml`, `deploy.yml`, `release.yml`, `helm-deploy.yml`, `publish-sdks.yml`, `sdk-codegen.yml`, `example-deploy-event.yml`.
- `make build-oss`, `make test-oss`, `make check-boundary` exist (GRVX-704).
- Build commands from `CLAUDE.md`: `docker-compose up -d --build`; `go test ./...`; `go test ./schemas/... -v -cover` (100% coverage required); `./scripts/golden_path_test.sh` (no Docker needed).
- Roles and their scopes are defined in `docs/oss/10-agent-roster.md`; the handoff matrix is §3 of that file.
- `docs/04-non-goals.md` lists seven categories of permanently-rejected request.

## 3. Non-goals for this spec

- Do NOT introduce a CLA, a CLA bot, or any agreement transferring copyright. Charter §3 forbids it permanently.
- Do NOT write `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md` or `ADOPTERS.md`. That is GRVX-706.
- Do NOT write `SECURITY.md`. That is GRVX-708.
- Do NOT change any existing workflow's behaviour; add one job only.
- Do NOT create `good first issue` content. That is GRVX-1205.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `CONTRIBUTING.md` | How to build, test, sign off, and get reviewed |
| `.github/workflows/dco.yml` | DCO sign-off enforcement |
| `.github/PULL_REQUEST_TEMPLATE.md` | PR checklist mirroring the L4 review gates |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | Bug form requiring a reproduction |
| `.github/ISSUE_TEMPLATE/feature_request.yml` | Feature form separating problem from proposed solution |
| `.github/ISSUE_TEMPLATE/config.yml` | Disables blank issues; links to Discussions and `SECURITY.md` |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none | This spec adds files only |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `.github/workflows/ci.yml` | DCO is a separate workflow; mixing them couples unrelated failures |
| `LICENSE`, `ee/LICENSE` | Settled by GRVX-701 and GRVX-702 |

## 5. Interface contract

### 5.1 `CONTRIBUTING.md` — required sections, in this order

1. `## Before you start` — links `docs/04-non-goals.md` and states plainly that requests in those
   seven categories are rejected regardless of implementation quality, so nobody wastes a weekend.
2. `## Development setup` — the exact commands: `cp .env.example .env`, `docker-compose up -d --build`,
   and the no-Docker path `./scripts/golden_path_test.sh`.
3. `## Running tests` — `go test ./...`, `go test ./schemas/... -v -cover` with the note that
   `schemas/` must stay at 100%, and `make check-boundary`.
4. `## The open-core boundary` — states that `ee/` is BUSL-1.1 source-available, that everything else
   is Apache-2.0 and cannot be relicensed, that no core package may import `ee/`, and that
   `make build-oss && make test-oss` must pass with `ee/` deleted. Links the charter.
5. `## Sign your commits (DCO)` — the exact command `git commit -s`, the literal
   `Signed-off-by: Name <email>` trailer format, a statement that **Gravix has no CLA and will never
   introduce one**, and the fix for an unsigned commit: `git commit --amend -s --no-edit` then force-push.
6. `## What review will check` — the five L4 gates verbatim: boundary, security, acceptance, docs, scope.
   Include the sentence: `A change to a file the spec did not name is a scope violation even when the change is an improvement.`
7. `## Where to send what` — a table reproducing the handoff matrix from `docs/oss/10-agent-roster.md` §3.
8. `## Commit and PR conventions` — imperative subject under 72 characters, body explaining why, one
   logical change per PR.

### 5.2 `.github/workflows/dco.yml`

- `name: DCO`
- Triggers: `pull_request` with types `[opened, synchronize, reopened]`.
- One job `check` on `ubuntu-latest`.
- Steps: `actions/checkout@v4` with `fetch-depth: 0`, then a `run:` step that inspects every commit
  in the PR range and asserts each commit message contains a line matching the regex
  `^Signed-off-by: .+ <.+@.+>$`.
- On failure the step prints, for each offending commit, `<short-sha> missing Signed-off-by`, then
  this exact remediation block and exits 1:

```
Every commit needs a DCO sign-off. To fix the most recent commit:
    git commit --amend -s --no-edit && git push --force-with-lease
For several commits:
    git rebase --signoff origin/main && git push --force-with-lease
Gravix uses the DCO and has no CLA. See CONTRIBUTING.md.
```

- Merge commits (more than one parent) are exempt and must be skipped.

### 5.3 `.github/PULL_REQUEST_TEMPLATE.md`

Sections: `## What this changes`, `## Spec` (spec ID or `none — <why>`), `## How this was verified`
(paste real command output), and `## Checklist` with exactly these items:

```
- [ ] Commits are signed off (`git commit -s`)
- [ ] `go test ./...` passes
- [ ] `make check-boundary` prints `boundary: 0 violations`
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] Only files named by the spec are modified
- [ ] Docs updated, or no user-visible change
- [ ] No test skipped, quarantined, or deleted to make CI green
```

### 5.4 Issue templates

`bug_report.yml` — required fields: Gravix version; deployment (`docker-compose.bootstrap.yml` /
`docker-compose.yml` / Helm / other); **exact steps from a clean checkout**; observed output;
expected output; whether it reproduces on `main`. Every one of those six is `required: true`.

`feature_request.yml` — required fields, in this order: **the problem you have** (not the solution);
what you tried; the proposed solution (marked optional); and a required checkbox
`I have read docs/04-non-goals.md and this is not in one of those categories`.

`config.yml` — `blank_issues_enabled: false`, plus contact links to GitHub Discussions for
questions and to `SECURITY.md` for vulnerabilities, with the line
`Do not report vulnerabilities in a public issue.`

## 6. Behaviour

1. Write `CONTRIBUTING.md` with all eight §5.1 sections in order.
2. Write `.github/workflows/dco.yml` per §5.2.
3. Write the PR template per §5.3 with the checklist verbatim.
4. Write the three issue templates per §5.4.
5. Verify each YAML file parses.
6. Verify the DCO regex against a signed and an unsigned commit message locally.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A non-merge commit lacks a sign-off | job exits 1 | `<short-sha> missing Signed-off-by`, then the §5.2 remediation block |
| All commits signed | job exits 0 | `DCO: all commits signed off` |
| A merge commit | skipped, not flagged | `<short-sha> merge commit, skipped` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `CONTRIBUTING.md` contains all eight §5.1 section headings | `TestContributingHasRequiredSections` |
| AC-2 | `CONTRIBUTING.md` states Gravix has no CLA and never will | `TestContributingRejectsCLA` |
| AC-3 | No file in the repository introduces a CLA or copyright assignment | `TestNoCLAAnywhere` |
| AC-4 | `dco.yml` exists, triggers on `pull_request`, and is not `continue-on-error` | `TestDCOWorkflowEnforced` |
| AC-5 | The DCO regex matches a valid trailer and rejects a missing one | `TestDCORegexBehaviour` |
| AC-6 | The PR template contains all seven checklist items verbatim | `TestPRTemplateChecklistComplete` |
| AC-7 | All three issue-template YAML files parse | `TestIssueTemplatesParse` |
| AC-8 | `bug_report.yml` marks all six reproduction fields `required: true` | `TestBugReportRequiresRepro` |
| AC-9 | `feature_request.yml` requires the non-goals acknowledgement checkbox | `TestFeatureRequestRequiresNonGoalsCheck` |
| AC-10 | `config.yml` sets `blank_issues_enabled: false` | `TestBlankIssuesDisabled` |

## 8. Verification

```bash
# 1. Contributing guide completeness
for s in "Before you start" "Development setup" "Running tests" "The open-core boundary" \
         "Sign your commits" "What review will check" "Where to send what" "Commit and PR conventions"; do
  grep -q "$s" CONTRIBUTING.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: eight "ok:" lines

# 2. No CLA anywhere
grep -ril "contributor license agreement" . --exclude-dir=.git --exclude-dir=node_modules | grep -c . || true
# expect: 0

# 3. Templates parse
python3 -c "import yaml,glob,sys; [yaml.safe_load(open(f)) for f in glob.glob('.github/ISSUE_TEMPLATE/*.yml')+['.github/workflows/dco.yml']]; print('yaml ok')"
# expect: yaml ok

# 4. Tests
go test ./tests/... -run 'TestContributing|TestDCO|TestPRTemplate|TestIssueTemplates|TestNoCLA|TestBugReport|TestFeatureRequest|TestBlankIssues' -v
# expect: PASS

# 5. Open-core integrity
make check-boundary
# expect: boundary: 0 violations
make build-oss && make test-oss
# expect: both succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Zero occurrences of "contributor license agreement" anywhere in the repository
- [ ] All YAML parses
- [ ] No existing workflow modified
- [ ] `docs-engineer` delta merged (README links `CONTRIBUTING.md`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any existing CLA reference or copyright-assignment text | Return `SPEC DEFECT: §3 — CLA reference at <path>:<line>`. Do not delete it unilaterally; the charter's §3 claim depends on knowing it was never there. |
| No `main` branch to compare the PR range against | Return `SPEC DEFECT: §5.2 — default branch name is <name>` |
