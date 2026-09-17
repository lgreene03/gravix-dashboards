# The annual charter review

Once a year, we publish evidence of whether the charter's promises were kept. Measured, not
asserted.

## The statement that governs this

```
This review cannot loosen the charter's core promises.

It can strengthen them, add to them, or change anything else. It cannot make the
core less free, re-gate something already released open, or permit a dark
pattern — not by majority, not by council vote, not by founder decision, and not
by an annual review that finds it inconvenient.

If a future version of this document argues otherwise, the charter is being
violated by the mechanism meant to protect it, and you should treat that as the
signal it is.
```

That last paragraph is addressed to a reader in the future, which is the only audience an
entrenchment clause really has. Everyone reading it today already agrees.

## Why a review at all

Charter §5 lists success conditions with numeric targets. A promise nobody checks becomes
decorative, and the ones most likely to rot are the ones it would be convenient to stop measuring.

So this happens on a schedule, publishes an uncomfortable finding every year by construction, and
is produced from a tool rather than from somebody's recollection.

**Its audience is a person deciding whether to depend on Gravix.** Not a prospective customer, not
an investor, not us. If it ever starts reading like marketing, it has stopped working.

## Cadence

- Published within **30 days of the anniversary of the charter's ratification**, on the docs site
  and linked from the README.
- CI opens a review-due issue **60 days before** the anniversary, assigned to the CPO.
- **A year is never skipped.** A review that happens when convenient is not a review.
- Missing the window is itself reported in the following year's review:
  `charterreview: <year> review published <n> days late`.

## What goes in one

Seven sections, in this order, using [`_TEMPLATE.md`](_TEMPLATE.md):

1. **What we promised** — charter §5's table, verbatim.
2. **What actually happened** — the evidence record, unedited.
3. **Where we fell short** — *mandatory, minimum one item.*
4. **Charter violations** — with the commands that checked, even when the answer is none.
5. **The canary** — G7.4's state, both trends, and what it means.
6. **What we are changing** — each as an RFC link.
7. **What we are not changing, and why** — including what was asked for and declined.

### Section 3 is not optional

A transparency report with nothing uncomfortable in it is a press release, and the first year it
reads that way is the year people stop reading it.

`charterreview.RequireShortfalls` refuses a review whose section 3 is empty — including one that
says "None", which is an empty section wearing a hat:

```
charterreview: "Where we fell short" is empty; a review with no uncomfortable finding was not a review
```

If a year genuinely had no missed target, the unmeasurable metrics go here instead. There have
always been some, and there probably always will be.

## The evidence

```bash
make charter-evidence
```

`pkg/charterreview` computes every field from this repository or from a named source. Nothing in the
record is a number somebody typed.

**A field that could not be measured is `-1`, never `0`.** Zero would say "none of the builds were
green"; `-1` says "nobody measured this", and the reason is in the `unmeasurable` list beside it.
Several entries are there permanently: Gravix collects no telemetry (charter §7.4), so retention is
not a number we have, and inventing a proxy for it would be a dark pattern in a document about not
having dark patterns.

`capabilities_moved_to_ee` is `[]`, not `null`, when nothing moved — checked and found none, rather
than a field nobody populated.

Exit codes: `0` collected, `1` could not compute, **`2` a capability moved from core into `ee/`**,
**`3` the canary tripped**. The last two are non-zero because both must reach a person before
publication, not when somebody gets around to reading the JSON.

## The two findings that stop everything

### A capability moved into `ee/`

Charter §7.3 Q4 — once open, always open — is entrenched. Something that was released under
Apache-2.0 and is now behind a licence is a violation of the promise, and the review **opens with
it**:

```
charterreview: "<capability>" moved from core to ee/ during <year>; charter §7.3 Q4 violation
```

Escalate to the License & Boundary Auditor immediately. Do not wait for publication to raise it.

### The canary

G7.4: **OSS 30-day retention falling while Pro revenue rises.**

That conjunction means the free tier is being degraded in a way the revenue number rewards, which is
the failure an open-core company is structurally most likely to have and least likely to notice —
every individual decision looks defensible, and the aggregate is extraction.

A tripped canary triggers Loop L0 regardless of the revenue figure. A good quarter is not a reason
to wait.

Both trends being `unknown` means they were not checked. It does not mean they are fine, and the
review says which.

## Amendments

The review may propose amendments through charter §6: an RFC, 14 days' public comment, approval
from the CPO **and** the License & Boundary Auditor, and a changelog entry.

`charterreview.CheckAmendment` refuses a proposal that would weaken §7.1, §7.3 Q4 or §7.4 **before
publication**, not after. A review that proposed it and then withdrew it has still published the
proposal.

## The reviews

| Year | Review | Shortfalls | Violations |
|---|---|---|---|
| 2026 | [2026.md](2026.md) | 6 | none |
