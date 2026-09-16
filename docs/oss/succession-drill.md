# The succession drill

Once a year, somebody who is *not* the primary custodian tries to get into each asset, unaided, and
writes down what happened.

That is the whole procedure, and it is the only part of [`succession.md`](succession.md) that
produces evidence. Everything else on that page is a claim about what would happen. This is the part
where somebody finds out.

## Why it is published, failures included

A published failed drill is more reassuring than an unpublished successful one.

An unpublished success is indistinguishable from a drill nobody ran, and the incentive to quietly
skip a drill is highest in exactly the years when it would have found something. Publishing the
failures is what makes the successes worth anything — and it removes the reason to fudge, because
a failure here is a normal result rather than an admission.

## The procedure

For each **provisioned** asset in the register, in order of blast radius:

1. The **secondary** custodian attempts recovery **without the primary's help**. Not a rehearsal,
   not a walkthrough, not the primary reading the steps aloud. They actually get in, or they do not.
2. Record the result: the date, who attempted it, whether it worked, and — on a failure — exactly
   where it stopped.
3. On a failure, fix the cause and **repeat step 1**. The drill is not complete until the attempt
   succeeds, and a failure carried into next year is a custody gap wearing a drill's clothes.
4. On a success, update that asset's `Verified` date in [`succession.md`](succession.md).
5. Publish the results here, in the year's section, before the drill is declared complete.

Assets that are **not provisioned** are skipped, and the skip is recorded. They hold nothing, so
there is nothing to recover; the register lists them so that they acquire two custodians before
anyone depends on them, not after.

### What counts as a failure

- The secondary could not get in at all.
- They got in, but only with information the primary supplied during the attempt.
- They got in, but the procedure written in the register was wrong, out of date, or incomplete.
- Nobody attempted it.

The last one is still a failure. A drill that did not happen is not a neutral result.

## 2026

**Status: NOT COMPLETED.**

| Asset | Attempted by | Result |
|---|---|---|
| GitHub account | — | **not attempted — no secondary custodian exists** |
| Release signing identity | — | **not attempted — no secondary custodian exists** |
| Container images | — | **not attempted — no secondary custodian exists** |
| npm package (Node SDK) | — | **not attempted — no secondary custodian exists** |
| PyPI project (Python SDK) | — | **not attempted — no secondary custodian exists** |
| Go module path | — | **not attempted — no secondary custodian exists** |
| `ee/` licence-signing key | — | skipped — not provisioned |
| `gravix.io` domain and DNS | — | skipped — not provisioned |
| `security@`, `conduct@`, `trademark@` | — | skipped — not provisioned |
| Docs-site hosting | — | skipped — not provisioned |
| Homebrew tap | — | skipped — does not exist |
| Trademark registration | — | skipped — not registered |

**What happened:** nothing, and that is the finding. Every provisioned asset has exactly one
custodian, so there is no second person to attempt an unaided recovery. Six of the twelve assets
could not be drilled for that reason; the other six do not exist yet.

**Why it is recorded as a failure rather than "not applicable":** because it is one. The drill's
purpose is to find out whether somebody other than the founder can keep Gravix running, and this
year's answer is a clear no. Filing that under "n/a" would make the absence of a second custodian
look like a scheduling detail rather than the central risk in
[`succession.md`](succession.md).

**Fixed by:** nothing yet. The cause is item 2 of
[what it would take to make that page true](succession.md#what-it-would-take-to-make-this-page-true)
— a second person — and it is a recruitment problem, not a documentation one. It is recorded as an
open risk in [`MAINTAINERS.md`](../../MAINTAINERS.md) and as waiting on the owner in
[`open-decisions.md`](open-decisions.md).

**Next drill:** within 30 days of a second custodian being appointed to any asset, rather than
waiting for the anniversary. The first real drill should happen while the appointment is recent
enough to fix, not eleven months later.

## Reading this page in a later year

If the section above is still the most recent one, this project's succession plan has not been
tested since it was written, and you should weigh that accordingly when deciding whether to depend
on Gravix. The register will tell you the same thing more precisely; the date on the newest section
here tells you it faster.
