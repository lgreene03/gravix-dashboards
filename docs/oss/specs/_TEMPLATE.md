# SPEC GRVX-<nnn>: <Imperative title>

<!--
AUTHORING RULES — read before filling this in. Author: senior-engineering-lead (Loop L2).

This spec is written FOR A MODEL THAT WILL NOT THINK. It will read this file and the files named
in it, and nothing else. It will not consult the roadmap, the charter, the issue thread, or you.

Therefore:
  * Every design decision must already be made HERE. If the implementer has to choose, you failed.
  * Every path is exact and repo-relative. Never "the ingestion service" — always
    `services/ingestion/main.go`.
  * Every signature is written out in full, including error types.
  * Every acceptance criterion is a single testable assertion with a named test.
  * The Verification block is copy-pasteable shell that works from a clean checkout.
  * BANNED WORDS, anywhere in the spec: appropriate, as needed, etc., handle errors, similar to,
    and so on, TBD, reasonable, properly, correctly (as an instruction), make sure it works.
  * If you cannot write it without those words, the design is not finished. Finish it first.

Delete this comment block when you fill in the template.
-->

| Field | Value |
|---|---|
| **Spec ID** | GRVX-<nnn> |
| **Phase** | <7–15> |
| **Goal** | <G1–G9>.<kr> |
| **Placement** | `core` (Apache-2.0) \| `ee/` (BUSL-1.1) |
| **Charter basis** | §7.3 Q<n> = <YES/NO> → <core/ee>. <one clause of why> |
| **Implementer role** | `senior-engineer` \| `pro-engineer` \| `frontend-engineer` |
| **Depends on** | <spec IDs that must be merged first, or "none"> |
| **Blocks** | <spec IDs, or "none"> |
| **Effort** | <n> person-days |
| **Readiness Gate** | <n>/12 — <PASS \| BLOCKED on check n> |

---

## 1. Objective

<Two to four sentences. What will be true after this is merged that is not true now. No rationale,
no history, no alternatives considered — those belong in the roadmap.>

## 2. Context the implementer needs

<Only facts. What currently exists, in which file, doing what. Enough that the implementer never
has to go exploring. Bullet points, each naming a path.>

- `path/to/file.go:NN` — <what it currently does>

## 3. Non-goals for this spec

<Explicit list of things NOT to do. This section prevents scope creep more reliably than any
review. Include the nearby product non-goals from `docs/04-non-goals.md` and show they are not
crossed.>

- Do NOT <thing>.
- This spec does not cross non-goal §<n> (<name>) because <one clause>.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `exact/path.go` | <one line> |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `exact/path.go` | <exactly what changes> |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `exact/path.go` | <out of scope; changing it is a review failure> |

## 5. Interface contract

<Full signatures. Every exported symbol. Every struct field with its type and tag. Every error
value. If it is a CLI change, every flag with its type, default, and help string. If it is an HTTP
endpoint, the exact method, path, request body, response body, and every status code.>

```go
// package <name>

// <Doc comment as it must appear in the code.>
func Name(ctx context.Context, arg Type) (Result, error)

type Result struct {
    Field string `json:"field"`
}

var ErrThing = errors.New("<exact error string>")
```

## 6. Behaviour

<Numbered, deterministic steps. The implementer follows these in order. No branching left to
judgement — if there is a branch, state the condition and both outcomes.>

1. <step>
2. If <exact condition>, then <exact outcome>. Otherwise <exact outcome>.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| <condition> | <return / exit code / status> | `<verbatim string>` |

## 7. Acceptance criteria

<Each criterion is ONE assertion, independently testable, with the test name that proves it.
`qa-engineer` will write these tests from this section alone, before reading the implementation.>

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | <single assertion> | `TestName` |
| AC-2 | <single assertion> | `TestName` |

## 8. Verification

<Copy-pasteable. Runs from a clean checkout. Each command with its expected output. The
implementer pastes the real output into its report; `qa-engineer` re-runs it independently.>

```bash
# 1. <what this proves>
go test ./path/... -run TestName -v
# expect: PASS, 0 failures

# 2. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] <spec-specific item>

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
| (`pro-engineer` only) a needed core hook | Return `EXTENSION POINT REQUIRED` with the proposed interface. |
