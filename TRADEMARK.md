# Trademark policy

## Summary

**The Gravix code is free. The Gravix name is not the code.**

Gravix is licensed under Apache-2.0, and §6 of that licence grants **no** trademark rights. That is
normal and deliberate: a permissive code licence and a reserved name serve different purposes. This
document says what the name may be used for, so you do not have to ask a lawyer — or us.

Gravix™ is an unregistered common-law mark. No registration is claimed.

## What you may do without asking

- **Fork the repository**, publish your fork, and modify it however you like.
- **State truthfully** that your product "works with Gravix", "is compatible with Gravix", or
  "is built on Gravix".
- **Write about Gravix** — blog posts, talks, books, courses, reviews, comparisons, and criticism.
  Especially criticism.
- **Name a package after its origin**, such as `gravix-exporter-foo` or `gravix-notifier-bar`.
- **Run Gravix** internally, commercially, at any scale, and say that you do.
- **Offer consulting, training, or support _for_ Gravix.**

None of this needs our permission, a contract, or a logo from us.

## What needs permission

- **Naming your fork or derivative product "Gravix"**, or a name likely to be confused with it —
  because a user reporting a bug in "Gravix" needs us to know which code they are running.
- **Using the Gravix logo as your product's or company's logo** — that is an identity claim, not a
  compatibility statement.
- **Offering a hosted service named Gravix** — the same confusion problem, at a larger scale.
  *(Separately, the BUSL Additional Use Grant in [`ee/LICENSE`](ee/LICENSE) governs hosting the
  Enterprise code. That is a licence question; this is a naming question. They are independent.)*
- **Domain names, social handles, or app-store listings whose primary element is "Gravix"** —
  because these are the places people look for the official project.
- **Suggesting official endorsement, affiliation, or certification** — we do not endorse anyone.

## Naming your fork

Not permitted:

- `Gravix Plus`
- `GravixHQ`
- `Gravix Cloud by Example Ltd`

Permitted:

- `Foo Metrics (a fork of Gravix)`
- `Foo, based on Gravix`
- `Example Observability, powered by Gravix`

The distinction, stated once:

> **Use our name to describe your relationship to us, not as your identity.**

## Why we reserve the name

> So that when a user reports a bug in "Gravix", we know which code they are running. Name
> confusion in an observability tool is not a branding problem; it is a debugging problem.

That is the entire reason. We are not protecting a brand's market value; we are protecting the
ability to answer the question "which version are you running" with something more useful than a
guess.

## Asking

Open an issue on [the repository](https://github.com/lgreene03/gravix-dashboards/issues) titled
`Trademark: <what you want to do>`, or contact a maintainer listed in
[`MAINTAINERS.md`](MAINTAINERS.md) through GitHub.

**We will respond within 14 days.** Permission is usually granted for anything that does not create
confusion about who supports the software.

*A dedicated `trademark@` address will replace the issue tracker once the project's domain is
provisioned. Until then, GitHub is the channel that actually reaches us.*

## Changes to this policy

Changes follow the amendment procedure in the
[Open-Core Charter](docs/oss/00-open-core-charter.md) §6.

**No change will retroactively restrict a fork that complied with this policy at the time it was
published.** If you named something in good faith under the rules as they stood, those rules
continue to apply to you.
