<!-- Governance document. Changes follow GOVERNANCE.md's Design tier. -->
# The contribution ladder

Three levels — **Contributor → Reviewer → Maintainer** — with criteria you can check against your
own GitHub history before anyone nominates you.

The point of writing them down is that gaining influence in Gravix should be a matter of meeting
stated criteria, not of being noticed by the founder. Every criterion below is evidenced by
something public: a merged pull request, a review comment, a written decision. A nomination cites
the links; anyone can verify them; nobody has to take a judgement on trust.

There is no discretionary path, and there is no fourth level. Each extra rung is one more place for
someone to be stuck.

---

## The three levels

| Level | Can | Cannot |
|---|---|---|
| **Contributor** | Open issues and pull requests; comment; review informally | Merge; approve |
| **Reviewer** | Everything above, plus a binding approval on pull requests in their subsystem | Merge; grant levels; cut a release |
| **Maintainer** | Everything above, plus merge, release, and grant Contributor→Reviewer | Amend the charter (§6 stands); overrule a veto-holding role inside a sprint |

Everyone starts as a Contributor by opening their first issue or pull request. There is nothing to
apply for and nothing to sign: Gravix has no CLA, deliberately
([charter §3](00-open-core-charter.md)).

A **Reviewer**'s approval is binding within their subsystem — the parts of the tree they know. It is
recorded in [`MAINTAINERS.md`](../../MAINTAINERS.md) alongside the maintainers, with their level and
subsystems named, so an author can see whose approval will count on their change before they open it.

