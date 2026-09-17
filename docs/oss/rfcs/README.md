# RFCs

A written, public, archived path for design changes and charter amendments.

[**The decision log is `index.md`**](index.md) — every RFC ever opened, in every state. Start there
if you want to know whether something has already been decided.

## When you need one

| Tier | What it covers | Comment window | Approvals |
|---|---|---|---|
| **Routine** | Bug fix, docs, tests, implementing an approved spec | — | One maintainer, on the pull request. **No RFC.** |
| **Design** | New public API, schema change, new dependency, new extension point | 7 days | Two maintainers |
| **Charter** | Anything altering [`00-open-core-charter.md`](../00-open-core-charter.md) | **14 days** | The CPO **and** the License & Boundary Auditor (charter §6) |

The routine row is the important one. A process applied to a typo fix is a process people route
around, and a routed-around process protects nothing.

## How to open one

1. Copy [`0000-template.md`](0000-template.md) to `NNNN-short-title.md` with the next free number.
2. Fill in the front matter. `comment_closes` is `opened` + 7 for design tier, + 14 for charter tier.
   CI checks the arithmetic.
3. Fill in all eight sections. CI fails on a missing one.
4. Run `make rfc-index` and commit the regenerated `index.md`.
5. Open a pull request using the RFC template
   ([`.github/PULL_REQUEST_TEMPLATE/rfc.md`](../../../.github/PULL_REQUEST_TEMPLATE/rfc.md)) — append
   `?template=rfc.md` to the pull-request URL.
6. Let the window run. When it closes, set `status`, `decided` and `approvals`, regenerate the index,
   and merge.

For a **charter-tier** RFC, `approvals` must name both roles charter §6 requires — `cpo` and
`license-boundary-auditor` — because §6 does not say "two people", and two approvals from anyone
else is not the thing it asks for. `alice (cpo)` and a bare `cpo` both count; which handle held the
role is a question for the comment window, and that the role approved at all is the part CI insists
on.

## What CI checks

`make rfc-check` runs on every pull request and fails on:

| Rule | Message |
|---|---|
| Charter-tier window under 14 days | `rfc <n>: charter tier requires a 14-day comment window, has <d>` |
| Accepted with too few approvals | `rfc <n>: accepted with <a> approvals, tier requires <b>` |
| A charter RFC whose approvals do not name both §6 roles | `rfc <n>: charter tier requires an approval naming "<role>"` |
| Decided before the window closed | `rfc <n>: decided <d> before comment window closed <c>` |
| No genuine alternative | `rfc <n>: "Alternatives considered" contains no alternative` |
| Entrenched clause touched without a strengthening statement | `rfc <n>: touches an entrenched clause; Charter impact must state that it strengthens, not weakens` |
| A missing required section | `rfc: a required section is missing: rfc <n>: "<section>"` |
| Stale index | the `diff -u` output |

None of these is a judgement about whether the proposal is any good. Whether an idea is right is for
the comment window; whether it waited, whether enough people approved it, and whether the
entrenchment question was asked are for CI, because those are the ones nobody re-checks by hand.

## The entrenchment guard

Charter §7.1 (the core is Apache-2.0), §7.3 Q4 (once open, always open) and §7.4 (no dark patterns)
are **entrenched**. They may be strengthened. They may never be weakened.

No parser can tell strengthening from weakening — that is a reading of a proposal, not a property of
its text. So the rule is procedural instead: an RFC with `touches_entrenched: true` must state, in
`## Charter impact`, that it **strengthens** the clause, and it must be filed at charter tier. CI
rejects it otherwise.

The flag exists so the question is asked in public, before the vote, rather than discovered after
it. A reviewer still has to decide whether the statement is true. What the guard guarantees is that
somebody had to write it down.

## Why rejected RFCs stay

Permanently, in the log, with their reasons.

A decision log that records only what was accepted is a marketing page. The value is in seeing what
was declined and why — otherwise the same well-argued request arrives every six months and gets
re-argued from nothing, by people who had no way of knowing it was already considered carefully.

`status: withdrawn` and `status: rejected` are outcomes, not failures. An RFC that changed someone's
mind and was then withdrawn did its job.

## What an RFC cannot do

Re-litigate one of the seven non-goals in [`docs/04-non-goals.md`](../../04-non-goals.md) at design
tier. Those change through charter §6 or not at all, and an RFC that crosses one is a charter-tier
RFC whatever else it is about — which is what `## Non-goals crossed` exists to surface.

It also cannot be back-filled. A decision made in a private channel and written up afterwards
produces a description of a decision, not the decision. The record is the decision.
