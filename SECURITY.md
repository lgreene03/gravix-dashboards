# Security policy

## Reporting a vulnerability

Use GitHub's **[private vulnerability reporting](https://github.com/lgreene03/gravix-dashboards/security/advisories/new)**
on this repository. It is confidential, reaches the maintainers directly, and needs no account
beyond the GitHub one you already have.

> **Do not open a public issue for a vulnerability. Do not post it in Discussions, a pull request,
> or a comment.**

*A dedicated `security@` address will be published once the project's domain is provisioned. Until
then, private vulnerability reporting is the only channel that actually reaches us — an earlier
version of `docs/security.md` advertised an address at a domain that does not resolve, and that has
been corrected rather than propagated. See [`docs/oss/spec-defects.md`](docs/oss/spec-defects.md)
SD-003.*

## What to include

- The affected version or commit
- The affected component — ingestion, gateway, rollup, dashboard, SDK, Helm chart
- **The concrete attack path**: what an attacker does, step by step
- The observed impact
- A reproduction, if you have one

> A report without a concrete attack path is difficult to triage. "This looks unsafe" is a code
> review comment, not a vulnerability report — still welcome, just send it as an issue instead.

## Our commitments

| Stage | Commitment |
|---|---|
| Acknowledgement | within 24 hours |
| Triage and severity assessment | within 72 hours |
| Fix or documented mitigation, critical | within 7 days |
| Fix or documented mitigation, high | within 30 days |
| Coordinated public disclosure | with your credit, unless you ask otherwise |

**A security fix bypasses the normal release train and ships immediately.**

These commitments apply to everyone. There is no paid tier that gets faster security handling, and
there never will be — see the [Open-Core Charter](docs/oss/00-open-core-charter.md) §7.3.

## Severity

- **Critical** — unauthenticated remote code execution, or one tenant reading another tenant's facts.
- **High** — authentication bypass, privilege escalation, or credential disclosure.
- **Medium** — denial of service against ingestion, or information disclosure limited to metadata.
- **Low** — issues requiring an already-privileged position or an unrealistic precondition.

## Supported versions

This table is generated from the release record (`docs/oss/releases.json`) by
`make supported-versions`, and CI fails if it goes stale. A security policy that claims a support
window the maintainers do not actually honour is worse than no policy, so it is not maintained by
hand.

<!-- BEGIN GENERATED: supported-versions -->
| Version | Line | Supported until | Receives |
|---|---|---|---|
| _none yet_ | — | — | Gravix has cut no versioned release. The default branch receives everything. |
<!-- END GENERATED: supported-versions -->

The full policy — what each line receives, how long an LTS is supported, and what is backported to
one — is [`docs/oss/lts-policy.md`](docs/oss/lts-policy.md).

**Security fixes are free on every supported line, always.** Charter §7.3 Q3. Paid support
(`GRVX-1504`) sells response time and advice; it does not sell patches, and there is no licence
check anywhere in the backport path.

## No bounty

> Gravix does not operate a paid bug bounty. We credit every reporter who wants credit, and we will
> say so publicly. We would rather tell you that plainly than imply a reward that does not exist.

## Scope

**In scope:** this repository's code, the published container images, the Helm chart, and the SDKs.

**Out of scope:**

- Findings requiring an attacker to already hold valid administrator credentials
- Denial of service by sending more traffic than the documented capacity
- Vulnerabilities in third-party dependencies with no Gravix-specific exploit path — please report
  those upstream, and tell us so we can bump the dependency
- Social engineering of maintainers or users
- Physical attacks
- Third-party services we do not operate (Stripe, cloud providers)
- Self-hosted instances running modified code

## Safe harbour

*Migrated verbatim in substance from `docs/responsible-disclosure.md`, which this document
supersedes. It is the most valuable thing that document contained.*

We will not pursue legal action against researchers who:

- act in good faith and follow this policy;
- do not access, modify, or delete data belonging to other users;
- report findings promptly and do not publicly disclose before coordinated disclosure;
- do not use automated scanning that generates excessive traffic against infrastructure they do
  not own.

If you are unsure whether something you are about to try falls inside this, ask first through the
private reporting channel. We would rather answer that question than have you not look.

## Disclosure timing

Coordinated disclosure happens after a fix is available, with a **90-day maximum** from your report
regardless of fix status. If we have not fixed it in 90 days, you are free to publish, and we will
not consider that a breach of this policy. A deadline that only we control is not a commitment.

## Recognition

With your permission, we credit you in the advisory and the release notes. If you would rather stay
anonymous, say so and we will not name you.

## Verifying a release

Every release is signed with keyless cosign and its build is reproducible. You can confirm that a
binary came from this repository, and that it matches the published source, before running it:

```bash
make verify-reproducible   # build twice, compare digests
```

Full instructions, including signature and SBOM verification:
[`docs/verifying-releases.md`](docs/verifying-releases.md).

## Past advisories

See [`docs/security-advisories.md`](docs/security-advisories.md).
