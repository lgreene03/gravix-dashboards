# Succession: where Gravix's identity lives, and who can get it back

If the person who set all this up stops answering, does the next Gravix release still come from the
same place, signed by the same identity, at the same URLs?

Today the answer is **no**, and this page exists to say so precisely rather than leave somebody to
find out. [`docs/disaster-recovery.md`](../disaster-recovery.md) covers data, which has backups.
This covers identity, which does not.

**Nothing on this page is a credential.** Every column is a location, a role, or a consequence. No
key, token, password or recovery material is written here — not encrypted, not partially redacted,
not "safe because it is old". `scripts/verify_custody.sh` refuses this file if one ever appears in
it, because a repository is not a vault and this one is public.

## The rule this register is audited against

```
Every asset here has at least two people who can recover it.

Not two people who know about it — two people who have independently
demonstrated, in the last twelve months, that they can actually get in. We test
this annually, because a recovery procedure nobody has executed is a hypothesis.
```

**Today, not one asset meets that rule.** Gravix has one maintainer, and every account below has
exactly one person who can reach it. The rule is what the audit enforces and what the project is
working toward; the register below is what is actually true. Publishing the rule as though it were
the state would be the single most useful thing to lie about, so it is stated as a rule and
contradicted immediately.

`./scripts/verify_custody.sh` **fails today**, deliberately, and will keep failing until a second
custodian exists. See [What it would take to make this page true](#what-it-would-take-to-make-this-page-true).

## The register

| Asset | Where it lives | Primary custodian | Secondary custodian | Recovery path | Verified | Blast radius |
|---|---|---|---|---|---|---|
| GitHub account | `github.com/lgreene03` — a **personal** account, not an organisation | founder — @lgreene03 | — none | GitHub's account-recovery process, which returns access to the account holder. A personal account has exactly one owner; a collaborator cannot inherit one. | 2026-09-16 | **total** — the repository, every release, the signing identity, the container images and the canonical clone URL all hang off this one account |
| Release signing identity | keyless cosign via GitHub OIDC (`GRVX-709`), asserted by `.github/workflows/release.yml` | founder — @lgreene03, through control of the repository | — none, by design | Whoever controls the repository controls the identity. There is no key to escrow, split or hand over. | 2026-09-16 | Releases can no longer be signed as Gravix. Already-published signatures stay verifiable — they are in a public transparency log, not in anyone's custody. |
| Container images | `ghcr.io/lgreene03/gravix-dashboards-*` | founder — @lgreene03 | — none | Follows the GitHub account; GHCR packages are owned by it. | 2026-09-16 | No new images can be pushed under the published names. Existing tags stay pullable while the account exists. |
| npm package (Node SDK) | npmjs.com, published by `.github/workflows/publish-sdks.yml` using the `NPM_TOKEN` repository secret | founder — @lgreene03 | — none | npm account recovery, against the account behind that token. | 2026-09-16 | The Node SDK cannot be republished, and its name is stranded: npm does not reassign a package to somebody who cannot prove they own the account. |
| PyPI project (Python SDK) | pypi.org, published by the same workflow using the `PYPI_API_TOKEN` repository secret | founder — @lgreene03 | — none | PyPI account recovery, plus PyPI's own project-takeover policy for abandoned projects. | 2026-09-16 | As npm, with one difference: PyPI has a documented path for transferring an abandoned project, so this one is recoverable by the community given enough time. |
| Go module path | `github.com/lgreene/gravix-dashboards`, declared in `go.mod` | founder — @lgreene03 | — none | Follows the GitHub account. **This path does not currently resolve** — the repository is at `lgreene03`, not `lgreene`. See F-050. | 2026-09-16 | The Go SDK is not installable at the path the docs publish, today, for everyone. This is a live defect rather than a succession risk. |
| `ee/` licence-signing key | not provisioned — `pkg/license/pubkey.go` compiles in the **development** key, which signs `testdata/` fixtures and nothing else | — none yet | — none yet | n/a until a production key exists. It must be escrowed with two custodians **before** it signs its first real licence. | — | When it exists: every Pro licence in the field. A lost private key means no new licences can be issued; a leaked one means any licence can be forged, and charter §7.5 makes verification offline, so there is nothing to revoke against. |
| `gravix.io` domain and DNS | not provisioned | — none yet | — none yet | n/a until registered. | — | None today. Once registered: the documented endpoints, the docs site, and every role address below. |
| `security@`, `conduct@`, `trademark@` | not provisioned; reporting goes through GitHub private vulnerability reporting and the issue tracker, as `SECURITY.md` and `TRADEMARK.md` both say | — none yet | — none yet | n/a until the domain exists. | — | None today, because nothing depends on them yet. Once published: a security reporter with no way to reach anybody. |
| Docs-site hosting | not provisioned — `docs-site/` is a Docusaurus project with no deployment workflow | — none yet | — none yet | n/a until deployed. | — | None today. Once deployed: the published documentation goes offline and cannot be corrected, which is where every link in this repository points. |
| Homebrew tap | does not exist | — none yet | — none yet | n/a. | — | None today. Once it exists: `brew install` stops working and the formula cannot be updated, including to withdraw a bad release. |
| Trademark registration | not registered — Gravix™ is an unregistered common-law mark (`TRADEMARK.md`) | — none | — none | There is no registration to transfer. Common-law rights attach to continued use of the name, so what carries them is the project continuing, not a document changing hands. | — | Nobody can stop a confusing fork from using the name without a fight nobody is funded to have. This was a deliberate choice, not an oversight. |

Read the register as two groups. Rows with a `Verified` date are **live assets with one custodian**:
those are the ones that can be lost. Rows with a dash do not exist yet, and they are listed anyway,
because the useful moment to give something two custodians is before anyone depends on it — an asset
that appears in the register only once it exists is one that gets provisioned by one person on a
Tuesday and inherited by nobody.

## The concentration is the design, and it has a bill

The single highest-blast-radius asset is the **GitHub account**, and it is not close.

`GRVX-709` chose keyless cosign over a long-lived signing key. That was the right call and this page
is where its cost lands. With keyless signing there is no private key to escrow, split or lose —
whoever controls the repository controls the release identity. So the key-management risk did not
disappear; it moved, and it all moved into the same place.

Then it moved one step further than it should have. The repository lives under a **personal GitHub
account** rather than an organisation. An organisation can have several owners, and an owner who
stops answering can be replaced by the others. A personal account cannot: GitHub's recovery process
returns access to the account holder, and no collaborator, however trusted, can inherit it.

So the correct reading of this register is not "eleven assets, each at one custodian". It is: **one
account, holding the repository, the releases, the signing identity, the images and the canonical
URL, with exactly one person able to reach it — and no procedure, anywhere, that changes that.**

Everything that can be done about it is in
[What it would take to make this page true](#what-it-would-take-to-make-this-page-true). Nothing
that can be done about it is a document.

## What survives anyway

An honest plan says what is *not* at risk, or the reader over-corrects.

- **The code.** Apache-2.0, on every clone and every proxy that ever fetched it. A fork is a
  complete, legal continuation, and charter §7.3 Q4 means nothing already released can be taken back.
- **Past releases.** Their signatures are in a public transparency log. They stay verifiable whoever
  holds the account.
- **The Go module cache.** Versions already fetched by the module proxy stay fetchable at their
  recorded hashes.
- **The data.** Nothing here touches an operator's facts, which live in their own object store.

What does not survive is **continuity of identity**: the assurance that the next thing published as
Gravix comes from the same place as the last one. A fork can carry the code forward immediately. It
cannot carry the name, the URLs or the signing identity, and the gap between those two facts is
exactly what this page is measuring.

## Trigger conditions

| Trigger | Confirmation | Action |
|---|---|---|
| **Planned handover** | The departing custodian confirms, in writing, in a council minute | Transfer, verify the new custodian can independently get in, update the register's `Verified` date |
| **90 days unreachable** | Two council members attest publicly, in a minuted decision | The secondary custodian assumes primary; the register is updated the same day |
| **Incapacity or death** | A council decision, publicly minuted | The secondary assumes primary; a new secondary is appointed within 30 days |
| **Compromise** | The `security-engineer` role declares it; no vote, no waiting | Immediate rotation of everything the compromised custodian could reach, then a minute explaining what was rotated |

The 90-day threshold is deliberately long and deliberately public. Declaring somebody unreachable is
a serious act with an obvious failure mode — it is the move an impatient or hostile party would
reach for — and it should require patience, two named attestations and a written record that anyone
can read afterwards.

Three of these four triggers route through the council
([`council.md`](council.md)), which currently has one member, so three of the four cannot be
executed. That is the same single fact as everything else on this page.

## No company is load-bearing

No legal entity holds any asset in this register. There is no company to dissolve, no assignment to
unwind and no contract whose lapse strands anything. The assets are held by an individual and, when
there is a second custodian, will be held by individuals in council roles.

This is on purpose and it survives the obvious objection. A company would give the trademark a
cleaner home and make procurement easier, and it would also become a single point of failure with a
registrar, a bank and a jurisdiction attached. [`GRVX-1506`](specs/GRVX-1506-foundation-donation-evaluation.md)
evaluates the alternative — donating to a foundation, which is the version of this that *is* worth
considering, because a foundation is built to outlive its founders and a company is not.

## The annual drill

Once a year, for each provisioned asset, the secondary custodian attempts recovery **without the
primary's help**, records the result, and updates the `Verified` date. Failures are fixed before the
drill counts as complete.

[`succession-drill.md`](succession-drill.md) records every year's results publicly, failures
included. A published failed drill is more reassuring than an unpublished successful one, because it
is evidence the drill is real.

The 2026 drill is recorded there as **not completed**, because it cannot be: a drill needs a second
person and there is not one. That is the finding, not an excuse for skipping it.

## What it would take to make this page true

In the order that removes the most risk per unit of effort. None of it can be done by an
implementer; every item needs the founder, and two need another human being to agree.

1. **Move the repository to a GitHub organisation.** This is the one that matters. It costs an
   afternoon, it breaks no URL (GitHub redirects), and it turns "one account nobody can inherit"
   into "an account type that can have a second owner at all". Everything else is blocked behind it.
2. **Appoint a second organisation owner.** Needs a second person who meets
   [the ladder](contribution-ladder.md). This is the real blocker and it is a recruitment problem,
   not a documentation one.
3. **Add a second custodian to npm and PyPI.** Both support multiple owners per package. Cheap, and
   independent of items 1 and 2 once a second person exists.
4. **Escrow the `ee/` licence key with two custodians before it signs anything.** The only item here
   that is easier to do right the first time than to fix, because a licence key that has already
   signed cannot be quietly replaced.
5. **Run the drill.** Not a document — the secondary actually logging in, unaided, and writing down
   what happened.

Items 1 and 3 are single-person tasks available today. They are recorded in
[`open-decisions.md`](open-decisions.md) as waiting on the owner, and in
[`MAINTAINERS.md`](../../MAINTAINERS.md) as an open risk, because a risk that lives only in a plan
nobody has opened is not recorded.

## Running the audit

```bash
./scripts/verify_custody.sh
```

Exit `0` when every provisioned asset has two custodians verified within twelve months, `1` when one
does not or when a succession file contains a secret or a personal detail, `2` when the register
cannot be read. It runs monthly in
[`.github/workflows/succession-audit.yml`](../../.github/workflows/succession-audit.yml), and it is
red today.

A permanently red check is normally a bad idea — people learn to ignore it, and then the actionable
failures stop being seen too. This one keeps its teeth anyway, and the difference is worth stating:
a subsystem with one reviewer can be recovered by forking, which is why
[`bus_factor.sh`](../../scripts/bus_factor.sh) passes a gap that is written down. An identity with
one custodian cannot be recovered at all. There is no equivalent of a fork for an npm account.
