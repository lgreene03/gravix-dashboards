# Gravix Enterprise Edition (`ee/`)

This directory is **source-available, not open source.**

You can read this code, audit it, and build from it. You cannot use it in production without a
licence, and it is not covered by an OSI-approved licence. Calling it "open source" would be
inaccurate, so we don't.

Everything **outside** this directory is Apache-2.0 and free forever. Per the Open-Core Charter
§7.3 Q4, a capability released under Apache-2.0 can never be moved in here — once open, always
open. See [`../docs/oss/00-open-core-charter.md`](../docs/oss/00-open-core-charter.md).

## The invariant that matters

**The repository must build and pass its full test suite with this directory deleted.**

```bash
make build-oss   # builds the Apache-2.0 core with ee/ physically removed
make test-oss    # builds and tests it with ee/ physically removed
```

Both run on every pull request. If either fails, the core has grown a dependency on commercial
code and that is a charter violation, not a build error to work around.

## No core package may import `ee/`

```bash
make check-boundary
```

This fails if any package outside `ee/` imports anything inside it. The two rules together are
what make the free product genuinely independent rather than nominally so.

## Licence

BUSL-1.1 with a **two-year** Change Date, converting to **Apache License, Version 2.0**.

Two years rather than the BUSL default of four: Sentry, having used BUSL and then written its
replacement, published the observation that a four-year Change Date "can make it feel like the
eventual change to Open Source is only a token effort." That is correct, and an `ee/` feature's
competitive value has decayed well before then. See [`LICENSE`](LICENSE).

## What belongs in here

Only capabilities that solve a problem appearing when an organisation runs Gravix **for other
people** or **at organisational scale** — many tenants, many installs, many identity providers,
many auditors, many invoices. A feature must fail all five questions of the Crippleware Test
(charter §7.3) to be placed here, and "people would pay for it" is never sufficient.

The authoritative placement map is [`../docs/oss/boundary.yaml`](../docs/oss/boundary.yaml).
