---
rfc: 0001
title: Adopt a written RFC process and public decision log
author: lgreene03
status: accepted
tier: design
opened: 2026-09-01
comment_closes: 2026-09-08
decided: 2026-09-16
approvals: [cpo, license-boundary-auditor]
supersedes: 0
touches_entrenched: false
---

# RFC 0001: Adopt a written RFC process and public decision log

## Summary

Design changes and charter amendments get a written, public, archived path: an RFC file in
`docs/oss/rfcs/`, a comment window whose length is set by the tier, named approvals, and a generated
decision log that keeps every proposal — including the rejected ones — permanently.

## Motivation

Charter §6 already requires an RFC, a 14-day comment window and two named approvals before the
charter can be amended. It requires them of a directory that did not exist. A procedure whose
artefacts have nowhere to live is aspirational, and the first time it is needed will be the first
time anyone discovers that.

The second problem is repetition. Gravix declines things — distributed tracing, a query language,
per-request querying — and it declines them on the record in
[`docs/04-non-goals.md`](../../04-non-goals.md). But a non-goal says *what* was decided, not *why*,
against which argument, and what would change the answer. Without that, the same well-argued request
arrives every six months and is re-argued from nothing, by people who have no way of knowing it was
already considered carefully.

The third is that decisions made in a private channel and written up afterwards are indistinguishable
from decisions made in public — until someone needs to check who agreed to what, and finds a
description of a decision rather than the decision.

## Proposal

A `docs/oss/rfcs/` directory containing one Markdown file per proposal, each with YAML front matter
carrying `rfc`, `title`, `author`, `status`, `tier`, `opened`, `comment_closes`, `decided`,
`approvals`, `supersedes` and `touches_entrenched`, and eight required sections: Summary, Motivation,
Proposal, Alternatives considered, Non-goals crossed, Charter impact, Migration, Unresolved
questions.

`pkg/rfc` parses and validates them. `make rfc-check` runs in CI on every pull request and fails on:

- a charter-tier RFC with a comment window shorter than 14 days, or a design-tier one shorter than 7;
- an `accepted` RFC with fewer than two named approvals;
- a `decided` date earlier than `comment_closes`;
- an `Alternatives considered` section containing nothing but the template's own prompt;
- an RFC with `touches_entrenched: true` whose `Charter impact` does not state that it strengthens
  the clause, or that is filed at design tier;
- a `docs/oss/rfcs/index.md` that is stale against the RFCs it claims to summarise.

`make rfc-index` regenerates the log. It is generated, never hand-edited, and carries a
`<!-- GENERATED -->` first line saying so.

Routine changes — bug fixes, docs, tests, implementing an approved spec — need no RFC. That is
deliberate; see below.

## Alternatives considered

- **GitHub Discussions as the record.** Free, already there, and threaded. Rejected: the record then
  lives on a platform Gravix does not control and cannot export faithfully, and a project whose
  charter promises data ownership should not keep its own constitutional history somewhere it cannot
  `git clone`. Discussions remain the right place to *argue*; the RFC file is where the argument is
  concluded.
- **Issue labels and a milestone.** Lighter, no new machinery. Rejected: an issue has no required
  sections, so "Alternatives considered" and "Charter impact" get skipped exactly when they matter,
  and there is nothing to diff or validate. Charter §6's approvals would be a comment thread rather
  than a field.
- **Require an RFC for every change.** Maximally transparent. Rejected as the worse failure: a
  process applied to a typo fix is a process people route around, and a routed-around process
  protects nothing. Three tiers, with routine changes exempt, is the version that survives contact
  with a Tuesday.
- **Do nothing.** Charter §6 stays unexecutable. The first charter amendment would then invent its
  own procedure under time pressure, which is the circumstance under which procedures are invented
  badly. This is the alternative that made the others worth the effort.

## Non-goals crossed

None. The seven non-goals in `docs/04-non-goals.md` are all about what Gravix ingests, stores and
queries; an RFC process touches none of them.

The one a reader might reasonably worry about is scope creep — that a decision log is the first step
toward a governance apparatus larger than the project. That is why this RFC specifies one directory,
one generated file, and six validation rules, rather than a working-group structure. If the process
needs a second tier of ceremony, that is a later RFC and it should have to argue for itself.

## Charter impact

None is amended. This RFC builds the mechanism §6 assumes: §6 is the authority for the 14-day window
and the two named approvals, and this process implements them rather than changing them.

No entrenched clause is touched. §7.1, §7.3 Q4 and §7.4 are untouched in text and in effect, and the
new `touches_entrenched` flag exists to make any future proposal that does touch them say so in
public before a vote rather than after one.

## Migration

Nothing for users of Gravix: no code, no schema, no configuration changes.

For contributors, one change: a design-tier change — a new public API, a schema change, a new
dependency, a new extension point — now needs an RFC before the implementing pull request, where
before it needed two approvals on the pull request itself. `GOVERNANCE.md` already described that
tier; what changes is that it now has somewhere to happen.

Decisions made before this RFC are not back-filled. Writing an RFC after the fact for a decision
that was not made this way would produce exactly the artefact §3 of the spec forbids: a description
of a decision presented as the decision. Where an earlier decision needs a record, it is recorded as
what it was, in `docs/oss/spec-defects.md` or the changelog.

## Unresolved questions

- **Who assigns RFC numbers when two land the same day?** Currently first to merge; the second
  renumbers. Workable at this size and obviously not at ten times it.
- **What happens to an RFC nobody comments on?** The window closing with silence is treated as
  consent today. That is the right default for a project with one maintainer and the wrong one for a
  project with fifty, and there is no principled line yet for when it flips.
- **Should a rejected RFC be reopenable, or must a new one supersede it?** `supersedes` exists and is
  unused. The first time someone wants to revisit a rejection will settle it better than guessing now.
