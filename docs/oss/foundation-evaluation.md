# Should Gravix be donated to a foundation?

**Verdict: `NOT YET`.** The conditions that would change it are in
[Recommendation](#recommendation), and they are measurable on purpose.

| | |
|---|---|
| **Produced by** | `cpo`, with `oss-steward` and `license-boundary-auditor` (`GRVX-1506`) |
| **Comment window** | **opened 2026-09-16, closes 2026-09-30** — 14 days, per charter §6 |
| **Status** | not final until the window closes |
| **Sources retrieved** | 2026-09-16 |

This is a decision document, not a survey. A survey of foundations would have been easier to write
and would have committed to nothing, which is the failure mode this format exists to prevent.

## The question

**Should the Gravix core be transferred to a software foundation, which would then hold its
copyright position, its trademark and its governance?**

What is being evaluated:

- Transferring the Apache-2.0 core to a foundation, permanently and irreversibly.
- Transferring the **Gravix** name and logo with it, because every foundation examined requires that.
- Adopting that foundation's contribution rules in place of charter §3's DCO-never-CLA commitment.

What is **not** being evaluated:

- Changing any licence. The core stays Apache-2.0 in every branch of this decision.
- Whether `ee/` continues to exist as a commercial tier. That is a separate question, though this
  one cannot be answered without touching it.
- Anything happening now. This produces an evaluation. A donation would be a charter-tier decision
  under §6: an RFC, 14 days' public comment, approval from the CPO **and** the License & Boundary
  Auditor, and a changelog entry.

Two framings are deliberately excluded. "Would a foundation help us grow?" is a marketing question
and foundations are bad at marketing. "Would a foundation make us look serious?" is a question about
appearances, and a project that donates itself for credibility has answered the wrong question — the
donation is irreversible and the credibility is not.

## What a foundation would give us

Five things, in descending order of how much Gravix actually needs them today.

**1. A succession that does not depend on one person.** This is the real argument and it is much
stronger than the other four combined. [`succession.md`](succession.md) records that every live
identity asset — the GitHub account, the release signing identity, the container images, the npm and
PyPI accounts — has exactly one custodian, and that the repository sits under a **personal** GitHub
account which by construction cannot have a second owner. `./scripts/verify_custody.sh` fails today
and the 2026 recovery drill is recorded as not completed. A foundation holds assets institutionally.
It does not go unreachable.

**2. Trademark custody that outlives the founder.** `TRADEMARK.md` reserves the Gravix name as an
unregistered common-law mark, held by an individual. A foundation would hold it, register it if
worth registering, and enforce it without depending on anybody's personal funds or attention.

**3. Governance neutrality, in a form an outsider can verify.** [`GOVERNANCE.md`](../../GOVERNANCE.md)
and [`council.md`](council.md) describe a council with one member. The rules are real and the
neutrality is currently a promise about future behaviour. A foundation's neutrality is structural:
it does not rest on the current maintainer continuing to mean it.

**4. Procurement credibility.** Some buyers have a rule about single-vendor open source. A
foundation clears that rule in a way no document does. This is a real benefit and a small one at
Gravix's size, and it is the benefit most likely to be overstated by somebody who wants to donate.

**5. A contributor pipeline.** Foundations bring some. Less than people expect, and most projects
that credit a foundation for their contributors had them first.

## What it would cost us

**The trademark, and with it the business model.** Every foundation examined requires the name. CNCF
is explicit: a project must "have ownership of its trademark and logo assets transferred to the
Linux Foundation or a Linux Foundation project hosting entity" (charter §11(a)(i)). Gravix's paid
tier is `ee/`, sold under the Gravix name. If the name belongs to a foundation, selling "Gravix Pro"
becomes something the foundation's trademark policy permits or does not, decided by people with no
obligation to the commercial tier. That is not a detail to negotiate afterwards. It is the model.

**The DCO commitment.** See [The CLA problem](#the-cla-problem). Charter §3 refuses a CLA
specifically so that no future owner can relicense contributed code, and calls that "the strongest
available signal that §7.1 is real". Adopting a foundation's contribution rules where they require
an agreement is a reversal of that, and it requires its own charter-tier RFC.

**Unilateral direction.** Today a non-goal is enforced by one person saying no.
[`docs/04-non-goals.md`](../04-non-goals.md) is short, opinionated and load-bearing: no agents, no
distributed tracing, no logs platform, no query language. Under foundation governance those become
things a sufficiently motivated group of contributors can vote to add. The charter's whole thesis is
that Gravix is useful *because* of what it refuses, and a governance structure optimised for
inclusion is not optimised for refusal.

**Process overhead.** Sandbox applications, due-diligence reports, annual reviews, TOC liaison,
graduation criteria. Real, survivable, and a meaningful tax on a project with one maintainer.

**Irreversibility.** A donation cannot be undone. Every other decision on this page can.

## The `ee/` problem

This is the section that decides the verdict, and the answer is close to categorical.

**The Apache Software Foundation prohibits it outright.** The ASF's third-party licence policy puts
**Business Source License 1.1** in Category X, and states that "Apache projects may not distribute
Category X licensed components, in source or binary form; in ASF source code or in convenience
binaries" — grouped under field-of-use restrictions, "Not OSD-compliant"
([apache.org/legal/resolved.html](https://www.apache.org/legal/resolved.html), retrieved
2026-09-16). `ee/` is BUSL-1.1 with a 2-year Change Date. There is no version of "donate Gravix to
the ASF" in which `ee/` comes along.

**The CNCF requires OSI-approved licences for project code**: "For code development, use only
OSI-approved open source software licenses" (charter §11(a)(ii),
[cncf/foundation](https://github.com/cncf/foundation/blob/main/charter.md), retrieved 2026-09-16).
BUSL-1.1 is not OSI-approved. The CNCF charter says nothing either way about a commercial product
sold *alongside* a donated project, and many CNCF projects have exactly that. So the CNCF route is
not blocked — but it is the **core only**, with `ee/` outside, under a name the Linux Foundation
owns.

Three options, with their consequences stated rather than implied:

| Option | What happens | Consequence |
|---|---|---|
| **Donate the core, keep `ee/` outside** | Only CNCF-shaped foundations permit it. The core is the foundation's; `ee/` is a separate product that needs the foundation's trademark permission to be called Gravix anything | The paid tier's right to its own name becomes somebody else's decision, revocable, with no obligation to the business. The open-core model does not survive losing the name. |
| **Abandon `ee/` entirely** | The whole repository becomes Apache-2.0 and any foundation will take it | There is no commercial tier, so there is no funded maintenance, so the bus factor problem that motivated the donation is solved by the project becoming a hobby. Honest, viable for some projects, and a different project from this one. |
| **Do not donate** | Status quo | Succession stays a document rather than an institution — which is precisely the risk `succession.md` says is unmitigated. |

**The finding, stated plainly because a hedged version would be useless:** no foundation examined
will accept Gravix *as it is*. One (ASF) refuses the BUSL component categorically; the others require
splitting the project and handing over the name that the remaining half is sold under. That is not a
process obstacle to be worked through. It is the structure of the thing.

## The CLA problem

Charter §3: **"DCO sign-off (`Signed-off-by:`), not a CLA. A CLA would let a future owner relicense
contributed code; the DCO cannot. This is a deliberate constraint on our own future behaviour and is
the strongest available signal that §7.1 is real."** The consequence is stated and accepted in the
charter itself: *"we can never relicense the core. That is the point."*

The foundations differ sharply here, and the difference matters more than most summaries admit.

**The ASF requires a contributor agreement.** All project maintainers and contributors of large
contributions must submit a signed **ICLA** before receiving commit rights; corporations assigning
employees sign a **CCLA**, which does not remove the individual ICLA requirement; and donating an
existing body of software requires a **Software Grant Agreement**
([apache.org/licenses/contributor-agreements.html](https://www.apache.org/licenses/contributor-agreements.html),
retrieved 2026-09-16). Contributors "retain full rights to use their original contributions for any
other purpose outside of Apache", so it is a licence, not an assignment — but it is exactly the
instrument charter §3 refuses.

**The CNCF does not.** Its charter mandates the DCO for all contributions — every contribution must
be "accompanied by a Developer Certificate of Origin sign-off" and "made under the Apache License,
Version 2.0" (§11(b)(ii)) — and leaves a CLA as an optional per-project choice (§11(b)(i))
([cncf/foundation](https://github.com/cncf/foundation/blob/main/charter.md), retrieved 2026-09-16).
**A CNCF donation would not require reversing charter §3.** That is the single strongest point in
the CNCF's favour and it deserves to be said clearly rather than buried in a comparison table.

**The asymmetry, stated honestly.** A foundation CLA is materially safer than a company CLA. A
foundation has no shareholders, no acquirer and no board with a duty to maximise return; the
scenario charter §3 guards against — an owner relicensing contributed code — is the thing
foundations exist to prevent. Elastic, HashiCorp and Redis each had a company that could relicense,
and each did. A foundation is the opposite structure.

That is true, and it does not make an ASF-shaped donation free. It changes the argument from "a CLA
is dangerous" to "a CLA is a reversal of a stated commitment, in exchange for a structure that makes
the danger smaller". Reversing a public commitment costs something even when the new arrangement is
safer, because the commitment's value was that it was not negotiable. A project that says "no CLA,
ever" and then signs one has taught its readers how much its promises are worth.

## What the precedents show

From [`01-competitive-thesis.md`](01-competitive-thesis.md) §5, which is the project's own research
and is cited rather than repeated:

- **Three whole-repository relicensings, three foundation-backed forks.** Elastic → OpenSearch (now
  Linux-Foundation-governed), HashiCorp → OpenTofu (Linux Foundation accepted it **26 days** after
  the fork), Redis → Valkey (forked within days, backed by AWS, Google Cloud, Oracle, Ericsson,
  Snap).
- **No consequential fork from an `ee/`-shaped carve-out.** SigNoz and CockroachDB both run a
  BUSL-licensed commercial component beside a permissive core, and neither produced one.
- **Grafana Labs is the control case**: Apache-2.0 → AGPLv3, landed on corporate banned-licence
  lists, stayed OSI-approved, no consequential fork.

What each structure actually protected is the part worth extracting, and it is not the part usually
quoted:

**A foundation protected the community, not the project.** In all three cases the foundation arrived
*after* the relicensing, as the place the fork went. The Linux Foundation did not stop Elastic,
HashiCorp or Redis from relicensing — it gave everyone else somewhere to go afterwards. A foundation
is insurance for users against the owner, and Gravix has already bought a cheaper policy: charter
§7.3 Q4 entrenched, and a DCO that makes relicensing the core legally impossible rather than merely
promised.

So the precedents argue **less** for donating than they first appear to. They argue for exactly what
Gravix already did — and they say nothing at all about the risk Gravix actually has, which is not
a hostile owner but an absent one. No project in that table was forked because its maintainer
stopped answering.

## Candidate foundations

Every requirement below is quoted from the organisation's own document, with the URL and the date it
was retrieved. A requirement that could not be read at its source is marked **UNVERIFIED**, and the
verdict does not rest on any of those.

| Foundation | What it requires | What it provides | Position on a commercial tier | Source |
|---|---|---|---|---|
| **Apache Software Foundation** | A signed **ICLA** from every committer, a **CCLA** from corporations assigning employees, and a **Software Grant Agreement** to donate an existing codebase. Apache-2.0 for the code. | Institutional permanence, trademark custody, a governance model with decades of precedent | **Excludes it.** BUSL-1.1 is Category X: "Apache projects may not distribute Category X licensed components, in source or binary form" | [contributor-agreements](https://www.apache.org/licenses/contributor-agreements.html) · [legal/resolved](https://www.apache.org/legal/resolved.html) — retrieved 2026-09-16 |
| **CNCF** (a Linux Foundation project hosting entity) | Trademark and logo assets transferred to the Linux Foundation (§11(a)(i)); OSI-approved licences for code (§11(a)(ii)); **DCO on every contribution**, Apache-2.0 (§11(b)(ii)); a CLA only if the project chooses one (§11(b)(i)) | The same, plus an ecosystem Gravix's problem domain sits inside, and a sandbox→incubation→graduation ladder | **Silent on it.** No charter provision addresses a proprietary component sold alongside; the donated code must be OSI-licensed, so `ee/` stays outside | [cncf/foundation charter](https://github.com/cncf/foundation/blob/main/charter.md) — retrieved 2026-09-16 |
| **Linux Foundation** (directly, outside CNCF) | Trademark assignment to the LF, per the same clause the CNCF charter cites | Hosting, legal and administrative services; used by OpenTofu and OpenSearch | Not read at source | **UNVERIFIED** — `linuxfoundation.org` was unreachable from this environment on 2026-09-16. The trademark requirement above is quoted from the CNCF charter, which names the LF as the recipient; nothing else about the LF's direct-hosting terms is asserted here |
| **The Commons Conservancy** | Reported to leave copyright assignment to each Programme's own statutes rather than requiring it | Reported to hold assets for a Programme without taking copyright | Not read at source | **UNVERIFIED** — `commonsconservancy.org` and `dracc.commonsconservancy.org` were both unreachable from this environment on 2026-09-16. This row is a lead to follow, not a finding, and the recommendation does not use it |
| **Remain independent** | Nothing | Nothing that is not already built: `GRVX-1502` governance, `GRVX-1503` succession, `GRVX-1508` charter review | Keeps it | — |

Two of five rows could not be verified at source. That is recorded rather than smoothed over,
because a comparison table is exactly where an unchecked claim becomes a decision — and the
Commons Conservancy row is the one that, if true, would most change the analysis. Somebody with
working access should read it and revise this page.

## Recommendation

### `NOT YET`

Do not donate Gravix to a foundation now. Revisit when **all five** of the following are true, each
measured by a command in this repository rather than by judgement:

| # | Condition | How it is measured | Today |
|---|---|---|---|
| 1 | Every critical subsystem has an effective bus factor of at least 2 | `./scripts/bus_factor.sh` exits 0 **with no recorded-gap warnings** | 12 recorded gaps |
| 2 | Every identity asset has at least two custodians | `./scripts/verify_custody.sh` exits 0 | exits 1 on six assets |
| 3 | At least 3 non-founder maintainers, from at least 2 organisations | `./scripts/audit_access.sh`, against `MAINTAINERS.md` | 0 |
| 4 | At least 10 adopters who opted in publicly | `./scripts/verify_adopters.sh` exits 0 with ≥10 verified rows | 0 verified |
| 5 | The value of `ee/` is a published number, not a guess | An annual charter review publishes a Pro-revenue figure that is not `-1` (`pkg/charterreview`) | `-1` — not measured |

**Why these five and not "when the time is right".** A deferral nobody can falsify is a `DO NOT
DONATE` without the courage to say so, and it is the most common thing a founder writes when they do
not want to let go. Each condition above can be checked by running a command, by anybody, without
asking the author whether they feel ready.

**Why each one is load-bearing rather than decorative:**

1 and 3 exist because **a foundation does not fix a one-person project; it inherits one.** CNCF
sandbox due diligence asks who maintains a project and from where. Donating now would transfer a
bus-factor-of-one into an institution that cannot fix it either, and the foundation's neutrality
would be a formality over a project still driven by one person.

2 exists because **a donation is a custody transfer, and you cannot transfer what one person holds.**
Handing over a trademark, a repository and release infrastructure requires somebody able to hand
them over. `succession.md` says nobody else can reach any of it.

4 exists because foundations accept projects with users. Gravix's adopter list has zero verified
entries, and `scripts/verify_adopters.sh` will not count an unverified one (F-049).

5 exists because **the `ee/` disposition is the whole decision and it cannot be made blind.** If
`ee/` is worth little, abandoning it and donating everything is genuinely attractive. If it is worth
a lot, giving the Gravix name to a foundation ends the business model. The charter review currently
reports `-1` for Pro revenue, and `-1` means nobody measured it. Deciding the largest irreversible
question in the project's history on an unmeasured number would be indefensible.

**If the conditions are met, the recommendation is the CNCF, not the ASF.** The CNCF mandates the
DCO and makes a CLA optional, so charter §3 survives intact; the ASF requires an ICLA from every
committer and puts BUSL-1.1 in Category X, which rules out `ee/` in any form. That is not a
preference, it is the only route that does not require reversing something already written down.

### The strongest argument against this recommendation

Stated at full strength, because a weakened version of it would be a way of dismissing it:

> Gravix has a bus factor of one, a succession plan that fails its own audit every month, and an
> identity held in a personal account that cannot have a second owner. A foundation fixes precisely
> that, and it fixes it **now**. Every one of the five conditions above is a reason to wait that the
> project itself controls — and the conditions are hard. Three maintainers across two organisations
> is a recruitment problem with no deadline; a published Pro-revenue figure requires a commercial
> tier that has not sold anything. A founder who wanted to keep control indefinitely, while
> appearing to have evaluated the alternative rigorously, would write exactly this document. And
> for as long as the conditions go unmet, it is the **users** who carry the succession risk, not the
> founder. If Gravix never reaches them, `NOT YET` will have been `NO` with better manners, and
> nobody will be able to point to the moment the answer changed.

The honest response is that this argument is largely correct and is not fully answerable. What
limits it is narrow: the conditions are measured by committed commands rather than by opinion, so
the day they are met is a fact rather than a feeling, and anyone can check. It is a weaker defence
than "we will know when we are ready", because it can be proved wrong. That is the only reason to
prefer it.

### Whose interest this serves

**These diverge, and the recommendation follows the commercial interest more closely than the
users'.** Saying so is the only way the rest of this page is worth reading.

A user of Gravix would be better off today if the project were in a foundation: their succession
risk would be institutional rather than personal, and it would be fixed this year rather than
conditionally. The reasons to wait are mostly reasons that protect the project's ability to fund
itself — which, over a long enough horizon, is also the users' interest, because an unfunded project
with three volunteers is not obviously safer than a funded one with a single maintainer. But that is
an argument about the long run, made by the person who benefits in the short run, and it should be
read as such.

The asymmetry is worth naming precisely: **the users bear the risk now, and the benefit arrives
later, if it arrives.**

### What the founder gains and loses

Not donating, the founder keeps: the trademark, the right to monetise `ee/`, unilateral authority
over direction and non-goals, and the option to sell the project or take investment. A donation
forecloses the last of those permanently and materially dilutes the others.

That is a real influence on this recommendation and pretending otherwise would fool nobody. A reader
should weigh the five conditions knowing that the person who wrote them benefits from their not
being met yet.

### Entrenchment survives any version of this

Charter §7.1 (the core is Apache-2.0), §7.3 Q4 (once open, always open) and §7.4 (no dark patterns)
are entrenched. Nothing in this evaluation proposes weakening any of them, and no donation may.

**`license-boundary-auditor` ruling, 2026-09-16:** the `NOT YET` verdict changes no licence, moves
no capability between the core and `ee/`, and alters no clause of the charter. The entrenched clauses
are untouched. Recorded per §6 step 6; a `DONATE` verdict would have required the separate ruling
§5.4 specifies, on whether the receiving foundation's governance preserves §7.1, §7.3 Q4 and §7.4 —
and a foundation that could not guarantee them would be a reason not to donate rather than a reason
to amend them.

### No RFC is opened

The verdict is `NOT YET`, so there is nothing to propose. Charter §6 requires an RFC for a
charter-tier change; declining to make one is not a change.
[`docs/oss/rfcs/index.md`](rfcs/index.md) is therefore unchanged, and `GRVX-1506` §4.2 only calls for
regenerating it if the evaluation produces an RFC.

When the five conditions are met, the RFC that follows must cover what §5.4 lists: the CLA
implications for charter §3, the `ee/` disposition, trademark transfer per `GRVX-1503`, and how
§7.1, §7.3 Q4 and §7.4 would be preserved by the receiving foundation's governance.

## What we do instead, if we do not donate

"No" is only a responsible answer if it names how the goals a foundation would have served get met
another way. Each of these is built, not planned:

| What a foundation would have given | What does it instead | State today |
|---|---|---|
| Governance neutrality | [`GRVX-1502`](specs/GRVX-1502-governance-maturity.md) — the maintainer council, published thresholds, a tie that fails rather than being broken by a chair, and conflict resolution | Written and tested. One member, so the thresholds are theory. |
| Succession | [`GRVX-1503`](specs/GRVX-1503-succession-plan.md) — [`succession.md`](succession.md), the drill, and a monthly audit | Written, and **failing**, which is the honest state and the point of publishing it |
| A bus factor above one | [`GRVX-1210`](specs/GRVX-1210-grant-maintainer-rights.md) and [`GRVX-1507`](specs/GRVX-1507-codeowners-bus-factor.md) — the ladder, the onboarding checklist, the subsystem register and the monthly audit | The machinery exists; the people do not |
| An institution that checks the promises | [`GRVX-1508`](specs/GRVX-1508-annual-charter-review.md) — the annual charter review, produced from a tool, with a mandatory uncomfortable finding | First review published; 6 shortfalls recorded |
| Protection against an owner relicensing | Charter §3's DCO-never-CLA and §7.3 Q4, entrenched | In force, and **stronger** than what a foundation provides: it is legally impossible here rather than institutionally unlikely |

Four of those five are honest about not being finished. That pattern is the actual argument for
`NOT YET` rather than `DO NOT DONATE`: the alternatives are real mechanisms with real gaps, and the
gaps are the same ones the five conditions measure. If they close, Gravix will not need a foundation
for the reasons listed here. If they do not close, that is the evidence that it does.

---

*Comment on this evaluation by opening an issue against `docs/oss/foundation-evaluation.md`. The
window closes 2026-09-30. Announcing it more widely is the repository owner's call, and is recorded
in [`open-decisions.md`](open-decisions.md).*
