---
rfc: 0000
title: Template — copy this file
author: your-github-handle
status: draft
tier: design
opened: 2026-01-01
comment_closes: 2026-01-08
decided:
approvals: []
supersedes: 0
touches_entrenched: false
---

<!--
  Copy this file to NNNN-short-title.md, where NNNN is the next free number.
  Leave every heading in place: `make rfc-check` fails on a missing section.

  tier: design   -> 7-day comment window, two maintainer approvals
  tier: charter  -> 14-day window, CPO and License & Boundary Auditor approval

  touches_entrenched: true if this would alter charter §7.1, §7.3 Q4 or §7.4.
  Those may be strengthened and never weakened, so an RFC that touches one must
  say in "Charter impact" that it strengthens. That is charter tier, always.
-->

# RFC 0000: Template

## Summary

One paragraph. What changes, in plain words, for someone who will not read the rest.

## Motivation

The problem, not the solution. What goes wrong today, for whom, and how you know — an issue, a
support thread, a benchmark. A motivation that only describes the absence of the proposed feature is
not a motivation.

## Proposal

What changes, concretely. Names of files, flags, endpoints, schema fields. Enough that someone else
could implement it and you would recognise the result.

## Alternatives considered

At least one genuine alternative. What else could solve the problem? Describe each and say why it
was rejected.

An RFC with one option is an announcement. "Do nothing" counts as an alternative only if you say
what happens if nothing is done.

## Non-goals crossed

Which of the seven non-goals in [`docs/04-non-goals.md`](../../04-non-goals.md) this comes near, and
why it is not a crossing — or an explicit statement that it *is* one, which makes this a
charter-tier RFC whatever else it does.

If none: say "None", and say which one a reader might have worried about.

## Charter impact

Which sections of [`00-open-core-charter.md`](../00-open-core-charter.md) this touches, and whether
any is entrenched (§7.1, §7.3 Q4, §7.4).

If it touches an entrenched clause, state plainly that it **strengthens** the clause. CI rejects an
RFC that touches one without that statement — not because a parser can tell strengthening from
weakening, but so the question is answered in public before the vote rather than after it.

If none: say "None".

## Migration

What existing users must do. If the answer is nothing, say nothing and say why — a change that
claims to need no migration and does is worse than one that admits it.

## Unresolved questions

What you do not know yet, and what would answer it. An empty section here usually means the RFC has
not been thought about hard enough; "none" is a claim, not a default.
