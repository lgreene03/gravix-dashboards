# Which versions of Gravix are supported

**Security fixes are free, on every supported line, always.**

That is the first line of this document because it is the part that is not negotiable. Charter
§7.3 Q3: a security fix is a security control, and selling one would mean self-hosters run known
vulnerable code unless they pay. Paid support ([`GRVX-1504`](specs/GRVX-1504-paid-support-tiers.md))
sells response time and advice. It does not sell patches, there is no licence check anywhere in
`scripts/backport.sh`, and a test fails if one ever appears.

## The lines

| Line | Support | Receives |
|---|---|---|
| **Current** — the latest minor | Until the next minor | Everything |
| **LTS** — one minor per year, designated at release | **12 months** from designation | Security fixes, data-correctness fixes, and fixes for defects that lose data |
| **Previous LTS** | 3 months' overlap after a new LTS is designated | Security fixes only |
| Everything else | none | none |

One LTS per year, **designated at its release rather than retroactively**. A team planning an
annual upgrade needs to know which release to plan around before they need it, not afterwards.

The three-month overlap is an upgrade window, not a grace period. It exists so that a team is never
in the position where the only supported version is one they have not had time to test.

It is **added to** the outgoing LTS's support, not capped by it. On the annual cadence the previous
LTS's twelve-month window ends on the very day the new one is designated, so an overlap bounded by
that window would be exactly zero days long — which is the opposite of the point. During those
three months, three lines are supported at once.

The converse edge, stated plainly because it is the one that could surprise somebody: an LTS
superseded **early** gets three months from that point rather than the remainder of its year. Once
it is the previous LTS it receives security fixes only, and keeping it on the full LTS line for the
rest of its twelve months would be promising more than the table above says.

The live table of what is supported right now is in [`SECURITY.md`](../../SECURITY.md), and it is
**generated** from the release record by `make supported-versions`. CI fails if it goes stale. A
security policy that claims a support window the maintainers do not honour is worse than no policy.

## What is backported to an LTS

| Change | Backported |
|---|---|
| Security fix, any severity | **Yes**, free, always |
| Data-correctness fix — a wrong number, a lost fact, a broken recompute | **Yes** |
| A defect that loses data | **Yes** |
| A crash or hang in a supported path | Yes, at maintainer discretion, recorded on the backport |
| Performance improvement | No |
| New capability | No |
| Dependency bump with no security or correctness impact | No |

**A data-correctness fix is backported at the same priority as a security fix.** Charter-wise they
are the same class of problem: both mean somebody is acting on something untrue. Gravix's whole
argument is that its numbers are right, so a wrong number on a supported branch is not a lower tier
of urgency than a vulnerability.

A feature is never backported. An LTS that accumulates capability is not a stable line, it is a
slow-moving fork, and the teams who chose it chose it because it does not move.

This table is not only prose. It is `version.Eligible` in `pkg/version/support.go`, and
`scripts/backport.sh` refuses an ineligible change by calling it — so the script and this page
cannot drift apart.

## Backporting

```bash
./scripts/backport.sh --commit <sha> --to lts/v1 --kind security
```

The script:

1. **Refuses an ineligible change** — exit 3, naming the kind and why.
2. **Cherry-picks with `-x`**, so the commit message records
   `(cherry picked from commit <sha>)`. Every LTS commit names the `main` commit it came from. An
   LTS branch whose history cannot be traced back is a fork nobody can reason about.
3. **Runs the LTS branch's full test suite**, and refuses to leave the commit in place if it fails.

Exit codes: `0` applied, `1` conflict or test failure, `2` bad arguments, `3` not eligible.

`--kind` is inferred from a conventional-commit subject when you leave it off, and an inference it
is not sure of is **refused rather than guessed**. Backporting a feature by accident is how an LTS
stops being one.

## Keeping the branches alive

[`.github/workflows/lts-ci.yml`](../../.github/workflows/lts-ci.yml) runs the full suite plus
`make check-boundary`, `make build-oss` and `make test-oss` on every LTS branch — on every push
**and every Monday**.

The weekly run is the important half. An LTS branch that only builds when somebody touches it will
be broken by a dependency change and nobody will find out until a security fix is urgent. A
scheduled failure opens an issue, because a red run nobody looks at is the same as no run.

A new LTS branch is added to that workflow's matrix as part of designating it. An LTS branch
missing from the list is an LTS nobody is testing.

## The current state

Gravix has cut **no versioned release**. There is no LTS yet and nothing but the default branch is
supported. `docs/oss/releases.json` is empty and `SECURITY.md`'s generated table says so.

This document describes the policy the first release will be governed by, and the machinery is in
place so that designating that release is a matter of adding a line to the register rather than
building any of this under time pressure.

## Upgrading

See [the upgrade guide](../upgrade-guide.md), which covers the LTS-to-LTS case — a jump that skips
a year of releases and therefore a year of migrations at once.
