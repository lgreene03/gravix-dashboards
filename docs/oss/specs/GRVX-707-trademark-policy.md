# SPEC GRVX-707: Add `TRADEMARK.md` — fork-friendly, name reserved

| Field | Value |
|---|---|
| **Spec ID** | GRVX-707 |
| **Phase** | 7 |
| **Goal** | G1.4 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §3 — "the name Gravix and the logo are reserved. Forks may exist and are welcome; they may not use the name." |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701 |
| **Blocks** | GRVX-1503 |
| **Effort** | 0.5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

State exactly what someone may and may not do with the Gravix name and logo, so that a fork, a
consultancy, a blog post, a conference talk, or a hosted service knows where the line is without
having to ask a lawyer. Apache-2.0 grants no trademark rights (§6 of the licence); silence on this
point creates avoidable conflict.

## 2. Context the implementer needs

- `LICENSE` is Apache-2.0. Its §6 explicitly grants **no** trademark licence.
- `ee/LICENSE` is BUSL-1.1 with an Additional Use Grant that already restricts offering the Licensed
  Work as a competing hosted service.
- `GOVERNANCE.md` §Trademark points to this file (GRVX-706).
- The three relicensing case studies in `docs/oss/01-competitive-thesis.md` §5 include HashiCorp
  sending OpenTofu a cease-and-desist — evidence that an unclear boundary between licence and
  trademark produces conflict.
- No trademark registration is asserted. Do not claim a registered mark (®) unless one exists.

## 3. Non-goals for this spec

- Do NOT claim a registered trademark or use the ® symbol. Use "™" only as an unregistered common-law claim.
- Do NOT restrict anything the Apache-2.0 licence permits. Trademark policy governs the **name**, not the code.
- Do NOT restrict forks. Forks are explicitly welcome; only the name is reserved.
- Do NOT duplicate the BUSL Additional Use Grant. Reference `ee/LICENSE` instead of restating it.
- Do NOT write legal advice or a licence agreement. This is a policy statement.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `TRADEMARK.md` | The trademark policy |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none | This spec adds one file |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `LICENSE`, `ee/LICENSE` | Trademark policy is separate from and subordinate to the licences |
| `GOVERNANCE.md` | Already links here (GRVX-706) |

## 5. Interface contract

### 5.1 `TRADEMARK.md` — required sections, in order

1. `## Summary` — opening line verbatim:
   `The Gravix code is free. The Gravix name is not the code.`
   Then: Apache-2.0 §6 grants no trademark rights, so this policy states what the name may be used for.

2. `## What you may do without asking` — a bulleted list, each item unambiguous:
   - Fork the repository, publish your fork, and modify it however you like.
   - State truthfully that your product "works with Gravix", "is compatible with Gravix", or "is built on Gravix".
   - Write about Gravix — blog posts, talks, books, courses, reviews, comparisons, criticism.
   - Use the name in a package identifier that names the origin, such as `gravix-exporter-foo`.
   - Run Gravix internally, commercially, at any scale, and say that you do.
   - Offer consulting, training, or support **for** Gravix.

3. `## What needs permission` — each item with a one-clause reason:
   - Naming your fork or derivative product "Gravix" or a name likely to be confused with it.
   - Using the Gravix logo as your product's or company's logo.
   - Offering a hosted service **named** Gravix. *(Note: the BUSL Additional Use Grant in `ee/LICENSE` separately governs hosting the Enterprise code; that is a licence question, this is a naming question.)*
   - Domain names, social handles, or app-store listings whose primary element is "Gravix".
   - Suggesting official endorsement, affiliation, or certification.

4. `## Naming your fork` — a worked example, because a rule with an example is followed and a rule
   without one is argued about:
   - Not permitted: `Gravix Plus`, `GravixHQ`, `Gravix Cloud by <you>`.
   - Permitted: `Foo Metrics (a fork of Gravix)`, `Foo, based on Gravix`.
   The distinction stated verbatim: `Use our name to describe your relationship to us, not as your identity.`

5. `## Why we reserve the name` — the honest reason, stated verbatim:
   `So that when a user reports a bug in "Gravix", we know which code they are running. Name confusion in an observability tool is not a branding problem; it is a debugging problem.`

6. `## Asking` — the contact address `trademark@gravix.io`, a commitment to respond within 14 days,
   and a statement that permission is usually granted for anything that does not create confusion
   about who supports the software.

7. `## Changes to this policy` — changes follow charter §6, and no change will retroactively restrict
   a fork that complied with the policy at the time it was published.

## 6. Behaviour

1. Write `TRADEMARK.md` with all seven §5.1 sections in order, including all three verbatim sentences.
2. Use "™" only. Do not use "®".
3. Verify `trademark@gravix.io` is deliverable. If it is not, return a `SPEC DEFECT` rather than
   publishing an unreachable contact.
4. Confirm no sentence restricts a right that Apache-2.0 grants over the code.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `trademark@gravix.io` unverified | stop | `SPEC DEFECT: §5.1 — trademark contact address unverified` |
| A registered mark would be implied | stop | `SPEC DEFECT: §6 step 2 — no registered mark exists` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `TRADEMARK.md` contains all seven §5.1 section headings | `TestTrademarkHasRequiredSections` |
| AC-2 | The first line of `## Summary` is the verbatim §5.1.1 sentence | `TestTrademarkSummaryOpeningLine` |
| AC-3 | Forking is explicitly permitted without asking | `TestTrademarkPermitsForks` |
| AC-4 | The `## Naming your fork` section contains both permitted and not-permitted examples | `TestTrademarkNamingExamplesPresent` |
| AC-5 | The "debugging problem" sentence appears verbatim | `TestTrademarkStatesHonestReason` |
| AC-6 | The file contains no `®` character | `TestTrademarkClaimsNoRegisteredMark` |
| AC-7 | A response commitment of 14 days is stated | `TestTrademarkStatesResponseWindow` |
| AC-8 | No clause restricts a right Apache-2.0 grants over the code | `TestTrademarkDoesNotRestrictCode` |

## 8. Verification

```bash
# 1. Sections present
for s in "Summary" "What you may do without asking" "What needs permission" "Naming your fork" \
         "Why we reserve the name" "Asking" "Changes to this policy"; do
  grep -q "$s" TRADEMARK.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: seven "ok:" lines

# 2. The three verbatim sentences
grep -c "The Gravix code is free. The Gravix name is not the code." TRADEMARK.md
# expect: 1
grep -c "Use our name to describe your relationship to us, not as your identity." TRADEMARK.md
# expect: 1
grep -c "it is a debugging problem" TRADEMARK.md
# expect: 1

# 3. No registered-mark claim
grep -c "®" TRADEMARK.md || true
# expect: 0

# 4. Tests
go test ./tests/... -run TestTrademark -v
# expect: PASS

# 5. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eight acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] No `®` anywhere in the file
- [ ] `trademark@gravix.io` confirmed deliverable, or a `SPEC DEFECT` returned
- [ ] Only `TRADEMARK.md` created; no other file changed
- [ ] `docs-engineer` delta merged (README links `TRADEMARK.md`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The contact address is unverified | Return `SPEC DEFECT: §5.1` |
| A clause that would restrict an Apache-2.0 right | Return `SPEC DEFECT: §3 — <quote the clause>` |
| An existing registered trademark for "Gravix" held by someone else | Return `SPEC DEFECT: §5 — prior mark found: <details>` and stop. This is a legal question for the owner, not an implementation decision. |
