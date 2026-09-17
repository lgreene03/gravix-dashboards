# Revoking maintainer rights

Read this **before** accepting maintainer rights, not after. Agreeing the exit terms while everyone
is happy is what makes an eventual exit uneventful.

## Removing access is not a judgement about a person

```
Removing access is not a judgement about a person.

Inactive access is a security liability, not a courtesy. We remove it quickly,
we say so plainly, and we restore it on request with no re-qualification if you
come back. Nobody has to explain why they stopped.
```

A credential nobody is using is one that can be stolen from somebody who would not notice. Removing
it protects the person as much as the project.

## What triggers it

| Trigger | What happens |
|---|---|
| **Resignation** | At any time, with no explanation owed. Say so in an issue or email a maintainer. |
| **Six months' inactivity** | Emeritus, per the [ladder](contribution-ladder.md). Recognition retained, access removed. |
| **Removal for cause** | Agreement of all other maintainers, and a **written public reason**. |

## Within 24 hours, in every case

1. **Merge and release access revoked.**
2. **`MAINTAINERS.md` and `CODEOWNERS` updated** — the record matches reality, or the record is
   worthless.
3. **`scripts/audit_access.sh` run to confirm revocation.** Believing access was removed is not the
   same as checking. The audit is what turns step 1 into a fact.
4. **For removal for cause only:** the written public reason, per [`GOVERNANCE.md`](../../GOVERNANCE.md).

Twenty-four hours is a deadline rather than a target. Access that lingers after somebody has stepped
away is access nobody is watching.

## Coming back

An Emeritus maintainer returns to their prior level **on request**: no re-qualification, no waiting
period, no vote. They earned it, and being busy for six months is not losing it.

Resignation works the same way. If you resign and later want back in, ask.

## Removal for cause

Two requirements, both load-bearing:

- **The agreement of all other maintainers.** Unanimity means no faction can remove a dissenter.
- **A written, public reason.** If the reason will not survive being read by everyone, it is not a
  good enough reason.

Where a code-of-conduct matter is the cause, the reason is published without republishing private
details of a report. [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md) governs how a report is
handled; this document governs only the loss of access that may follow.

## What is not revoked

- **Authorship.** Your commits keep your name and your sign-off. The DCO is a legal record and
  rewriting it would break every signature and every existing checkout.
- **Credit.** You stay in the release notes for everything you worked on. If you would rather not
  be, [`no-credit.md`](no-credit.md) — no reason required.
- **Your standing as a contributor.** Losing merge access is not being asked to go away.

## What was never granted, and so is never revoked here

Organisation ownership, billing, domain control, publishing credentials and `ee/` signing keys are
custody matters, held separately from merge access on purpose. Offboarding a maintainer does not
touch them, because onboarding one never touched them either. See `GRVX-1503`.
