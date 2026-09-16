# Responsible disclosure policy

**This policy now lives in [`SECURITY.md`](../SECURITY.md) at the repository root**, where GitHub
surfaces it automatically and where researchers look first.

Everything that was here has moved there, including the safe-harbour commitment, the disclosure
timing, and the scope list. Two things changed rather than moved:

1. **The contact channel.** This document advertised `security@gravix.io`. That domain does not
   resolve, so no report sent to it was ever delivered. Reports now go through
   [GitHub private vulnerability reporting](https://github.com/lgreene03/gravix-dashboards/security/advisories/new),
   which works today. See [`oss/spec-defects.md`](oss/spec-defects.md) SD-003.

2. **The PGP key.** This document referenced a key at `/.well-known/pgp-key.txt`. No such file
   exists in this repository and no `.well-known` directory is served. The reference is removed
   rather than left dangling — GitHub's private reporting channel is already encrypted in transit
   and at rest, so it covers the need the key was there to serve.

The response commitments are now **stricter** than they were here: acknowledgement within 24 hours
rather than 2 business days, and triage within 72 hours rather than 5 business days.
