# The maintainer council

The body that decides things when there is more than one person to decide them, and the limits it
cannot exceed no matter how many people are on it.

## The current state, first

**There is no council yet.** Gravix has one maintainer, so the minimum of three members in §1 is
not met and the project operates under [`GOVERNANCE.md`](../../GOVERNANCE.md)'s founder-led rules.
[`MAINTAINERS.md`](../../MAINTAINERS.md) says the same thing in the same words.

This document is written now rather than later on purpose. A governance model designed while
somebody has a stake in the outcome is a governance model shaped by that stake. Writing it before
there is a second maintainer means nobody's position is being protected, including the founder's.

## Composition

- **Every maintainer is a council member.** There is no separate election. A second body layered on
  top of the maintainer group is a second place to have the same argument.
- **Minimum three members** for the council to act. Below three, founder-led rules apply and
  `MAINTAINERS.md` states it plainly rather than implying a body that does not exist.
- **Maximum nine.** Above that, decisions stop being decisions.
- **No organisation holds more than one third of seats.** If employment changes push one employer
  over that line, the council records it publicly and the most recently joined member from that
  employer abstains on charter-tier votes until it resolves.

The employer rule is stated in advance because the alternative is discovering it during the vote it
would have decided. Nobody has to argue about whether it applies; it either does or it does not, and
the remedy is already written down.

## What the council decides, and by how much

| Tier | Threshold | Comment window |
|---|---|---|
| **Routine** — bug fix, docs, tests, implementing an approved spec | One maintainer approval | none |
| **Design** — new public API, schema change, new dependency, new extension point | Simple majority of the council, minimum two approvals | 7 days |
| **Charter** — anything altering the charter (§6) | Two-thirds of the council, **and** the License & Boundary Auditor's concurrence | 14 days |
| **Maintainer removal for cause** | All members except the subject | 14 days |
| **Entrenched clause** — charter §7.1, §7.3 Q4, §7.4 | **Cannot be weakened by any threshold.** Strengthening: two-thirds | 14 days |

Routine changes need no vote. A process applied to a typo fix is a process people route around.

Ordinary decisions deliberately do **not** require unanimity. Unanimity for everything is a veto for
everyone, and a body where any single member can stop anything is a body that stops things.

## What the council cannot do

Charter **§7.1** (the core is Apache-2.0), **§7.3 Q4** (once open, always open) and **§7.4** (no
dark patterns) are entrenched. They may be strengthened. **They may never be weakened** — not by a
majority, not by two-thirds, not by unanimity, not by a council that finds them inconvenient.

There is no threshold in the table above that unlocks them, and that is not an oversight. A
protection a sufficiently large majority can remove is a protection that lasts exactly until it
matters.

The council is bound by this exactly as the founder is. It did not grant itself these limits and it
cannot vote them away.

Three vetoes also sit outside the council's reach, per `GOVERNANCE.md`: the License & Boundary
Auditor on `ee/` placement, Security on releasing a known-exploitable vulnerability, and QA on
shipping an unproven acceptance criterion. Overruling one requires the charter §6 procedure, not a
council majority.

## The founder has no permanent seat

The founder is a maintainer, is a council member on the same terms as every other member, and is
removable for cause under the same threshold as anyone else — all members except the subject.

A permanent seat is a single point of failure with a nicer name. There is no casting vote, no
founder veto, and no tenure that counts for anything.

## Ties

```
A tied vote fails.

We do not break ties by seniority, tenure, founder status, or who spoke last. A
proposal that cannot secure more support than opposition does not proceed, and
it can be brought again with a better argument.

This makes the project slightly harder to change. That is the intended
trade-off: an observability tool people depend on should be conservative about
changing itself, and the cost of a good idea arriving a quarter late is lower
than the cost of a contested one landing on a narrow margin.
```

The status quo winning a tie is the conservative and honest default. Any other rule — seniority,
tenure, the founder, whoever spoke last — is a way of saying that some members' votes count more,
without saying it.

## Minutes

Every council decision is minuted publicly within **5 working days**, in
[`council-minutes/`](council-minutes/), using [the template](council-minutes/_TEMPLATE.md).

Minutes record the question, the positions argued, the vote **count**, the outcome, and the
follow-up issue or RFC.

They do **not** record who voted which way, unless a member asks to be recorded. That is
deliberate: it keeps the reasoning public — which is the part anybody can learn from — while
letting a member vote against their employer's commercial interest without it becoming a matter of
permanent record. A governance model that makes that expensive will eventually stop getting honest
votes.

## Disagreement

See [`conflict-resolution.md`](conflict-resolution.md). A council vote is the third of four stages,
not the first: most disagreements are resolved by two people noticing they are describing different
problems.

## Changing this document

This page is design tier. Changing the thresholds in it requires an RFC, a 7-day window and a
simple majority — except where a change would touch charter §6 or an entrenched clause, which is
charter tier and, for the entrenched clauses, not possible at all.
