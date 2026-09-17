<!--
  For an RFC. Append ?template=rfc.md to the pull-request URL to get this form.
  For anything else, use the default template.

  The RFC itself is the file you are adding under docs/oss/rfcs/. This form is
  about the pull request: what you have done and what still has to happen.
-->

## The RFC

- **Number and title:**
- **Tier:** design / charter
- **Comment window closes:**
- **Touches an entrenched clause (§7.1, §7.3 Q4, §7.4):** no / yes — and it **strengthens** it

## Sections

All eight are required, and `make rfc-check` fails on a missing one. Confirm each says something:

- [ ] `## Summary` — one paragraph, for someone who will not read the rest
- [ ] `## Motivation` — the problem, not the solution
- [ ] `## Proposal` — concretely enough that someone else could implement it
- [ ] `## Alternatives considered` — **at least one genuine alternative**, with why it was rejected
- [ ] `## Non-goals crossed` — which of the seven this comes near, and why it is not a crossing
- [ ] `## Charter impact` — which sections, and whether any is entrenched
- [ ] `## Migration` — what existing users must do
- [ ] `## Unresolved questions` — what you do not know yet

## Before merging

- [ ] `make rfc-index` run and the regenerated `docs/oss/rfcs/index.md` committed
- [ ] `make rfc-check` passes locally
- [ ] The comment window has actually closed — CI checks `decided >= comment_closes`
- [ ] `approvals` names who approved, and there are at least two
- [ ] A changelog entry, if this is charter tier (charter §6)

## What this pull request is not

- [ ] It does not implement the proposal. An RFC lands first; the implementation is a separate pull request against an accepted RFC.
- [ ] It does not back-fill a decision already made elsewhere. The record is the decision.

<!--
  An RFC with one option is an announcement. If "Alternatives considered" was
  hard to fill in, that is usually worth another day of thinking rather than a
  sentence saying no alternatives exist.
-->
