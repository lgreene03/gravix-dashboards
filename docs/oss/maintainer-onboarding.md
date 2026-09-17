# Granting maintainer rights

The checklist for making somebody a maintainer, and what they do and do not receive.

Nothing here is a formality. Every item has an owner, and each one is recorded as done in the public
grant issue — so the grant is checkable afterwards by anyone, including the person receiving it.

## What a maintainer receives

| Access | Scope | Why |
|---|---|---|
| Merge on the default branch | All paths | You cannot improve a bus factor without it |
| Tag and release | All releases | A maintainer who cannot release is decorative |
| Grant Contributor→Reviewer | Per the [ladder](contribution-ladder.md) | Distributes the promotion path |
| Triage: labels, milestones, close | All issues | Daily maintenance |
| Security advisory drafting | Private advisories | Disclosure must not queue behind one person |

## What a maintainer does not receive

- Organisation ownership
- Billing
- Domain or DNS control
- Package-registry publishing credentials
- `ee/` licence-signing key material

These are **custody**, not maintenance, and they are handled separately by `GRVX-1503`. Keeping them
apart means a compromised maintainer account cannot take the project's identity — it can merge bad
code, which is recoverable, rather than transfer the domain, which is not.

It is not a statement about trust. The founder's own account should not hold all of both either, and
the separation exists so that no single compromise is catastrophic.

## The checklist

Every item is recorded as done, with a link, in the grant issue.

- [ ] **Ladder criteria met and evidenced with links** — `oss-steward`
- [ ] **Nomination, unanimous maintainer agreement, 14-day public comment window closed** — `cpo`
- [ ] **Two-factor authentication verified on their account** — `security-engineer`
- [ ] **Signed commits or verified DCO history reviewed** — `security-engineer`
- [ ] **Charter read and acknowledged in writing**, specifically §7.1, §7.3 Q4 and §7.4 — `cpo`
- [ ] **`CODEOWNERS` entry added** for their subsystems — `oss-steward`
- [ ] **`MAINTAINERS.md` updated** — `oss-steward`
- [ ] **Access granted** — `cpo`
- [ ] **First release cut _with_ them, not for them** — `sre-release-manager`
- [ ] **Offboarding procedure read by both parties** — `oss-steward`

### The last two matter most

**Cutting a release with them.** Somebody who has never cut a release does not improve the bus
factor; they improve the *appearance* of one. The first release after a grant is driven by the new
maintainer, with the existing one watching and answering questions. If that has not happened, the
bus factor has not changed, whatever `MAINTAINERS.md` says.

**Reading the offboarding procedure first.** Agreeing the exit terms while everyone is happy is what
makes an eventual exit uneventful. Nobody negotiates well on the day they are leaving, and the
version written in advance is the fair one.

## Two-factor is not negotiable

`two-factor authentication is required before merge access`. No exceptions, no "I will set it up
next week". An account with merge rights and no second factor is a single stolen password away from
a supply-chain compromise of everyone running Gravix.

## When nobody qualifies

That is a real outcome and it gets reported as one. The criteria in the
[contribution ladder](contribution-ladder.md) are mechanical, and granting rights to somebody who
has not met them to improve a number is how a project ends up with maintainers who do not maintain.

`MAINTAINERS.md` states the real bus factor, including when it is 1. An adopter is taking that risk
whether or not the file mentions it, and a maintainers file that implies more depth than exists
misleads them about what they are taking on.

## After the grant

`scripts/audit_access.sh` runs weekly and flags any discrepancy between who holds access, who is in
`MAINTAINERS.md`, and who is in `CODEOWNERS`. A discrepancy usually means someone was added or
removed without this checklist, which is the failure this document exists to prevent.

## Stepping away

[`maintainer-offboarding.md`](maintainer-offboarding.md). Read it before you accept, not after.