A **Maintainer** can merge, cut a release, and promote a Contributor to Reviewer. That is the top of
this ladder and it is a smaller set of powers than it sounds like — see
[What no level can do](#what-no-level-can-do).

The three **veto-holding roles** in [`10-agent-roster.md`](10-agent-roster.md) §4 — the License &
Boundary Auditor, Security, and QA — are *roles*, not rungs. A Contributor can hold one; a
Maintainer may hold none. Holding a veto is not a level, and reaching Maintainer does not grant one.

---

## Contributor → Reviewer

All four, evidenced publicly:

| # | Criterion | The public evidence |
|---|---|---|
| 1 | **≥5 merged pull requests**, at least 3 touching code rather than only docs | The merged pull requests, linked |
| 2 | **≥10 substantive review comments** on other people's pull requests | The comments, linked |
| 3 | **≥1 pull request that fixed a defect someone else reported**, end to end | The issue and the pull request that closed it |
| 4 | **No unresolved code-of-conduct matter** | The absence of an open report; see [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md) |

**Substantive** in criterion 2 means the comment identified one of: a defect, a missing test, or a
scope violation. It is deliberately narrow. "LGTM", a typo fix, and a formatting note are all
welcome and none of them count, because the question this criterion asks is whether someone's review
catches things — and a nomination answers it by linking ten comments that did.

**Process.** Any maintainer nominates, in a public issue, listing the links for all four criteria.
Two maintainers concur. While fewer than three maintainers exist, the sole maintainer nominates and
a **public 7-day comment window** runs with no sustained objection standing in its place — so the
ladder works at bus factor 1 without becoming one person's gift.

---

## Reviewer → Maintainer

All five, evidenced publicly:

| # | Criterion | The public evidence |
|---|---|---|
| 1 | **≥3 months as a Reviewer** | The dated entry in `MAINTAINERS.md` |
| 2 | **≥20 merged pull requests** in total | The merged pull requests |
| 3 | **Reviewed ≥15 pull requests**, having requested changes and not only approved | The reviews, including at least one changes-requested review that was acted on |
| 4 | **Demonstrated understanding of the charter**: has applied the Crippleware Test, or declined a request citing a non-goal, in public, at least once | The comment, RFC, or review where they did it |
| 5 | **No unresolved code-of-conduct matter** | The absence of an open report |

**Criterion 4 is the one that matters.** The other four measure volume and time, which anyone
persistent accumulates. Criterion 4 asks whether someone has said no to something on principle when
saying yes would have been easier — declining a feature request citing
[`docs/04-non-goals.md`](../04-non-goals.md), or ruling on
[charter §7.3](00-open-core-charter.md)'s five questions and landing on the answer that made less
money. A maintainer who has never declined anything on principle has not shown they will when it is
expensive, and the day it is expensive is the only day it counts.

Criterion 3's "requested changes" half is there for the same reason. A reviewer who approves
everything is not reviewing; they are agreeing.

**Process.** Any maintainer nominates in a public issue with the links. **Unanimous agreement of
existing maintainers**, and a **public 14-day comment window**.

---

## What the criteria deliberately do not include

- **Employment, a commercial relationship, or an NDA.** At no level. Not with the founder, not with any company, not with anyone who sells Gravix Pro. If a level ever required one, this document would be the wrong place to find out; it is stated here so the absence is on the record.
- **A contributor licence agreement.** There is none, by design (charter §3).
- **Being liked.** There is no "at the maintainers' discretion" clause anywhere on this page, and adding one would be a Design-tier change with a public RFC, which is exactly the discussion that should happen before a ladder quietly becomes a clique.
- **Volume of code.** Criterion 3 for Reviewer and criterion 4 for Maintainer are about judgement. Someone can meet the counting criteria and not be ready; someone who meets all of them is.

---

## Stepping down and inactivity

**Resigning.** Any level may be resigned at any time, with no explanation owed. Say so in an issue,
or email a maintainer; that is the whole process.

**Emeritus.** Six months with no review and no merge moves a Reviewer or a Maintainer to
**Emeritus**: recognition retained, access removed.

**This is not a punishment, and it is not a judgement about anyone's contribution.** Access that
nobody is using is a security liability — a credential that can be stolen from someone who would not
notice it had been. Removing it protects the person as much as the project. Emeritus status is
recorded in `MAINTAINERS.md` with the same respect as any other level.

**Returning.** An Emeritus member returns to their prior level **on request**, with no
re-qualification, no waiting period, and no vote. They earned it; they did not lose it by being
busy.

**Removal for cause.** Requires the agreement of **all other maintainers** and a **written, public
reason**. Both halves are load-bearing. Unanimity means no faction can remove a dissenter, and the
public written reason means removal cannot be done quietly — if the reason will not survive being
read by everyone, it is not a good enough reason.

Where a code-of-conduct matter is the cause, the reason is published without republishing private
details of a report; [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md) governs how a report is
handled, and this document governs only the loss of access that may follow.

---

## What no level can do

```
No level of this ladder confers the ability to:

  - amend the Open-Core Charter (see charter §6);
  - relicense any code (there is no CLA, deliberately — charter §3);
  - move a capability from the Apache-2.0 core into ee/ (charter §7.3 Q4 makes
    that impossible for anything already released open);
  - overrule a security, boundary, or acceptance veto inside a sprint.

These are not maintainer powers. They are not anyone's powers.
```

Amending the charter needs the charter's own §6 procedure: an RFC, 14 days' public comment, and the
approval of the CPO **and** the License & Boundary Auditor. A room full of maintainers agreeing does
not substitute for it, and neither does the founder.

---

## Where each level is recorded

| What | Where |
|---|---|
| Who holds which level, and their subsystems | [`MAINTAINERS.md`](../../MAINTAINERS.md) |
| Decision tiers, vetoes, and what is not up for a vote | [`GOVERNANCE.md`](../../GOVERNANCE.md) |
| How to contribute in the first place | [`CONTRIBUTING.md`](../../CONTRIBUTING.md) |
| The licence boundary the criteria refer to | [`00-open-core-charter.md`](00-open-core-charter.md) |

Every nomination, concurrence, comment window and removal happens in a public issue. A promotion
nobody can find a record of did not happen.
